package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/skillopt"
	"github.com/jerryfane/gitmoot/internal/subprocess"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// fakeSandboxProvisioner is a sandboxProvisioner that returns a canned dir (or an
// error) and records the ref it was asked to provision + whether cleanup ran.
type fakeSandboxProvisioner struct {
	dir         string
	err         error
	provisioned string
	cleaned     bool
}

func (p *fakeSandboxProvisioner) Provision(_ context.Context, ref string) (string, func(), error) {
	p.provisioned = ref
	if p.err != nil {
		return "", nil, p.err
	}
	return p.dir, func() { p.cleaned = true }, nil
}

// fakeVerifierRunner is a subprocess.Runner that returns a canned error per verifier
// command (the `sh -c <command>` argument) and records the working DIR each command
// ran in — so a test can assert the commands ran in the SANDBOX, never the real
// checkout. An optional onRun hook lets a test model a command's side effect.
type fakeVerifierRunner struct {
	errByCommand map[string]error
	onRun        func(dir, command string)
	calls        []verifierCall
}

type verifierCall struct {
	dir     string
	command string
}

func (r *fakeVerifierRunner) Run(_ context.Context, dir string, command string, args ...string) (subprocess.Result, error) {
	verifier := ""
	if command == "sh" && len(args) == 2 && args[0] == "-c" {
		verifier = args[1]
	}
	r.calls = append(r.calls, verifierCall{dir: dir, command: verifier})
	if r.onRun != nil {
		r.onRun(dir, verifier)
	}
	return subprocess.Result{}, r.errByCommand[verifier]
}

func (r *fakeVerifierRunner) LookPath(file string) (string, error) { return "/bin/" + file, nil }

var _ subprocess.Runner = (*fakeVerifierRunner)(nil)

func hardVerifierImplementPayload() workflow.JobPayload {
	return workflow.JobPayload{Repo: "owner/repo", PullRequest: 7}
}

// TestHardVerifierAllPassIsPass: every command exits 0 → HardPassed=true, ok=true, a
// per-command rubric of all-1.0, tagged HardVerifier, and the sandbox is cleaned up.
func TestHardVerifierAllPassIsPass(t *testing.T) {
	prov := &fakeSandboxProvisioner{dir: "/sandbox/wt"}
	runner := &fakeVerifierRunner{errByCommand: map[string]error{}} // no errors => all pass
	d := &hardVerifierDispatcher{
		runner:   runner,
		sandbox:  prov,
		commands: []string{"go build ./...", "go test ./..."},
	}
	outcome, ok, err := d.Verify(context.Background(), db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), "deadbeefcafe")
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !ok {
		t.Fatal("all-pass verifiers must produce a verdict (ok=true)")
	}
	if !outcome.HardVerifier || !outcome.HardPassed {
		t.Fatalf("outcome = HardVerifier=%v HardPassed=%v, want both true", outcome.HardVerifier, outcome.HardPassed)
	}
	if outcome.Kind != workflow.OutcomeReviewed {
		t.Fatalf("outcome kind = %q, want OutcomeReviewed", outcome.Kind)
	}
	if outcome.Rubric["go build ./..."] != 1.0 || outcome.Rubric["go test ./..."] != 1.0 {
		t.Fatalf("rubric = %v, want all 1.0", outcome.Rubric)
	}
	if !prov.cleaned {
		t.Fatal("the sandbox cleanup must run")
	}
	if prov.provisioned != "deadbeefcafe" {
		t.Fatalf("provisioned ref = %q, want the merged head", prov.provisioned)
	}
}

// TestHardVerifierAnyFailIsFail: a SINGLE failing command (fail-closed set membership)
// makes the whole verdict FAIL, and that command's rubric entry is 0.0.
func TestHardVerifierAnyFailIsFail(t *testing.T) {
	prov := &fakeSandboxProvisioner{dir: "/sandbox/wt"}
	runner := &fakeVerifierRunner{errByCommand: map[string]error{
		"go test ./...": errors.New("exit status 1"),
	}}
	d := &hardVerifierDispatcher{
		runner:   runner,
		sandbox:  prov,
		commands: []string{"go build ./...", "go test ./..."},
	}
	outcome, ok, err := d.Verify(context.Background(), db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), "head")
	if err != nil || !ok {
		t.Fatalf("Verify = ok %v err %v, want ok=true err=nil", ok, err)
	}
	if outcome.HardPassed {
		t.Fatal("a single failing command must make the verdict FAIL")
	}
	if outcome.Rubric["go build ./..."] != 1.0 || outcome.Rubric["go test ./..."] != 0.0 {
		t.Fatalf("rubric = %v, want go build=1.0 go test=0.0", outcome.Rubric)
	}
	if !prov.cleaned {
		t.Fatal("the sandbox cleanup must run even on a FAIL verdict")
	}
}

