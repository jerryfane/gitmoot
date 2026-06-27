package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jerryfane/gitmoot/internal/db"
)

// recordingCheckerDispatcher is a stub DeterministicCheckerDispatcher that records
// the call and returns a canned objective OutcomeReviewed (or err/skip), so the
// engine trigger can be tested without external tools.
type recordingCheckerDispatcher struct {
	called  int
	jobIDs  []string
	heads   []string
	outcome Outcome
	ok      bool
	err     error
}

func (r *recordingCheckerDispatcher) Check(_ context.Context, job db.Job, _ JobPayload, mergedHead string) (Outcome, bool, error) {
	r.called++
	r.jobIDs = append(r.jobIDs, job.ID)
	r.heads = append(r.heads, mergedHead)
	return r.outcome, r.ok, r.err
}

// TestEngineDispatchesCheckerLegOnMerge proves the objective deterministic-checker
// leg fires on a merge, attributed to the implement job, and its objective
// OutcomeReviewed is harvested (#485).
func TestEngineDispatchesCheckerLegOnMerge(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	harvester := &recordingHarvester{}
	engine.OutcomeHarvester = harvester
	dispatcher := &recordingCheckerDispatcher{
		ok: true,
		outcome: Outcome{
			Kind: OutcomeReviewed, Objective: true, Repo: "jerryfane/gitmoot", PullRequest: 7,
			Rubric: map[string]float64{"diff_size": 0.9},
		},
	}
	engine.DeterministicCheckerDispatcher = dispatcher
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob returned error: %v", err)
	}
	if dispatcher.called != 1 {
		t.Fatalf("checker dispatcher called %d times, want 1", dispatcher.called)
	}
	if dispatcher.jobIDs[0] != "implement-job" {
		t.Fatalf("checker leg attributed to %q, want implement-job", dispatcher.jobIDs[0])
	}
	if dispatcher.heads[0] != "head123" {
		t.Fatalf("checker leg head sha = %q, want the PR head head123", dispatcher.heads[0])
	}
	// Both the merge floor AND the objective checker outcome are harvested; the
	// checker outcome carries Objective=true.
	var sawObjectiveReviewed bool
	kinds := map[OutcomeKind]bool{}
	for _, o := range harvester.snapshot() {
		kinds[o.Kind] = true
		if o.Kind == OutcomeReviewed && o.Objective {
			sawObjectiveReviewed = true
		}
	}
	if !kinds[OutcomeMerged] {
		t.Fatalf("harvested kinds = %v, want the merged floor", kinds)
	}
	if !sawObjectiveReviewed {
		t.Fatalf("expected an Objective=true OutcomeReviewed harvest, got %v", harvester.snapshot())
	}
}

// TestEngineNilCheckerDispatcherIsByteIdentical proves off-by-default: with no
// DeterministicCheckerDispatcher the merge advances exactly as before and no checker
// leg runs (no Objective outcome harvested).
func TestEngineNilCheckerDispatcherIsByteIdentical(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	engine.OutcomeHarvester = &recordingHarvester{}
	// No DeterministicCheckerDispatcher.
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob with nil checker dispatcher returned error: %v", err)
	}
	assertTaskState(t, store, "task-7", TaskMerged)
	for _, o := range engine.OutcomeHarvester.(*recordingHarvester).snapshot() {
		if o.Kind == OutcomeReviewed && o.Objective {
			t.Fatal("nil checker dispatcher must not produce an objective reviewed outcome")
		}
	}
}

// TestEngineCheckerDispatchErrorNeverFailsMerge proves a checker-leg failure is
// best-effort: the merge still completes and a deterministic_checkers_failed event
// is recorded on the implement job (the checker never blocks/fails the job).
func TestEngineCheckerDispatchErrorNeverFailsMerge(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	engine.OutcomeHarvester = &recordingHarvester{}
	engine.DeterministicCheckerDispatcher = &recordingCheckerDispatcher{err: errors.New("checker boom")}
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob must not fail on a checker error, got: %v", err)
	}
	assertTaskState(t, store, "task-7", TaskMerged)

	events, err := store.ListJobEvents(ctx, "implement-job")
	if err != nil {
		t.Fatalf("ListJobEvents returned error: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == "deterministic_checkers_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a deterministic_checkers_failed job event, got %+v", events)
	}
}

// TestEngineCheckerDispatchSkipWritesNothing proves a SKIP (ok=false, every checker
// skipped) writes no checker row and never errors.
func TestEngineCheckerDispatchSkipWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	harvester := &recordingHarvester{}
	engine.OutcomeHarvester = harvester
	engine.DeterministicCheckerDispatcher = &recordingCheckerDispatcher{ok: false}
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob returned error: %v", err)
	}
	for _, o := range harvester.snapshot() {
		if o.Kind == OutcomeReviewed && o.Objective {
			t.Fatal("a SKIP must not harvest an objective reviewed outcome")
		}
	}
}

// blockingCheckerDispatcher blocks inside Check until released, modeling a wedged
// external tool. started fires once Check is entered.
type blockingCheckerDispatcher struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingCheckerDispatcher) Check(_ context.Context, _ db.Job, _ JobPayload, _ string) (Outcome, bool, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return Outcome{Kind: OutcomeReviewed, Objective: true, Repo: "jerryfane/gitmoot", PullRequest: 7, Rubric: map[string]float64{"diff_size": 1.0}}, true, nil
}

// TestEngineCheckerLegDoesNotBlockAdvanceJob is the detach regression: even when the
// checker tool blocks indefinitely, AdvanceJob returns PROMPTLY (the merge completes)
// because the checker leg is DETACHED into its own goroutine — it never stalls
// AdvanceJob, the worker tick, or the daemon's checkoutLock. The detached checker
// still runs once unblocked, and the goroutine is exercised for real (race-tested).
func TestEngineCheckerLegDoesNotBlockAdvanceJob(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	// Build an engine with the PRODUCTION default async spawn (no synchronous
	// CheckerSpawner override), so the detached goroutine is exercised for real.
	engine := Engine{
		Store: store,
		JobID: func(request JobRequest) string {
			return strings.Join([]string{request.Action, request.Agent, request.TaskID}, "-")
		},
	}
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	harvester := &recordingHarvester{}
	engine.OutcomeHarvester = harvester
	dispatcher := &blockingCheckerDispatcher{started: make(chan struct{}), release: make(chan struct{})}
	engine.DeterministicCheckerDispatcher = dispatcher
	seedMergeReviewJobs(t, store)

	done := make(chan error, 1)
	go func() { done <- engine.AdvanceJob(ctx, "review-job") }()

	// AdvanceJob must return promptly despite the wedged checker.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AdvanceJob returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AdvanceJob did not return while the checker was blocked — the checker leg is NOT detached")
	}
	assertTaskState(t, store, "task-7", TaskMerged)

	// The detached checker goroutine did enter Check (proving it was dispatched).
	select {
	case <-dispatcher.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the detached checker leg never started")
	}

	// Release the checker and confirm the detached leg eventually harvests.
	close(dispatcher.release)
	deadline := time.After(5 * time.Second)
	for {
		harvested := false
		for _, o := range harvester.snapshot() {
			if o.Kind == OutcomeReviewed && o.Objective {
				harvested = true
			}
		}
		if harvested {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the detached checker never harvested its objective OutcomeReviewed after release")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
