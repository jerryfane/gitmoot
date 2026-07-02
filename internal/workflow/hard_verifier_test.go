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

// recordingHardVerifierDispatcher is a stub HardVerifierDispatcher that records the
// call and returns a canned hard OutcomeReviewed (or err/skip), so the engine trigger
// can be tested without a real sandbox.
type recordingHardVerifierDispatcher struct {
	called  int
	jobIDs  []string
	heads   []string
	outcome Outcome
	ok      bool
	err     error
}

func (r *recordingHardVerifierDispatcher) Verify(_ context.Context, job db.Job, _ JobPayload, mergedHead string) (Outcome, bool, error) {
	r.called++
	r.jobIDs = append(r.jobIDs, job.ID)
	r.heads = append(r.heads, mergedHead)
	return r.outcome, r.ok, r.err
}

// TestEngineDispatchesHardVerifierLegOnMerge proves the hard-verifier leg fires on a
// merge, attributed to the implement job at the PR head, and its HardVerifier
// OutcomeReviewed is harvested (#474).
func TestEngineDispatchesHardVerifierLegOnMerge(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	harvester := &recordingHarvester{}
	engine.OutcomeHarvester = harvester
	dispatcher := &recordingHardVerifierDispatcher{
		ok: true,
		outcome: Outcome{
			Kind: OutcomeReviewed, HardVerifier: true, HardPassed: true,
			Repo: "jerryfane/gitmoot", PullRequest: 7,
			Rubric: map[string]float64{"go test ./...": 1.0},
		},
	}
	engine.HardVerifierDispatcher = dispatcher
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob returned error: %v", err)
	}
	if dispatcher.called != 1 {
		t.Fatalf("hard verifier dispatcher called %d times, want 1", dispatcher.called)
	}
	if dispatcher.jobIDs[0] != "implement-job" {
		t.Fatalf("hard verifier leg attributed to %q, want implement-job", dispatcher.jobIDs[0])
	}
	if dispatcher.heads[0] != "head123" {
		t.Fatalf("hard verifier leg head sha = %q, want the PR head head123", dispatcher.heads[0])
	}
	var sawHardReviewed bool
	kinds := map[OutcomeKind]bool{}
	for _, o := range harvester.snapshot() {
		kinds[o.Kind] = true
		if o.Kind == OutcomeReviewed && o.HardVerifier {
			sawHardReviewed = true
		}
	}
	if !kinds[OutcomeMerged] {
		t.Fatalf("harvested kinds = %v, want the merged floor", kinds)
	}
	if !sawHardReviewed {
		t.Fatalf("expected a HardVerifier=true OutcomeReviewed harvest, got %v", harvester.snapshot())
	}
}

// TestEngineNilHardVerifierDispatcherIsByteIdentical proves off-by-default: with no
// HardVerifierDispatcher the merge advances exactly as before and no verifier leg runs.
func TestEngineNilHardVerifierDispatcherIsByteIdentical(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	engine.OutcomeHarvester = &recordingHarvester{}
	// No HardVerifierDispatcher.
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob with nil hard verifier dispatcher returned error: %v", err)
	}
	assertTaskState(t, store, "task-7", TaskMerged)
	for _, o := range engine.OutcomeHarvester.(*recordingHarvester).snapshot() {
		if o.Kind == OutcomeReviewed && o.HardVerifier {
			t.Fatal("nil hard verifier dispatcher must not produce a hard reviewed outcome")
		}
	}
}

// TestEngineHardVerifierDispatchErrorNeverFailsMerge proves a verifier-leg failure is
// best-effort: the merge still completes and a hard_verifiers_failed event is recorded
// on the implement job.
func TestEngineHardVerifierDispatchErrorNeverFailsMerge(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	engine.OutcomeHarvester = &recordingHarvester{}
	engine.HardVerifierDispatcher = &recordingHardVerifierDispatcher{err: errors.New("verifier boom")}
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob must not fail on a verifier error, got: %v", err)
	}
	assertTaskState(t, store, "task-7", TaskMerged)

	events, err := store.ListJobEvents(ctx, "implement-job")
	if err != nil {
		t.Fatalf("ListJobEvents returned error: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == "hard_verifiers_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a hard_verifiers_failed job event, got %+v", events)
	}
}

// TestEngineHardVerifierDispatchSkipWritesNothing proves a SKIP (ok=false: no verdict
// producible) writes no hard row and never errors.
func TestEngineHardVerifierDispatchSkipWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	harvester := &recordingHarvester{}
	engine.OutcomeHarvester = harvester
	engine.HardVerifierDispatcher = &recordingHardVerifierDispatcher{ok: false}
	seedMergeReviewJobs(t, store)

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob returned error: %v", err)
	}
	for _, o := range harvester.snapshot() {
		if o.Kind == OutcomeReviewed && o.HardVerifier {
			t.Fatal("a SKIP must not harvest a hard reviewed outcome")
		}
	}
}

// blockingHardVerifierDispatcher blocks inside Verify until released, modeling a
// wedged test suite.
type blockingHardVerifierDispatcher struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingHardVerifierDispatcher) Verify(_ context.Context, _ db.Job, _ JobPayload, _ string) (Outcome, bool, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return Outcome{Kind: OutcomeReviewed, HardVerifier: true, HardPassed: true, Repo: "jerryfane/gitmoot", PullRequest: 7, Rubric: map[string]float64{"go test ./...": 1.0}}, true, nil
}

// TestEngineHardVerifierLegDoesNotBlockAdvanceJob is the detach regression: even when
// the verifier blocks indefinitely (a wedged suite), AdvanceJob returns PROMPTLY (the
// merge completes) because the hard-verifier leg is DETACHED into its own goroutine —
// it never stalls AdvanceJob, the worker tick, or the daemon's checkoutLock. The
// detached verifier still runs once unblocked, and the goroutine is race-tested.
func TestEngineHardVerifierLegDoesNotBlockAdvanceJob(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	// Build an engine with the PRODUCTION default async spawn (no synchronous
	// HardVerifierSpawner override), so the detached goroutine is exercised for real.
	engine := Engine{
		Store: store,
		JobID: func(request JobRequest) string {
			return strings.Join([]string{request.Action, request.Agent, request.TaskID}, "-")
		},
	}
	engine.MergeGate = &fakeMergeGate{decision: MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	harvester := &recordingHarvester{}
	engine.OutcomeHarvester = harvester
	dispatcher := &blockingHardVerifierDispatcher{started: make(chan struct{}), release: make(chan struct{})}
	engine.HardVerifierDispatcher = dispatcher
	seedMergeReviewJobs(t, store)

	done := make(chan error, 1)
	go func() { done <- engine.AdvanceJob(ctx, "review-job") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AdvanceJob returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AdvanceJob did not return while the verifier was blocked — the hard-verifier leg is NOT detached")
	}
	assertTaskState(t, store, "task-7", TaskMerged)

	select {
	case <-dispatcher.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the detached hard-verifier leg never started")
	}

	close(dispatcher.release)
	deadline := time.After(5 * time.Second)
	for {
		harvested := false
		for _, o := range harvester.snapshot() {
			if o.Kind == OutcomeReviewed && o.HardVerifier {
				harvested = true
			}
		}
		if harvested {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the detached verifier never harvested its hard OutcomeReviewed after release")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