// TestHardVerifierTimeoutIsFail: a command whose Run returns a context-deadline error
// (the leg's bounded context cancelled it) is a FAIL, never a silent pass.
func TestHardVerifierTimeoutIsFail(t *testing.T) {
	prov := &fakeSandboxProvisioner{dir: "/sandbox/wt"}
	runner := &fakeVerifierRunner{errByCommand: map[string]error{
		"slow-suite": context.DeadlineExceeded,
	}}
	d := &hardVerifierDispatcher{
		runner:   runner,
		sandbox:  prov,
		commands: []string{"slow-suite"},
	}
	outcome, ok, err := d.Verify(context.Background(), db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), "head")
	if err != nil || !ok {
		t.Fatalf("Verify = ok %v err %v, want ok=true err=nil", ok, err)
	}
	if outcome.HardPassed {
		t.Fatal("a timed-out (context-cancelled) verifier must FAIL, not pass")
	}
}

// TestHardVerifierRunsInSandboxNotRealCheckout: EVERY verifier command runs with its
// working dir set to the SANDBOX the provisioner returned, never any other checkout.
// This is the isolation contract the tier depends on for cheat-proofing.
func TestHardVerifierRunsInSandboxNotRealCheckout(t *testing.T) {
	const sandboxDir = "/tmp/gitmoot-hardverify-XYZ/wt"
	prov := &fakeSandboxProvisioner{dir: sandboxDir}
	runner := &fakeVerifierRunner{errByCommand: map[string]error{}}
	d := &hardVerifierDispatcher{
		runner:   runner,
		sandbox:  prov,
		commands: []string{"cmd-one", "cmd-two"},
	}
	if _, ok, err := d.Verify(context.Background(), db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), "head"); err != nil || !ok {
		t.Fatalf("Verify = ok %v err %v, want ok=true", ok, err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("expected two verifier runs, got %d", len(runner.calls))
	}
	for _, call := range runner.calls {
		if call.dir != sandboxDir {
			t.Fatalf("verifier %q ran in %q, want the sandbox %q", call.command, call.dir, sandboxDir)
		}
	}
}

// TestHardVerifierUnprovisionableSandboxSkips: a provisioner error yields ok=false (no
// hard row) and runs NO verifier — the leg degrade-skips, never fails the merge.
func TestHardVerifierUnprovisionableSandboxSkips(t *testing.T) {
	prov := &fakeSandboxProvisioner{err: errors.New("merged head not present in base checkout")}
	runner := &fakeVerifierRunner{errByCommand: map[string]error{}}
	d := &hardVerifierDispatcher{
		runner:   runner,
		sandbox:  prov,
		commands: []string{"go test ./..."},
	}
	outcome, ok, err := d.Verify(context.Background(), db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), "head")
	if err != nil {
		t.Fatalf("Verify must not error on an unprovisionable sandbox, got %v", err)
	}
	if ok {
		t.Fatalf("an unprovisionable sandbox must skip (ok=false), got outcome %+v", outcome)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("no verifier must run without a sandbox, got %d calls", len(runner.calls))
	}
}

// TestHardVerifierEmptyInputsSkip: no commands, no runner/provisioner, or an empty
// merged head all skip (ok=false) — byte-identical no-op guards.
func TestHardVerifierEmptyInputsSkip(t *testing.T) {
	prov := &fakeSandboxProvisioner{dir: "/sandbox/wt"}
	runner := &fakeVerifierRunner{errByCommand: map[string]error{}}
	cases := []struct {
		name string
		d    *hardVerifierDispatcher
		head string
	}{
		{"no commands", &hardVerifierDispatcher{runner: runner, sandbox: prov}, "head"},
		{"nil runner", &hardVerifierDispatcher{sandbox: prov, commands: []string{"go test ./..."}}, "head"},
		{"nil sandbox", &hardVerifierDispatcher{runner: runner, commands: []string{"go test ./..."}}, "head"},
		{"empty head", &hardVerifierDispatcher{runner: runner, sandbox: prov, commands: []string{"go test ./..."}}, "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok, err := tc.d.Verify(context.Background(), db.Job{ID: "j"}, hardVerifierImplementPayload(), tc.head)
			if err != nil || ok {
				t.Fatalf("%s: Verify = ok %v err %v, want ok=false err=nil", tc.name, ok, err)
			}
		})
	}
}

