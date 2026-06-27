package cli

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/github"
	"github.com/jerryfane/gitmoot/internal/subprocess"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// checkerDiffStub is a diffFileLister that returns canned PullRequestFile patches so
// the pure-Go diff-size metric is deterministic.
type checkerDiffStub struct {
	files []github.PullRequestFile
	err   error
}

func (s checkerDiffStub) ListPullRequestFiles(context.Context, github.Repository, int64) ([]github.PullRequestFile, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.files, nil
}

// fakeCheckerRunner is a subprocess.Runner whose LookPath only finds the listed
// binaries and whose Run returns canned stdout per command — so a tool runner can be
// exercised deterministically AND a LookPath miss can be modeled (the degrade-skip
// path). A binary not in present makes LookPath fail (the tool is "not installed").
type fakeCheckerRunner struct {
	present map[string]bool   // binaries on PATH
	stdout  map[string]string // command -> canned stdout
	runErr  map[string]error  // command -> error to return from Run
	calls   []string          // commands actually Run
}

func (r *fakeCheckerRunner) LookPath(file string) (string, error) {
	if r.present[file] {
		return "/usr/bin/" + file, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", file)
}

func (r *fakeCheckerRunner) Run(_ context.Context, _ string, command string, _ ...string) (subprocess.Result, error) {
	r.calls = append(r.calls, command)
	if err := r.runErr[command]; err != nil {
		return subprocess.Result{}, err
	}
	return subprocess.Result{Command: command, Stdout: r.stdout[command]}, nil
}

var _ subprocess.Runner = (*fakeCheckerRunner)(nil)

// canonicalImplementPayload is the implement job payload the dispatcher checks
// against (a real merged PR on a real repo).
func canonicalImplementPayload() workflow.JobPayload {
	return workflow.JobPayload{Repo: "jerryfane/gitmoot", PullRequest: 7}
}

// smallDiffFiles is a tight (well-under-cap) diff: a few changed lines.
func smallDiffFiles() []github.PullRequestFile {
	return []github.PullRequestFile{
		{Filename: "a.go", Patch: "@@ -1,2 +1,3 @@\n unchanged\n+added one\n+added two\n-removed one\n"},
	}
}

// TestDiffSizeAlwaysProducesADimension: diff_size is pure-Go and needs no tool/
// checkout, so it ALWAYS produces a dimension from the canned PR patches — a tight
// diff scores high (1.0).
func TestDiffSizeAlwaysProducesADimension(t *testing.T) {
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{files: smallDiffFiles()},
		checkers: []string{checkerDiffSize},
		// No runner, no checkout: tool checkers would skip, but diff_size still runs.
	}
	outcome, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil {
		t.Fatalf("Check returned error: %v", err)
	}
	if !ok {
		t.Fatal("diff_size must always produce a dimension (ok=true)")
	}
	if !outcome.Objective {
		t.Fatal("the deterministic-checker outcome must be tagged Objective=true")
	}
	if outcome.Kind != workflow.OutcomeReviewed {
		t.Fatalf("outcome kind = %q, want reviewed", outcome.Kind)
	}
	score, present := outcome.Rubric[checkerDiffSize]
	if !present {
		t.Fatalf("diff_size dimension missing from rubric %v", outcome.Rubric)
	}
	if score != 1.0 {
		t.Fatalf("a tight diff must score 1.0, got %v", score)
	}
	// HeadSHA / repo / PR carried through for the harvester's per-PR keying.
	if outcome.HeadSHA != "head123" || outcome.Repo != "jerryfane/gitmoot" || outcome.PullRequest != 7 {
		t.Fatalf("outcome context not carried through: %+v", outcome)
	}
}

// TestDiffSizeLargeDiffScoresLower: a diff far over the soft cap scores below 1.0
// (a larger-than-budget change is a weaker objective signal).
func TestDiffSizeLargeDiffScoresLower(t *testing.T) {
	// Build a patch with many more changed lines than the soft cap.
	patch := "@@ -1,1 +1,600 @@\n"
	for i := 0; i < 600; i++ {
		patch += "+line\n"
	}
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{files: []github.PullRequestFile{{Filename: "big.go", Patch: patch}}},
		checkers: []string{checkerDiffSize},
	}
	outcome, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil || !ok {
		t.Fatalf("Check ok=%v err=%v", ok, err)
	}
	if score := outcome.Rubric[checkerDiffSize]; score >= 1.0 {
		t.Fatalf("a large (>cap) diff must score below 1.0, got %v", score)
	}
}

// TestToolCheckerDegradesSkipsOnLookPathMiss: when a tool binary is absent (LookPath
// miss) the dimension is OMITTED (no row for it) and the leg never errors — the
// degrade-skip contract. diff_size still survives.
func TestToolCheckerDegradesSkipsOnLookPathMiss(t *testing.T) {
	runner := &fakeCheckerRunner{present: map[string]bool{}} // nothing installed
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{files: smallDiffFiles()},
		runner:   runner,
		checkout: t.TempDir(), // a checkout exists, but no tools do
		checkers: []string{checkerDiffSize, checkerDuplication, checkerLint, checkerComplexity},
	}
	outcome, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil {
		t.Fatalf("a LookPath miss must NEVER error, got: %v", err)
	}
	if !ok {
		t.Fatal("diff_size must still survive a tool-less host")
	}
	if len(outcome.Rubric) != 1 {
		t.Fatalf("only diff_size should survive, got %v", outcome.Rubric)
	}
	if _, present := outcome.Rubric[checkerDiffSize]; !present {
		t.Fatalf("diff_size must survive, got %v", outcome.Rubric)
	}
	for _, skipped := range []string{checkerDuplication, checkerLint, checkerComplexity} {
		if _, present := outcome.Rubric[skipped]; present {
			t.Fatalf("%s must be SKIPPED on a LookPath miss, got %v", skipped, outcome.Rubric)
		}
	}
}

