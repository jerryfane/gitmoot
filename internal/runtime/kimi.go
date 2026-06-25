package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jerryfane/gitmoot/internal/subprocess"
)

const (
	KimiLiveCheckPrompt  = "Gitmoot Kimi live check. Return OK only."
	KimiAuthSetupMessage = "Kimi Code background jobs need a logged-in Kimi CLI. Run: kimi login, then restart the Gitmoot daemon so it inherits the session."
)

// KimiAdapter delivers Gitmoot jobs to the local Kimi Code CLI.
// Kimi supports non-interactive prompt mode (`-p`), structured stream-json output,
// and session resume (`-S <session_id>`). The adapter extracts the assistant
// response and the session id from the JSONL stream.
type KimiAdapter struct {
	Runner        subprocess.Runner
	Dir           string
	NewRuntimeRef func() (string, error)
}

func (a KimiAdapter) Name() string { return KimiRuntime }

func (a KimiAdapter) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	if err := validateStartRequest(request.Agent, a.Name(), request.Prompt); err != nil {
		return StartResult{}, err
	}
	// RuntimeRef is only used as a fallback if the stream does not report a session id.
	runtimeRef, err := a.newRuntimeRef()
	if err != nil {
		return StartResult{}, err
	}
	args := kimiPermissionArgs(request.Agent)
	if request.Agent.Model != "" {
		args = append(args, "--model", request.Agent.Model)
	}
	args = append(args, "-p", request.Prompt, "--output-format", "stream-json")
	result, err := a.runner().Run(ctx, a.Dir, "kimi", args...)
	if err != nil {
		return StartResult{Raw: result.Stdout + result.Stderr}, kimiCommandError(result, err)
	}
	content, sessionID, _, parseErr := parseKimiStreamJSON(result.Stdout)
	if parseErr != nil {
		return StartResult{Raw: result.Stdout}, fmt.Errorf("parse kimi stream-json output: %w", parseErr)
	}
	if sessionID == "" {
		sessionID = runtimeRef
	}
	return StartResult{RuntimeRef: sessionID, Raw: content}, nil
}

func (a KimiAdapter) Validate(_ context.Context, agent Agent) error {
	if err := validateRuntime(agent, a.Name()); err != nil {
		return err
	}
	if agent.RuntimeRef != "" && !isKimiSessionID(agent.RuntimeRef) {
		return fmt.Errorf("kimi runtime reference %q must be a Kimi session id or empty", agent.RuntimeRef)
	}
	return nil
}

func (a KimiAdapter) Deliver(ctx context.Context, agent Agent, job Job) (Result, error) {
	if err := a.Validate(ctx, agent); err != nil {
		return Result{}, err
	}
	args := kimiPermissionArgs(agent)
	model := effectiveModel(agent, job)
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, "-S", agent.RuntimeRef, "-p", job.Prompt, "--output-format", "stream-json")
	result, err := a.runner().Run(ctx, a.Dir, "kimi", args...)
	if err != nil {
		return Result{Raw: result.Stdout + result.Stderr}, kimiCommandError(result, err)
	}
	content, _, usage, parseErr := parseKimiStreamJSON(result.Stdout)
	if parseErr != nil {
		return Result{Raw: result.Stdout}, fmt.Errorf("parse kimi stream-json output: %w", parseErr)
	}
	return Result{Raw: content, Summary: strings.TrimSpace(content), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}, nil
}

func (a KimiAdapter) Health(ctx context.Context, agent Agent) error {
	if err := a.Validate(ctx, agent); err != nil {
		return err
	}
	_, err := a.Deliver(ctx, agent, Job{Prompt: KimiLiveCheckPrompt})
	return err
}

func (a KimiAdapter) Capabilities(context.Context) ([]string, error) {
	return []string{"review", "implement", "ask"}, nil
}

func (a KimiAdapter) runner() subprocess.Runner {
	if a.Runner != nil {
		return a.Runner
	}
	return subprocess.GroupRunner{}
}

func (a KimiAdapter) newRuntimeRef() (string, error) {
	if a.NewRuntimeRef != nil {
		return a.NewRuntimeRef()
	}
	return newUUID()
}