// --- E2E (deterministic, real git worktree sandbox + real sh) ---

// gitFixtureRepo inits a real git repo with one committed fixture file and returns the
// repo dir + the committed HEAD SHA — the sandbox is provisioned at this SHA.
func gitFixtureRepo(t *testing.T, marker string) (string, string) {
	t.Helper()
	repoDir := t.TempDir()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "test@example.com")
	runGit(t, repoDir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "marker.txt"), []byte(marker), 0o600); err != nil {
		t.Fatalf("write fixture marker: %v", err)
	}
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "fixture")
	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return repoDir, strings.TrimSpace(string(out))
}

// realHardVerifierDispatcher wires the REAL worktree provisioner + real ExecRunner
// against a fixture repo, so the E2E exercises actual git worktree isolation and
// actual `sh -c` exit codes — no fakes.
func realHardVerifierDispatcher(repoDir string, commands []string) *hardVerifierDispatcher {
	return &hardVerifierDispatcher{
		runner: subprocess.GroupRunner{},
		sandbox: worktreeSandboxProvisioner{
			base:   repoDir,
			runner: subprocess.ExecRunner{},
		},
		commands: commands,
	}
}

// installHardVerifierTemplate installs a template and returns its version + an
// implement payload attributed to it, so the real harvester resolves the version.
func installHardVerifierTemplate(t *testing.T, store *db.Store) (db.AgentTemplateVersion, workflow.JobPayload) {
	t.Helper()
	version := installChainTemplate(t, store, "planner")
	payload := workflow.JobPayload{
		Repo:                   "owner/repo",
		PullRequest:            7,
		TaskID:                 "task-1",
		TemplateID:             version.TemplateID,
		TemplateResolvedCommit: version.ResolvedCommit,
	}
	return version, payload
}

// TestHardVerifierE2EPassFlowsIntoHardScore drives a REAL fixture repo + a REAL
// exit-0 verifier (`test -f marker.txt` — the committed file IS present in the fresh
// checkout) through the REAL harvester, proving a PASS lands a choice "a" hard row
// whose persisted hard_score is 1.0 (EvaluatorScore.Hard=1.0).
func TestHardVerifierE2EPassFlowsIntoHardScore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repoDir, sha := gitFixtureRepo(t, "hello")
	d := realHardVerifierDispatcher(repoDir, []string{"test -f marker.txt"})

	outcome, ok, err := d.Verify(ctx, db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), sha)
	if err != nil || !ok {
		t.Fatalf("Verify = ok %v err %v, want ok=true", ok, err)
	}
	if !outcome.HardPassed {
		t.Fatal("exit-0 verifier on the fresh checkout must PASS (the committed marker.txt exists)")
	}

	store := openTraceChainStore(t)
	version, payload := installHardVerifierTemplate(t, store)
	h := skillopt.NewOutcomeHarvester(store, nil)
	if err := h.Harvest(ctx, db.Job{ID: "implement-job", Type: "implement"}, payload, outcome); err != nil {
		t.Fatalf("Harvest returned error: %v", err)
	}
	events := autoTraceFeedback(t, store, version.ID)
	if len(events) != 1 || events[0].Choice != "a" {
		t.Fatalf("expected one choice=a hard row, got %+v", events)
	}
	assertPersistedHardScore(t, store, version.ID, 1.0, true)
}

