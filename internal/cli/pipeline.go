package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jerryfane/gitmoot/internal/daemon"
	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/pipeline"
	"github.com/jerryfane/gitmoot/internal/runtime"
)

// runPipeline is the CLI for the #681 pipeline registry: define, inspect, and
// toggle declarative pipelines. run/resume/cancel and the per-run funnel view
// arrive in later steps and are deliberately not registered here (only what
// already works is exposed). The mux mirrors runAgentHeartbeat's shape.
func runPipeline(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPipelineUsage(stdout)
		return 0
	}
	switch args[0] {
	case "add":
		return runPipelineAdd(args[1:], stdout, stderr)
	case "list":
		return runPipelineList(args[1:], stdout, stderr)
	case "show":
		return runPipelineShow(args[1:], stdout, stderr)
	case "enable":
		return runPipelineSetEnabled(args[1:], true, stdout, stderr)
	case "disable":
		return runPipelineSetEnabled(args[1:], false, stdout, stderr)
	case "remove":
		return runPipelineRemove(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown pipeline command %q\n\n", args[0])
		printPipelineUsage(stderr)
		return 2
	}
}

func printPipelineUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gitmoot pipeline add <spec.yaml> [--enable]")
	fmt.Fprintln(w, "  gitmoot pipeline list [--json]")
	fmt.Fprintln(w, "  gitmoot pipeline show <name> [--json]")
	fmt.Fprintln(w, "  gitmoot pipeline enable <name>")
	fmt.Fprintln(w, "  gitmoot pipeline disable <name>")
	fmt.Fprintln(w, "  gitmoot pipeline remove <name>")
}

// pipelineRunnerAgentName derives the hidden shell runner agent name for a
// pipeline. It carries the recognizable "pipeline-...-runner" marker so
// isPipelineRunnerAgentName can hide it from `agent list`, mirroring the
// ephemeral-agent naming precedent in workflow/result.go. The pipeline name is a
// validated name-safe token, so no slugging is needed.
func pipelineRunnerAgentName(pipelineName string) string {
	return "pipeline-" + strings.TrimSpace(pipelineName) + "-runner"
}

// isPipelineRunnerAgentName reports whether an agent name is a pipeline's hidden
// shell runner (#681). Such agents are an implementation detail of the pipeline
// executor and are filtered out of the default `agent list` output, mirroring the
// isEphemeralAgentName filter.
func isPipelineRunnerAgentName(name string) bool {
	return strings.HasPrefix(name, "pipeline-") && strings.HasSuffix(name, "-runner") &&
		len(name) > len("pipeline--runner")
}

func runPipelineAdd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPipelineUsage(stderr)
		if len(args) == 0 {
			fmt.Fprintln(stderr, "pipeline add requires a spec file")
			return 2
		}
		return 0
	}
	specFile := strings.TrimSpace(args[0])
	fs := flag.NewFlagSet("pipeline add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	enable := fs.Bool("enable", false, "enable the pipeline immediately (default disabled)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "pipeline add accepts exactly one spec file")
		return 2
	}
	raw, err := os.ReadFile(specFile)
	if err != nil {
		fmt.Fprintf(stderr, "pipeline add: read spec: %v\n", err)
		return 1
	}
	spec, err := pipeline.Load(raw)
	if err != nil {
		fmt.Fprintf(stderr, "pipeline add: %v\n", err)
		return 2
	}
	repo := ""
	if strings.TrimSpace(spec.Repo) != "" {
		parsed, err := daemon.ParseRepository(spec.Repo)
		if err != nil {
			fmt.Fprintf(stderr, "pipeline add: invalid repo %q: %v\n", spec.Repo, err)
			return 2
		}
		repo = parsed.FullName()
	}
	interval, jitter := "", ""
	if spec.Schedule != nil {
		interval = spec.Schedule.Interval
		jitter = spec.Schedule.Jitter
	}
	record := db.Pipeline{
		Name:     spec.Name,
		Repo:     repo,
		SpecYAML: string(raw),
		SpecHash: pipeline.Hash(raw),
		Interval: interval,
		Jitter:   jitter,
	}
	runnerName := pipelineRunnerAgentName(spec.Name)
	var finalEnabled bool
	if err := withStore(*home, func(store *db.Store) error {
		// Refuse to clobber a real managed agent that happens to occupy the runner
		// name: a pre-existing non-shell agent by that name is a naming collision, not
		// this pipeline's runner. A pre-existing shell agent (this pipeline's own
		// runner from an earlier add) is fine — the upsert below is idempotent.
		if existing, err := store.GetAgent(context.Background(), runnerName); err == nil && existing.Runtime != runtime.ShellRuntime {
			return fmt.Errorf("runner agent name %q collides with an existing %s agent", runnerName, existing.Runtime)
		}
		if err := store.CreateOrUpdatePipeline(context.Background(), record); err != nil {
			return err
		}
		if err := store.UpsertAgent(context.Background(), pipelineRunnerAgent(runnerName, repo)); err != nil {
			return err
		}
		if *enable {
			if err := store.SetPipelineEnabled(context.Background(), spec.Name, true); err != nil {
				return err
			}
		}
		// Report the RESULTING enabled state, not just this invocation's --enable:
		// re-adding an edited spec preserves an already-enabled pipeline.
		saved, _, err := store.GetPipeline(context.Background(), spec.Name)
		if err != nil {
			return err
		}
		finalEnabled = saved.Enabled
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "pipeline add: %v\n", err)
		return 1
	}
	writeLine(stdout, "added pipeline %s (%s, %d stages)", spec.Name, enabledLabel(finalEnabled), len(spec.Stages))
	return 0
}