func kimiPermissionArgs(agent Agent) []string {
	// Kimi's `-p` prompt mode runs non-interactively. Kimi CLI >= v1.48 requires
	// `--print` for `--output-format stream-json` ("Output format is only supported
	// for print UI"); without it the CLI errors out (exit 2). --yolo/--auto cannot
	// be combined with -p, so --print is the only flag we add; read-only enforcement
	// is handled by the agent's capabilities and Gitmoot's dispatch/lock logic.
	_ = agent
	return []string{"--print"}
}

type kimiStreamEvent struct {
	Role string `json:"role"`
	Type string `json:"type"`
	// Content is a plain JSON string when thinking is off, or an array of typed
	// blocks ([{"type":"text","text":...},{"type":"think","think":...}]) when
	// thinking is on (Kimi CLI >= v1.48). Decoded via kimiContentText.
	Content   json.RawMessage `json:"content"`
	SessionID string          `json:"session_id"`
	// Usage carries best-effort token counts when Kimi's stream emits a usage or
	// result event (#338 Part B). The field set mirrors the common LLM-CLI shape
	// (input_tokens/output_tokens). It is nil/zero on events that carry no usage,
	// so a runtime that never reports usage simply contributes 0 to the budget.
	Usage *kimiUsage `json:"usage"`
}

// kimiUsage holds the best-effort token counts extracted from a Kimi stream-json
// usage/result event. Counts default to 0 when the stream omits usage.
type kimiUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// parseKimiStreamJSON reads Kimi's --output-format stream-json JSONL output and
// returns the concatenated assistant content, the session id from the resume hint
// meta event (if present), and the best-effort token usage from the last event
// that carried a usage object (if any). Usage capture is best-effort: when no
// event reports usage the returned kimiUsage is zero-valued and the job
// contributes 0 to the per-root token budget.
func parseKimiStreamJSON(output string) (string, string, kimiUsage, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var contentParts []string
	var sessionID string
	var usage kimiUsage
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event kimiStreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Kimi CLI >= v1.48 prints the resume hint as a plain-text line rather
			// than a structured meta event. Capture the session id from it.
			if id := kimiResumeIDFromText(line); id != "" {
				sessionID = id
			}
			continue
		}
		if event.Usage != nil {
			usage = *event.Usage
		}
		switch event.Role {
		case "assistant":
			contentParts = append(contentParts, kimiContentText(event.Content))
		case "meta":
			if event.Type == "session.resume_hint" && event.SessionID != "" {
				sessionID = event.SessionID
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", kimiUsage{}, err
	}
	return strings.Join(contentParts, ""), sessionID, usage, nil
}

// kimiContentText extracts assistant text from a Kimi stream event's content
// field. With thinking off it is a plain JSON string; with thinking on (Kimi CLI
// >= v1.48) it is an array of typed blocks, e.g.
// [{"type":"text","text":"..."},{"type":"think","think":"..."}]. Only "text"
// blocks contribute to the response; "think" reasoning blocks are skipped so they
// never pollute the gitmoot_result payload.
func kimiContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Type == "text" {
				b.WriteString(bl.Text)
			}
		}
		return b.String()
	}
	return ""
}

func isKimiSessionID(ref string) bool {
	// Older Kimi CLI emitted "session_"-prefixed ids. Kimi CLI >= v1.48 uses a
	// bare UUID (e.g. "cc9d5c55-6eb7-495e-a86a-634b1699ef1f"); `kimi -r/-S <uuid>`
	// resumes it. Accept both forms.
	return strings.HasPrefix(ref, "session_") || isUUID(ref)
}

// kimiResumeIDFromText extracts the session id from Kimi's plain-text resume
// hint, e.g. "To resume this session: kimi -r <uuid>". Kimi CLI >= v1.48 prints
// this line instead of a structured session.resume_hint stream event.
func kimiResumeIDFromText(line string) string {
	const marker = "kimi -r "
	i := strings.Index(line, marker)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[i+len(marker):])
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	if isUUID(rest) {
		return rest
	}
	return ""
}

func kimiCommandError(result subprocess.Result, err error) error {
	base := commandError(result, err)
	if !isKimiAuthFailure(result) {
		return base
	}
	return fmt.Errorf("Kimi Code authentication required. %s: %w", KimiAuthSetupMessage, base)
}

func isKimiAuthFailure(result subprocess.Result) bool {
	text := strings.ToLower(strings.Join([]string{result.Stdout, result.Stderr}, "\n"))
	return strings.Contains(text, "login") ||
		strings.Contains(text, "authenticate") ||
		strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "authentication")
}