// TestHardVerifierE2EFailFlowsIntoHardScore drives a REAL exit-1 verifier through the
// REAL harvester, proving a FAIL lands a choice "b" hard row whose persisted
// hard_score is 0.0 (EvaluatorScore.Hard=0.0). This is the mutation guard for the
// exit-code mapping: if pass/fail were inverted, this row would be choice "a".
func TestHardVerifierE2EFailFlowsIntoHardScore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repoDir, sha := gitFixtureRepo(t, "hello")
	// The file does NOT exist in the checkout → `test -f` exits 1 → FAIL.
	d := realHardVerifierDispatcher(repoDir, []string{"test -f does-not-exist.txt"})

	outcome, ok, err := d.Verify(ctx, db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), sha)
	if err != nil || !ok {
		t.Fatalf("Verify = ok %v err %v, want ok=true", ok, err)
	}
	if outcome.HardPassed {
		t.Fatal("exit-1 verifier must FAIL")
	}

	store := openTraceChainStore(t)
	version, payload := installHardVerifierTemplate(t, store)
	h := skillopt.NewOutcomeHarvester(store, nil)
	if err := h.Harvest(ctx, db.Job{ID: "implement-job", Type: "implement"}, payload, outcome); err != nil {
		t.Fatalf("Harvest returned error: %v", err)
	}
	events := autoTraceFeedback(t, store, version.ID)
	if len(events) != 1 || events[0].Choice != "b" {
		t.Fatalf("expected one choice=b hard row, got %+v", events)
	}
	assertPersistedHardScore(t, store, version.ID, 0.0, false)
}

// TestHardVerifierE2ESandboxIsolatesWrites drives a REAL verifier that WRITES a file
// (escapee.txt) via a relative path. The write must land in the throwaway sandbox
// (which is then cleaned up), NEVER the real base checkout — proving the freshness /
// isolation guarantee. This is the mutation guard for the sandbox-fresh contract: if
// the verifier ran in the base checkout, escapee.txt would appear there.
func TestHardVerifierE2ESandboxIsolatesWrites(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()
	repoDir, sha := gitFixtureRepo(t, "hello")
	// The verifier writes a relative file, then exits 0.
	d := realHardVerifierDispatcher(repoDir, []string{"echo escaped > escapee.txt"})

	outcome, ok, err := d.Verify(ctx, db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), sha)
	if err != nil || !ok {
		t.Fatalf("Verify = ok %v err %v, want ok=true", ok, err)
	}
	if !outcome.HardPassed {
		t.Fatal("the write-then-exit-0 verifier must PASS")
	}
	// The real base checkout MUST be untouched — the write went to the throwaway
	// sandbox, which was force-removed on cleanup.
	if _, err := os.Stat(filepath.Join(repoDir, "escapee.txt")); !os.IsNotExist(err) {
		t.Fatalf("a verifier write leaked into the REAL checkout %q (isolation broken): stat err=%v", repoDir, err)
	}
}

// TestHardVerifierE2ETimeoutFailsFromRealContext drives a REAL slow command against a
// short leg context, proving a genuine timeout (not a fake) is a FAIL.
func TestHardVerifierE2ETimeoutFailsFromRealContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir, sha := gitFixtureRepo(t, "hello")
	d := realHardVerifierDispatcher(repoDir, []string{"sleep 30"})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	outcome, ok, err := d.Verify(ctx, db.Job{ID: "implement-job"}, hardVerifierImplementPayload(), sha)
	if err != nil || !ok {
		t.Fatalf("Verify = ok %v err %v, want ok=true", ok, err)
	}
	if outcome.HardPassed {
		t.Fatal("a real command killed by the leg's context timeout must FAIL")
	}
}

// assertPersistedHardScore reads the hard item's metadata and asserts the persisted
// hard_score + hard_passed — proving the binary verdict flowed into
// EvaluatorScore.Hard end to end (through the real sandbox and the real harvester).
func assertPersistedHardScore(t *testing.T, store *db.Store, versionID string, wantScore float64, wantPassed bool) {
	t.Helper()
	items, err := store.ListEvalReviewItems(context.Background(), "auto-trace:"+versionID)
	if err != nil {
		t.Fatalf("ListEvalReviewItems returned error: %v", err)
	}
	for _, item := range items {
		if !strings.HasPrefix(item.ItemID, "hard#") {
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal([]byte(item.MetadataJSON), &meta); err != nil {
			t.Fatalf("hard item metadata invalid JSON: %v", err)
		}
		if score, _ := meta["hard_score"].(float64); score != wantScore {
			t.Fatalf("persisted hard_score = %v, want %v", meta["hard_score"], wantScore)
		}
		if passed, _ := meta["hard_passed"].(bool); passed != wantPassed {
			t.Fatalf("persisted hard_passed = %v, want %v", meta["hard_passed"], wantPassed)
		}
		return
	}
	t.Fatalf("no hard# item found for version %s", versionID)
}