// TestToolCheckerSkipsWithoutCheckout: with no checkout the tool dims skip (they need
// a working tree) but diff_size (pure-Go, no checkout) still runs.
func TestToolCheckerSkipsWithoutCheckout(t *testing.T) {
	runner := &fakeCheckerRunner{present: map[string]bool{"dupl": true, "golangci-lint": true, "gocyclo": true}}
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{files: smallDiffFiles()},
		runner:   runner,
		checkout: "", // NO checkout
		checkers: []string{checkerDiffSize, checkerDuplication, checkerLint, checkerComplexity},
	}
	outcome, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil || !ok {
		t.Fatalf("Check ok=%v err=%v", ok, err)
	}
	if len(outcome.Rubric) != 1 || outcome.Rubric[checkerDiffSize] == 0 {
		t.Fatalf("without a checkout only diff_size should run, got %v", outcome.Rubric)
	}
}

// TestToolCheckerProducesDimensionWhenAvailable: with the tool binaries present AND a
// checkout, the tool dimensions ARE produced from the canned tool output (the
// best-effort happy path that DOES run a subprocess).
func TestToolCheckerProducesDimensionWhenAvailable(t *testing.T) {
	runner := &fakeCheckerRunner{
		present: map[string]bool{"dupl": true, "golangci-lint": true, "gocyclo": true},
		stdout: map[string]string{
			"dupl":          "",           // no clones reported => 1.0
			"golangci-lint": "",           // no issues => 1.0
			"gocyclo":       "fn1\nfn2\n", // 2 over-threshold functions
		},
	}
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{files: smallDiffFiles()},
		runner:   runner,
		checkout: t.TempDir(),
		checkers: []string{checkerDuplication, checkerLint, checkerComplexity},
	}
	outcome, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil || !ok {
		t.Fatalf("Check ok=%v err=%v", ok, err)
	}
	if outcome.Rubric[checkerDuplication] != 1.0 {
		t.Fatalf("no-clones duplication must score 1.0, got %v", outcome.Rubric[checkerDuplication])
	}
	if outcome.Rubric[checkerLint] != 1.0 {
		t.Fatalf("no-issues lint must score 1.0, got %v", outcome.Rubric[checkerLint])
	}
	// gocyclo reported 2 over-threshold functions => 1.0 - 2*0.1 = 0.8.
	if got := outcome.Rubric[checkerComplexity]; got < 0.79 || got > 0.81 {
		t.Fatalf("complexity with 2 over-threshold funcs must score ~0.8, got %v", got)
	}
}

// TestToolCheckerErrorDegradesToSkip: a tool that ERRORS (e.g. a crash) skips that
// dimension rather than failing the leg.
func TestToolCheckerErrorDegradesToSkip(t *testing.T) {
	runner := &fakeCheckerRunner{
		present: map[string]bool{"gocyclo": true},
		runErr:  map[string]error{"gocyclo": errors.New("gocyclo crashed")},
	}
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{files: smallDiffFiles()},
		runner:   runner,
		checkout: t.TempDir(),
		checkers: []string{checkerComplexity},
	}
	_, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil {
		t.Fatalf("a tool error must NEVER error the leg, got: %v", err)
	}
	if ok {
		t.Fatal("a sole-checker error must yield ok=false (no producible dimension, no row)")
	}
}

// TestCheckerEmptyWhenEverythingSkips: when diff-size cannot read (PR file read
// fails) AND no tools/checkout exist, NOTHING is producible => ok=false (no checker
// row). The all-skipped fail-safe.
func TestCheckerEmptyWhenEverythingSkips(t *testing.T) {
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{err: errors.New("github down")},
		checkers: []string{checkerDiffSize, checkerLint},
		// no runner, no checkout
	}
	_, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil {
		t.Fatalf("an all-skipped run must NEVER error, got: %v", err)
	}
	if ok {
		t.Fatal("an all-skipped run must yield ok=false (no checker row)")
	}
}

// TestCheckerSelectorHonored: only the SELECTED checkers run — a diff_size-only
// selector never invokes a tool runner even when binaries are present.
func TestCheckerSelectorHonored(t *testing.T) {
	runner := &fakeCheckerRunner{present: map[string]bool{"dupl": true, "golangci-lint": true, "gocyclo": true}}
	d := &deterministicCheckerDispatcher{
		diff:     checkerDiffStub{files: smallDiffFiles()},
		runner:   runner,
		checkout: t.TempDir(),
		checkers: []string{checkerDiffSize}, // ONLY diff_size selected
	}
	outcome, ok, err := d.Check(context.Background(), db.Job{ID: "implement-job"}, canonicalImplementPayload(), "head123")
	if err != nil || !ok {
		t.Fatalf("Check ok=%v err=%v", ok, err)
	}
	if len(outcome.Rubric) != 1 {
		t.Fatalf("only diff_size selected, got %v", outcome.Rubric)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("a diff_size-only selector must invoke NO tool runner, ran %v", runner.calls)
	}
}

// TestCountPatchChangedLines: the pure-Go patch parser counts +/- body lines and
// ignores the +++/---/@@ headers.
func TestCountPatchChangedLines(t *testing.T) {
	patch := "--- a/x.go\n+++ b/x.go\n@@ -1,3 +1,4 @@\n context\n+added\n-removed\n+another\n"
	if got := countPatchChangedLines(patch); got != 3 {
		t.Fatalf("countPatchChangedLines = %d, want 3 (+added, -removed, +another)", got)
	}
	if got := countPatchChangedLines(""); got != 0 {
		t.Fatalf("empty patch must count 0, got %d", got)
	}
}