// pipelineRunnerAgent builds the hidden shell agent that owns a pipeline's stage
// jobs. The stage command travels per-job (via the stage job's runtime-override
// ref), NOT on this agent's runtime_ref, so one runner serves every stage. It is
// least-privilege read-only (the shell adapter runs the command verbatim; the
// policy is nominal for shell) and holds only the "ask" capability, matching the
// stage jobs' Action.
func pipelineRunnerAgent(name, repo string) db.Agent {
	return db.Agent{
		Name:           name,
		Role:           "pipeline-runner",
		Runtime:        runtime.ShellRuntime,
		RepoScope:      repo,
		Capabilities:   []string{"ask"},
		AutonomyPolicy: runtime.AutonomyPolicyReadOnly,
		HealthStatus:   "unknown",
	}
}

type pipelineStageJSON struct {
	ID               string   `json:"id"`
	Cmd              string   `json:"cmd"`
	Needs            []string `json:"needs,omitempty"`
	Timeout          string   `json:"timeout,omitempty"`
	Retry            int      `json:"retry,omitempty"`
	SuccessDecisions []string `json:"success_decisions,omitempty"`
}

type pipelineJSON struct {
	Name       string              `json:"name"`
	Repo       string              `json:"repo,omitempty"`
	Enabled    bool                `json:"enabled"`
	Interval   string              `json:"interval,omitempty"`
	Jitter     string              `json:"jitter,omitempty"`
	SpecHash   string              `json:"spec_hash"`
	Stages     []pipelineStageJSON `json:"stages,omitempty"`
	LastRunAt  string              `json:"last_run_at,omitempty"`
	NextDueAt  string              `json:"next_due_at,omitempty"`
	LastRunID  string              `json:"last_run_id,omitempty"`
	LastStatus string              `json:"last_status,omitempty"`
}

func runPipelineList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pipeline list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	jsonOut := fs.Bool("json", false, "print the pipelines as a JSON array")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "pipeline list does not accept positional arguments")
		return 2
	}
	var pipelines []db.Pipeline
	if err := withStore(*home, func(store *db.Store) error {
		var err error
		pipelines, err = store.ListPipelines(context.Background())
		return err
	}); err != nil {
		fmt.Fprintf(stderr, "pipeline list: %v\n", err)
		return 1
	}
	if *jsonOut {
		out := make([]pipelineJSON, 0, len(pipelines))
		for _, p := range pipelines {
			out = append(out, pipelineToJSON(p, false))
		}
		return encodePipelineJSON(stdout, stderr, out)
	}
	for _, p := range pipelines {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", p.Name, enabledLabel(p.Enabled), firstNonEmpty(p.Interval, "-"), firstNonEmpty(p.Repo, "-"), firstNonEmpty(p.LastStatus, "-"))
	}
	return 0
}

func runPipelineShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pipeline show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	jsonOut := fs.Bool("json", false, "print the pipeline as JSON")
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPipelineUsage(stderr)
		if len(args) == 0 {
			fmt.Fprintln(stderr, "pipeline show requires a name")
			return 2
		}
		return 0
	}
	name := strings.TrimSpace(args[0])
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "pipeline show accepts exactly one name")
		return 2
	}
	var (
		record db.Pipeline
		found  bool
	)
	if err := withStore(*home, func(store *db.Store) error {
		var err error
		record, found, err = store.GetPipeline(context.Background(), name)
		return err
	}); err != nil {
		fmt.Fprintf(stderr, "pipeline show: %v\n", err)
		return 1
	}
	if !found {
		fmt.Fprintf(stderr, "pipeline %s not found\n", name)
		return 1
	}
	if *jsonOut {
		return encodePipelineJSON(stdout, stderr, pipelineToJSON(record, true))
	}
	printPipeline(stdout, record)
	return 0
}

