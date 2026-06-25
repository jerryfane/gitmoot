package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
)

// delegationManifest is the on-disk context-manifest.json written for a parent
// job that produced delegations carrying artifacts. It gives each child a
// machine-readable view of its sibling delegations.
type delegationManifest struct {
	ParentJobID string                    `json:"parent_job_id"`
	Delegations []delegationManifestEntry `json:"delegations"`
}

// delegationManifestEntry is the reduced view of a single delegation surfaced in
// context-manifest.json. It exposes the coordination fields children need to
// reason about the wider batch, plus — when InjectUpstreamDepContext is on and
// the delegation has a SUCCEEDED child (#438) — a set of additive, omitempty
// result-reference fields so a downstream reader that prefers structured JSON
// over the prose "Upstream dependency results" block (#419) can reference an
// upstream output precisely. Bulk bodies are NEVER inlined here: OutputPath is
// the only handle to full content (the on-disk brief.md). With the flag off, or
// for a pending/failed/not-yet-run delegation, every result-reference field is
// the zero value and omitempty leaves the entry byte-identical to the reduced
// shape that shipped before #438.
type delegationManifestEntry struct {
	ID       string   `json:"id"`
	Agent    string   `json:"agent"`
	Action   string   `json:"action"`
	Worktree string   `json:"worktree,omitempty"`
	Deps     []string `json:"deps,omitempty"`

	// Decision is the succeeded child's gitmoot_result decision (e.g. "approved").
	Decision string `json:"decision,omitempty"`
	// SummaryPreview is the child's summary capped to maxUpstreamDepSummaryPreviewBytes
	// (rune-safe, with an ellipsis when truncated), mirroring the prose header preview.
	SummaryPreview string `json:"summary_preview,omitempty"`
	// ChangesMade is the COUNT of the child's changes_made entries (not the slice),
	// kept as an int to stay token-cheap and match #419's "[changes_made: N]".
	ChangesMade int `json:"changes_made,omitempty"`
	// PullRequest is the rendered github PR URL for the child, when it opened one.
	PullRequest string `json:"pull_request,omitempty"`
	// OutputPath is the on-disk path to the parent's full brief.md — the only
	// handle to bulk content; the manifest never inlines a body by value.
	OutputPath string `json:"output_path,omitempty"`
	// DerivedFrom is the delegation's own declared Deps (its declared upstream
	// lineage). This is a deliberate simplification: it is the dep's declared
	// upstreams, NOT a transitive/resolved ancestry graph.
	DerivedFrom []string `json:"derived_from,omitempty"`
}

// writeDelegationArtifacts persists the coordinator brief and a context manifest
// for a parent job when at least one of its delegations requests artifacts. It
// mirrors writeAgentTemplateDraft's MkdirAll(0o755)+WriteFile(0o644) pattern and
// returns the directory the artifacts were written to, or "" when nothing was
// written (empty root or no delegation requested artifacts) so callers can skip
// wiring the directory into child prompts. Artifacts live under
// <root>/delegations/<parent-job-id>/, with the parent job id sanitized into a
// single safe path segment because job ids may contain slashes.
func writeDelegationArtifacts(root string, parentJobID string, result *AgentResult) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil
	}
	if result == nil || !delegationsRequestArtifacts(result.Delegations) {
		return "", nil
	}
	segment, err := safeDelegationPathSegment(parentJobID, "parent job id")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "delegations", segment)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create delegation artifact directory: %w", err)
	}
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte(result.ArtifactBody), 0o644); err != nil {
		return "", fmt.Errorf("write delegation brief %s: %w", briefPath, err)
	}
	manifest := delegationManifest{
		ParentJobID: parentJobID,
		Delegations: make([]delegationManifestEntry, 0, len(result.Delegations)),
	}
	for _, d := range result.Delegations {
		manifest.Delegations = append(manifest.Delegations, delegationManifestEntry{
			ID:       d.ID,
			Agent:    d.Agent,
			Action:   d.Action,
			Worktree: d.Worktree,
			Deps:     d.Deps,
		})
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode delegation manifest: %w", err)
	}
	manifestPath := filepath.Join(dir, "context-manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return "", fmt.Errorf("write delegation manifest %s: %w", manifestPath, err)
	}
	return dir, nil
}

