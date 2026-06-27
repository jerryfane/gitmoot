package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/github"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// TestDeterministicCheckerFullChain is the FULL-CHAIN integration test for #485. It
// drives a real workflow.Engine through AdvanceJob to a MERGE, where the engine
// dispatches the OBJECTIVE deterministic-checker leg (a real
// deterministicCheckerDispatcher whose diff reader returns canned PR patches so
// diff_size is deterministic and whose tool runners all LookPath-miss so only
// diff_size is produced), the engine derives Outcome{Kind:OutcomeReviewed,
// Objective:true}, and the REAL skillopt.OutcomeHarvester writes a THIRD coexisting
// objective FeedbackEvent (gitmoot-checker) into the SAME auto-trace:<versionID> run
// alongside the verifiable floor (gitmoot-auto) — with NO live LLM and NO live
// GitHub. Everything between AdvanceJob and the persisted rows is production code.
//
// It pins the invariants: the checker leg never blocks the job (the merge still
// happens), no candidate/promotion row is written (manual promotion preserved), the
// floor row coexists with the checker row under a distinct item id, and the objective
// dimension lands in the checker item metadata.
func TestDeterministicCheckerFullChain(t *testing.T) {
	ctx := context.Background()
	store := openTraceChainStore(t)
	version := installChainTemplate(t, store, "planner")
	gate := &stubMergeGate{decision: workflow.MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	engine := chainEngine(store, gate)

	// The real harvester (built through the daemon construction path) AND a real
	// deterministic-checker dispatcher: diff_size from canned patches, tool runners
	// all LookPath-miss (so only diff_size is produced).
	engine.OutcomeHarvester = enabledHarvesterFor(t, store, harvestGitHubStub{status: realCIChainStatus()})
	engine.DeterministicCheckerDispatcher = &deterministicCheckerDispatcher{
		store:    store,
		diff:     checkerDiffStub{files: smallDiffFiles()},
		runner:   &fakeCheckerRunner{present: map[string]bool{}}, // nothing installed
		checkout: t.TempDir(),
		checkers: []string{checkerDiffSize, checkerDuplication, checkerLint, checkerComplexity},
	}

	seedCodexImplementJob(t, store, version)
	seedChainApprovingReview(t, store, "head123")

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob returned error: %v", err)
	}

	events := autoTraceFeedback(t, store, version.ID)
	// The verifiable floor (gitmoot-auto) AND the objective checker (gitmoot-checker).
	if len(events) != 2 {
		t.Fatalf("expected the floor + checker row (2), got %d: %+v", len(events), events)
	}
	var floor, checker *db.FeedbackEvent
	for i := range events {
		switch events[i].Reviewer {
		case "gitmoot-auto":
			floor = &events[i]
		case "gitmoot-checker":
			checker = &events[i]
		}
	}
	if floor == nil {
		t.Fatalf("verifiable floor row missing; got %+v", events)
	}
	if checker == nil {
		t.Fatalf("objective checker row missing; got %+v", events)
	}
	// Distinct item ids: the checker row never overwrote the floor.
	if floor.ItemID == checker.ItemID {
		t.Fatalf("floor and checker share item id %q (checker overwrote the floor)", floor.ItemID)
	}
	if checker.ItemID != "checker#jerryfane/gitmoot#7" {
		t.Fatalf("checker item id = %q, want checker#jerryfane/gitmoot#7", checker.ItemID)
	}

	// The checker item carries objective=true and the deterministic diff_size
	// dimension in its per-dimension metadata; the tool dims (LookPath-missed) are
	// absent.
	items, err := store.ListEvalReviewItems(ctx, "auto-trace:"+version.ID)
	if err != nil {
		t.Fatalf("ListEvalReviewItems returned error: %v", err)
	}
	var meta map[string]any
	for _, item := range items {
		if item.ItemID == checker.ItemID {
			if err := json.Unmarshal([]byte(item.MetadataJSON), &meta); err != nil {
				t.Fatalf("checker item metadata unmarshal: %v", err)
			}
		}
	}
	if meta == nil || meta["objective"] != true {
		t.Fatalf("checker item must be objective=true, got %v", meta)
	}
	dims, _ := meta["dimension_scores"].(map[string]any)
	if dims == nil || dims["diff_size"] != 1.0 {
		t.Fatalf("checker item must carry the deterministic diff_size=1.0 dimension, got %v", meta["dimension_scores"])
	}
	if _, present := dims["lint"]; present {
		t.Fatalf("a LookPath-missed tool dim must be absent, got %v", dims)
	}

	// The merge still happened (the checker never blocked the job).
	if len(gate.requests) != 1 {
		t.Fatalf("merge gate requests = %d, want 1 (the merge runs regardless of the checker)", len(gate.requests))
	}

	// Manual promotion preserved: only the single installed version still exists.
	versions, err := store.ListAgentTemplateVersions(ctx, version.TemplateID)
	if err != nil {
		t.Fatalf("ListAgentTemplateVersions returned error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("the checker must create NO new template version (manual promotion), got %d", len(versions))
	}
}

// TestDeterministicCheckerOffByDefault: with deterministic_checkers_enabled UNSET,
// daemonDeterministicCheckerDispatcher returns nil through the SAME gate, so the
// engine dispatches no checker leg and writes ONLY the verifiable floor row (the
// checker is byte-identically absent — the BYTE-IDENTICAL guard).
func TestDeterministicCheckerOffByDefault(t *testing.T) {
	ctx := context.Background()
	store := openTraceChainStore(t)
	version := installChainTemplate(t, store, "planner")
	gate := &stubMergeGate{decision: workflow.MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	engine := chainEngine(store, gate)
	engine.OutcomeHarvester = enabledHarvesterFor(t, store, harvestGitHubStub{status: realCIChainStatus()})

	// auto_trace on but deterministic_checkers OFF: the dispatcher must be nil.
	root := writeHarvestConfig(t, "[skillopt]\nauto_trace_enabled = true\n")
	if d := daemonDeterministicCheckerDispatcher(store, harvestGitHubStub{}, "", root); d != nil {
		t.Fatalf("off-by-default: checker dispatcher must be nil, got %T", d)
	}
	// Leave engine.DeterministicCheckerDispatcher nil (what the daemon would wire).

	seedCodexImplementJob(t, store, version)
	seedChainApprovingReview(t, store, "head123")

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob returned error: %v", err)
	}

	events := autoTraceFeedback(t, store, version.ID)
	if len(events) != 1 {
		t.Fatalf("off-by-default must write only the floor row, got %d: %+v", len(events), events)
	}
	if events[0].Reviewer != "gitmoot-auto" {
		t.Fatalf("off-by-default row reviewer = %q, want only the verifiable floor", events[0].Reviewer)
	}
}

// TestDaemonDeterministicCheckerDispatcherGate proves the admission gate: the
// dispatcher is non-nil ONLY when BOTH knobs are set, and nil otherwise (including a
// malformed config — fail-safe to disabled). Mirrors
// TestDaemonReviewLegDispatcherGate.
func TestDaemonDeterministicCheckerDispatcherGate(t *testing.T) {
	store := openHarvestStore(t)
	gh := harvestGitHubStub{}

	bothOff := writeHarvestConfig(t, "[skillopt]\n")
	if d := daemonDeterministicCheckerDispatcher(store, gh, "", bothOff); d != nil {
		t.Fatalf("both knobs off: want nil, got %T", d)
	}
	checkerOnly := writeHarvestConfig(t, "[skillopt]\ndeterministic_checkers_enabled = true\n")
	if d := daemonDeterministicCheckerDispatcher(store, gh, "", checkerOnly); d != nil {
		t.Fatal("deterministic_checkers without auto_trace must be nil (requires BOTH)")
	}
	malformed := writeHarvestConfig(t, "[skillopt]\nauto_trace_enabled = true\ndeterministic_checkers_enabled = perhaps\n")
	if d := daemonDeterministicCheckerDispatcher(store, gh, "", malformed); d != nil {
		t.Fatal("a malformed config must fail-safe to a nil (disabled) dispatcher")
	}
	bothOn := writeHarvestConfig(t, "[skillopt]\nauto_trace_enabled = true\ndeterministic_checkers_enabled = true\n")
	d := daemonDeterministicCheckerDispatcher(store, gh, "", bothOn)
	if d == nil {
		t.Fatal("both knobs on: want a non-nil checker dispatcher")
	}
	concrete, ok := d.(*deterministicCheckerDispatcher)
	if !ok {
		t.Fatalf("expected the real *deterministicCheckerDispatcher, got %T", d)
	}
	// The default selector (diff_size only) is wired when no list is configured.
	if len(concrete.checkers) != 1 || concrete.checkers[0] != "diff_size" {
		t.Fatalf("default checker selector = %v, want [diff_size]", concrete.checkers)
	}
}

// TestDeterministicCheckerCoexistsWithReview proves all THREE rows coexist when both
// the #469 review leg AND the #485 checker leg are wired: gitmoot-auto (floor),
// gitmoot-review (subjective), gitmoot-checker (objective), each under a distinct
// item id, with the merge unaffected.
func TestDeterministicCheckerCoexistsWithReview(t *testing.T) {
	ctx := context.Background()
	store := openTraceChainStore(t)
	version := installChainTemplate(t, store, "planner")
	gate := &stubMergeGate{decision: workflow.MergeDecision{Ready: true, Merged: true, MergeCommitSHA: "merge123"}}
	engine := chainEngine(store, gate)
	engine.OutcomeHarvester = enabledHarvesterFor(t, store, harvestGitHubStub{status: realCIChainStatus()})
	engine.ReviewLegDispatcher = reviewDispatcherForTest(store, harvestGitHubStub{})
	engine.DeterministicCheckerDispatcher = &deterministicCheckerDispatcher{
		store:    store,
		diff:     checkerDiffStub{files: smallDiffFiles()},
		runner:   &fakeCheckerRunner{present: map[string]bool{}},
		checkout: t.TempDir(),
		checkers: []string{checkerDiffSize},
	}

	seedCodexImplementJob(t, store, version)
	registerShellReviewer(t, store)
	seedChainApprovingReview(t, store, "head123")

	if err := engine.AdvanceJob(ctx, "review-job"); err != nil {
		t.Fatalf("AdvanceJob returned error: %v", err)
	}

	events := autoTraceFeedback(t, store, version.ID)
	reviewers := map[string]bool{}
	itemIDs := map[string]int{}
	for _, ev := range events {
		reviewers[ev.Reviewer] = true
		itemIDs[ev.ItemID]++
	}
	for _, want := range []string{"gitmoot-auto", "gitmoot-review", "gitmoot-checker"} {
		if !reviewers[want] {
			t.Fatalf("expected the %s row to coexist, got reviewers %v", want, reviewers)
		}
	}
	if len(events) != 3 {
		t.Fatalf("expected exactly 3 coexisting rows, got %d: %+v", len(events), events)
	}
	for id, n := range itemIDs {
		if n != 1 {
			t.Fatalf("item id %q has %d rows, want distinct item ids (1 each)", id, n)
		}
	}
	if len(gate.requests) != 1 {
		t.Fatalf("merge gate requests = %d, want 1", len(gate.requests))
	}
}

// compile-time assurance the integration stub matches the diff reader seam.
var _ diffFileLister = checkerDiffStub{}
var _ github.Client = harvestGitHubStub{}