func runPipelineSetEnabled(args []string, enabled bool, stdout, stderr io.Writer) int {
	verb := "enable"
	if !enabled {
		verb = "disable"
	}
	fs := flag.NewFlagSet("pipeline "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPipelineUsage(stderr)
		if len(args) == 0 {
			fmt.Fprintf(stderr, "pipeline %s requires a name\n", verb)
			return 2
		}
		return 0
	}
	name := strings.TrimSpace(args[0])
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "pipeline %s accepts exactly one name\n", verb)
		return 2
	}
	if err := withStore(*home, func(store *db.Store) error {
		return store.SetPipelineEnabled(context.Background(), name, enabled)
	}); err != nil {
		fmt.Fprintf(stderr, "pipeline %s: %v\n", verb, err)
		return 1
	}
	writeLine(stdout, "%sd pipeline %s", verb, name)
	return 0
}

func runPipelineRemove(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pipeline remove", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPipelineUsage(stderr)
		if len(args) == 0 {
			fmt.Fprintln(stderr, "pipeline remove requires a name")
			return 2
		}
		return 0
	}
	name := strings.TrimSpace(args[0])
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "pipeline remove accepts exactly one name")
		return 2
	}
	var removed bool
	if err := withStore(*home, func(store *db.Store) error {
		var err error
		removed, err = store.DeletePipeline(context.Background(), name)
		if err != nil {
			return err
		}
		// Best-effort: dispose the hidden runner agent so removing a pipeline does not
		// leak it. Ignore the outcome — the pipeline row is what `remove` is about, and
		// leaving an orphan runner is harmless (run/job cleanup lands in the run step).
		_, _ = store.RemoveAgent(context.Background(), pipelineRunnerAgentName(name))
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "pipeline remove: %v\n", err)
		return 1
	}
	if !removed {
		fmt.Fprintf(stderr, "pipeline %s not found\n", name)
		return 1
	}
	writeLine(stdout, "removed pipeline %s", name)
	return 0
}

// pipelineToJSON projects a stored pipeline row into its script-stable JSON
// shape. When withStages is set it parses the stored (already-validated) spec to
// enumerate the stage DAG; a parse failure degrades to no stages rather than
// failing the command.
func pipelineToJSON(record db.Pipeline, withStages bool) pipelineJSON {
	out := pipelineJSON{
		Name:       record.Name,
		Repo:       record.Repo,
		Enabled:    record.Enabled,
		Interval:   record.Interval,
		Jitter:     record.Jitter,
		SpecHash:   record.SpecHash,
		LastRunAt:  heartbeatTimeForStatus(record.LastRunAt),
		NextDueAt:  heartbeatTimeForStatus(record.NextDueAt),
		LastRunID:  record.LastRunID,
		LastStatus: record.LastStatus,
	}
	if out.LastRunAt == "-" {
		out.LastRunAt = ""
	}
	if out.NextDueAt == "-" {
		out.NextDueAt = ""
	}
	if withStages {
		if spec, err := pipeline.Load([]byte(record.SpecYAML)); err == nil {
			for _, stage := range spec.Stages {
				out.Stages = append(out.Stages, pipelineStageJSON{
					ID:               stage.ID,
					Cmd:              stage.Cmd,
					Needs:            stage.Needs,
					Timeout:          stage.Timeout,
					Retry:            stage.Retry,
					SuccessDecisions: stage.SuccessDecisions,
				})
			}
		}
	}
	return out
}

func printPipeline(stdout io.Writer, record db.Pipeline) {
	writeLine(stdout, "name: %s", record.Name)
	writeLine(stdout, "repo: %s", firstNonEmpty(record.Repo, "-"))
	writeLine(stdout, "enabled: %t", record.Enabled)
	writeLine(stdout, "interval: %s", firstNonEmpty(record.Interval, "-"))
	writeLine(stdout, "jitter: %s", firstNonEmpty(record.Jitter, "-"))
	writeLine(stdout, "spec_hash: %s", record.SpecHash)
	writeLine(stdout, "last_run: %s", heartbeatTimeForStatus(record.LastRunAt))
	writeLine(stdout, "next_due: %s", heartbeatTimeForStatus(record.NextDueAt))
	writeLine(stdout, "last_status: %s", firstNonEmpty(record.LastStatus, "-"))
	writeLine(stdout, "last_run_id: %s", firstNonEmpty(record.LastRunID, "-"))
	writeLine(stdout, "stages:")
	spec, err := pipeline.Load([]byte(record.SpecYAML))
	if err != nil {
		writeLine(stdout, "  (unparseable stored spec: %v)", err)
		return
	}
	for _, stage := range spec.Stages {
		needs := "-"
		if len(stage.Needs) > 0 {
			needs = strings.Join(stage.Needs, ",")
		}
		writeLine(stdout, "  %s\tneeds=%s\tcmd=%s", stage.ID, needs, stage.Cmd)
	}
}

func encodePipelineJSON(stdout, stderr io.Writer, value any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		fmt.Fprintf(stderr, "pipeline: %v\n", err)
		return 1
	}
	return 0
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