// augmentDelegationManifest re-writes context-manifest.json with the enriched
// view (#438): each delegation entry whose child has SUCCEEDED gains the additive
// result-reference fields (decision/summary_preview/changes_made/pull_request/
// output_path/derived_from) so a downstream reader can reference an upstream
// output by structured JSON instead of re-parsing the prose block (#419). It is
// the structured sibling of buildUpstreamDepBlock and rides the identical gate:
// callers MUST only invoke it when Engine.InjectUpstreamDepContext is set, so the
// flag-off path never re-writes the manifest and stays byte-identical to today.
//
// Write timing is WRITE-ONCE-THEN-AUGMENT: writeDelegationArtifacts writes the
// reduced manifest at dispatch (before any dep has run), and this overwrites it
// each advanceDelegations pass once deps have actually reached JobSucceeded. The
// enriched view is recomputed from the current children/dedupWinners every pass
// and produces stable, sorted JSON (delegations in parentResult order, same as
// dispatch), so repeated passes over a stable succeeded set produce byte-identical
// output (idempotent — no churn). It is a no-op (returns the resolved dir, no
// write) when the root is empty or no delegation requested artifacts, mirroring
// writeDelegationArtifacts. It reuses the same MkdirAll(0o755)+WriteFile(0o644)
// pattern so a child reading the directory always sees a complete manifest.
func augmentDelegationManifest(root string, parentJobID string, parentResult *AgentResult, children, dedupWinners map[string]db.Job, e Engine) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil
	}
	if parentResult == nil || !delegationsRequestArtifacts(parentResult.Delegations) {
		return "", nil
	}
	segment, err := safeDelegationPathSegment(parentJobID, "parent job id")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "delegations", segment)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create delegation artifact directory: %w", err)
	}
	manifest := delegationManifest{
		ParentJobID: parentJobID,
		Delegations: make([]delegationManifestEntry, 0, len(parentResult.Delegations)),
	}
	for _, d := range parentResult.Delegations {
		manifest.Delegations = append(manifest.Delegations, buildEnrichedManifestEntry(d, children, dedupWinners, e))
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode delegation manifest: %w", err)
	}
	manifestPath := filepath.Join(dir, "context-manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return "", fmt.Errorf("write delegation manifest %s: %w", manifestPath, err)
	}
	return dir, nil
}

// buildEnrichedManifestEntry returns the reduced manifest entry for a delegation,
// plus the result-reference fields when the delegation has a SUCCEEDED child
// (#438). It mirrors buildUpstreamDepBlock's resolution exactly: the child is
// looked up in children, falling back to dedupWinners so a fingerprint-deduped
// delegation is followed to its winning sibling; only a JobSucceeded child with a
// resolvable payload is enriched. A pending/failed/missing-child delegation
// returns the bare reduced entry (every new field zero, so omitempty omits it),
// keeping the flag-off / not-yet-run shape byte-identical.
func buildEnrichedManifestEntry(d Delegation, children, dedupWinners map[string]db.Job, e Engine) delegationManifestEntry {
	entry := delegationManifestEntry{
		ID:       d.ID,
		Agent:    d.Agent,
		Action:   d.Action,
		Worktree: d.Worktree,
		Deps:     d.Deps,
	}
	child, ok := children[d.ID]
	if !ok {
		// A deduped delegation has no child of its own; follow it to the winner.
		child, ok = dedupWinners[d.ID]
	}
	if !ok || child.State != string(JobSucceeded) {
		return entry
	}
	payload, err := unmarshalPayload(child.Payload)
	if err != nil {
		return entry
	}
	// Defensive on a nil Result, mirroring buildUpstreamDepBlock: a succeeded child
	// with no Result still carries the by-reference handles (PR/output_path/lineage)
	// but no decision/summary/changes from the missing result.
	if payload.Result != nil {
		entry.Decision = strings.TrimSpace(payload.Result.Decision)
		if summary := strings.TrimSpace(payload.Result.Summary); summary != "" {
			entry.SummaryPreview = manifestSummaryPreview(summary)
		}
		entry.ChangesMade = len(payload.Result.ChangesMade)
	}
	entry.PullRequest = childPullRequestLink(payload)
	entry.OutputPath = e.inlineBriefPath(child.ID)
	entry.DerivedFrom = compactStrings(d.Deps)
	return entry
}

// manifestSummaryPreview caps a child's summary to the same short, rune-safe
// preview the prose header uses (maxUpstreamDepSummaryPreviewBytes), appending an
// ellipsis when truncated. Unlike the prose path it does NOT backtick-fence the
// preview: json.MarshalIndent already escapes any embedded backticks/sentinels
// safely, and a fence would corrupt the JSON string. The full body travels by
// reference via OutputPath, so this preview is deliberately short.
func manifestSummaryPreview(summary string) string {
	preview, omitted := truncateUTF8Bytes(summary, maxUpstreamDepSummaryPreviewBytes)
	if omitted > 0 {
		preview = strings.TrimRight(preview, " \t\n") + "…"
	}
	return preview
}

// delegationArtifactDir returns the directory writeDelegationArtifacts would
// have written for this parent, without touching the filesystem. It lets the
// deferred-enqueue path (advanceDelegations) point late-running children at the
// same brief.md/context-manifest.json the ready children received, returning ""
// when no artifacts were requested or the engine has no artifact root.
func delegationArtifactDir(root string, parentJobID string, result *AgentResult) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil
	}
	if result == nil || !delegationsRequestArtifacts(result.Delegations) {
		return "", nil
	}
	segment, err := safeDelegationPathSegment(parentJobID, "parent job id")
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "delegations", segment), nil
}

func delegationsRequestArtifacts(delegations []Delegation) bool {
	for _, d := range delegations {
		if len(d.Artifacts) > 0 {
			return true
		}
	}
	return false
}

// safeDelegationPathSegment reduces a value (typically a parent job id, which may
// contain slashes such as "parent-job/delegation/del-1") to a single path
// segment that cannot escape its parent directory. Unsafe characters are
// replaced with '-' rather than rejected so artifacts can always be written;
// the only failure is an empty or wholly unsafe value.
func safeDelegationPathSegment(value string, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-' || char == '_':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	segment := strings.Trim(builder.String(), "-")
	if segment == "" || segment == "." || segment == ".." {
		return "", fmt.Errorf("%s %q has no safe path representation", label, value)
	}
	return segment, nil
}
