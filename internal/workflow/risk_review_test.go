package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func highRiskEvent() PullRequestEvent {
	return PullRequestEvent{
		Repo:              "jerryfane/gitmoot",
		Branch:            "task-7",
		PullRequest:       7,
		GoalID:            "goal-1",
		TaskID:            "task-7",
		TaskTitle:         "Auth change",
		LeadAgent:         "lead",
		Sender:            "lead",
		RequiredReviewers: []string{"audit", "sec"},
		ChangedPaths:      []string{"internal/auth/session.go"},
	}
}

// TestHighRiskPRFansOutLensReviewers pins the core acceptance behavior: an
// enabled + high-risk PR event fans out >= 2 lens review jobs (not the single
// native fan-out), seeds a review coordinator carrying synthesis_rule quorum, and
// records the resolved risk as an explainable job event.
func TestHighRiskPRFansOutLensReviewers(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	seedAgent(t, store, "audit", []string{"review"}, "jerryfane/gitmoot")
	seedAgent(t, store, "sec", []string{"review"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.RiskTiersEnabled = true

	if err := engine.HandlePullRequestOpened(ctx, highRiskEvent()); err != nil {
		t.Fatalf("HandlePullRequestOpened returned error: %v", err)
	}
	assertTaskState(t, store, "task-7", TaskReviewing)

	coordID := "review-coordinator/task-7/review-1"
	coord := mustJob(t, store, coordID)
	if coord.Type != "review_coordinator" {
		t.Fatalf("coordinator type = %q, want review_coordinator", coord.Type)
	}
	if got := countJobEvents(t, store, coordID, "risk_tier_resolved"); got != 1 {
		t.Fatalf("risk_tier_resolved events = %d, want 1", got)
	}

	lensIDs := []string{
		coordID + "/delegation/" + LensCorrectness,
		coordID + "/delegation/" + LensSecurity,
	}
	n := 0
	for _, id := range lensIDs {
		if !jobExists(t, store, id) {
			t.Fatalf("expected lens review job %q to be enqueued", id)
		}
		job := mustJob(t, store, id)
		if job.Type != "review" || job.State != string(JobQueued) {
			t.Fatalf("lens job %q = type %q state %q, want queued review", id, job.Type, job.State)
		}
		payload, err := unmarshalPayload(job.Payload)
		if err != nil {
			t.Fatalf("unmarshal lens payload: %v", err)
		}
		if payload.SynthesisRule != "quorum" {
			t.Fatalf("lens %q synthesis_rule = %q, want quorum", id, payload.SynthesisRule)
		}
		if payload.RiskTier != RiskTierHigh {
			t.Fatalf("lens %q risk_tier = %q, want high", id, payload.RiskTier)
		}
		n++
	}
	if n < 2 {
		t.Fatalf("fanned out %d lens reviewers, want >= 2", n)
	}
	// The single-review job the routine path would have created must NOT exist.
	if jobExists(t, store, "review-audit-task-7-review-1") {
		t.Fatal("high-risk path must NOT enqueue the single native review job")
	}
}

// TestHighRiskCriticalRefutationBlocks is the deterministic no-LLM E2E: it drives
// a high-risk PR through fan-out and then lands one lens with a critical
// refutation (a `blocked` decision, a non-approving quorum outcome). The quorum
// gate must fail and block the shared task — the "blocks on a critical refutation"
// acceptance behavior — with no coordinator continuation enqueued.
func TestHighRiskCriticalRefutationBlocks(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	seedAgent(t, store, "audit", []string{"review"}, "jerryfane/gitmoot")
	seedAgent(t, store, "sec", []string{"review"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.RiskTiersEnabled = true

	if err := engine.HandlePullRequestOpened(ctx, highRiskEvent()); err != nil {
		t.Fatalf("HandlePullRequestOpened returned error: %v", err)
	}
	coordID := "review-coordinator/task-7/review-1"
	correctnessID := coordID + "/delegation/" + LensCorrectness
	securityID := coordID + "/delegation/" + LensSecurity

	// Correctness lens approves (survives the lens).
	completeDelegationChild(t, store, correctnessID, JobSucceeded, AgentResult{
		Decision: "approved",
		Summary:  "no correctness defect",
	})
	if err := engine.AdvanceJob(ctx, correctnessID); err != nil {
		t.Fatalf("AdvanceJob(correctness) returned error: %v", err)
	}

	// Security lens reports a CRITICAL refutation via findings and blocks.
	critical, _ := json.Marshal(LensFinding{
		Lens: LensSecurity, Refuted: true, Severity: SeverityCritical, Confidence: 0.95, Evidence: "auth bypass at session.go:41",
	})
	blockDecision := SynthesizeLensDecision([]LensFinding{{Refuted: true, Severity: SeverityCritical}})
	completeDelegationChild(t, store, securityID, stateForDecision(blockDecision), AgentResult{
		Decision: blockDecision,
		Summary:  "critical auth bypass",
		Findings: []json.RawMessage{critical},
	})
	err := engine.AdvanceJob(ctx, securityID)

	var blocked BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("AdvanceJob(security) error = %v, want BlockedError (quorum failed)", err)
	}
	if !strings.Contains(blocked.Reason, "quorum") {
		t.Fatalf("block reason = %q, want quorum-failure reason", blocked.Reason)
	}
	assertTaskState(t, store, "task-7", TaskBlocked)
	if jobExists(t, store, delegationContinuationID(coordID)) {
		t.Fatal("a failed quorum must NOT enqueue a coordinator continuation")
	}
}

// TestHighRiskAllLensesApproveSatisfiesQuorum pins the other side of the
// synthesis mapping: when every lens approves, the quorum is satisfied and the
// coordinator continuation is enqueued (the task is not blocked).
func TestHighRiskAllLensesApproveSatisfiesQuorum(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement", "ask"}, "jerryfane/gitmoot")
	seedAgent(t, store, "audit", []string{"review"}, "jerryfane/gitmoot")
	seedAgent(t, store, "sec", []string{"review"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.RiskTiersEnabled = true

	if err := engine.HandlePullRequestOpened(ctx, highRiskEvent()); err != nil {
		t.Fatalf("HandlePullRequestOpened returned error: %v", err)
	}
	coordID := "review-coordinator/task-7/review-1"
	for _, lens := range []string{LensCorrectness, LensSecurity} {
		id := coordID + "/delegation/" + lens
		completeDelegationChild(t, store, id, JobSucceeded, AgentResult{Decision: "approved", Summary: "clean"})
		if err := engine.AdvanceJob(ctx, id); err != nil {
			t.Fatalf("AdvanceJob(%s) returned error: %v", lens, err)
		}
	}
	if !jobExists(t, store, delegationContinuationID(coordID)) {
		t.Fatal("a satisfied quorum must enqueue the coordinator continuation")
	}
	if task, _ := store.GetTask(ctx, "task-7"); task.State == string(TaskBlocked) {
		t.Fatal("a satisfied quorum must not block the task")
	}
}

// TestPullRequestSignalsSeamClassifiesInProcess pins the in-process trigger path:
// when an event carries no risk signals, the engine resolves them through the
// best-effort PullRequestSignals seam and classifies from those.
func TestPullRequestSignalsSeamClassifiesInProcess(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	seedAgent(t, store, "audit", []string{"review"}, "jerryfane/gitmoot")
	seedAgent(t, store, "sec", []string{"review"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.RiskTiersEnabled = true
	called := 0
	engine.PullRequestSignals = func(_ context.Context, repo string, number int) ([]string, []string, error) {
		called++
		if repo != "jerryfane/gitmoot" || number != 7 {
			t.Fatalf("seam called with repo=%q number=%d", repo, number)
		}
		return nil, []string{"internal/auth/session.go"}, nil
	}

	event := PullRequestEvent{
		Repo:              "jerryfane/gitmoot",
		Branch:            "task-7",
		PullRequest:       7,
		TaskID:            "task-7",
		TaskTitle:         "Auth change",
		LeadAgent:         "lead",
		Sender:            "lead",
		RequiredReviewers: []string{"audit", "sec"},
		// No Labels/ChangedPaths on the event -> the seam supplies them.
	}
	if err := engine.HandlePullRequestOpened(ctx, event); err != nil {
		t.Fatalf("HandlePullRequestOpened returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("PullRequestSignals called %d times, want 1", called)
	}
	if !jobExists(t, store, "review-coordinator/task-7/review-1") {
		t.Fatal("seam-resolved high-risk path must create the coordinator")
	}
}

func TestPullRequestSignalsSeamSkippedWhenEventCarriesSignals(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	seedAgent(t, store, "audit", []string{"review"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.RiskTiersEnabled = true
	engine.PullRequestSignals = func(context.Context, string, int) ([]string, []string, error) {
		t.Fatal("seam must not be consulted when the event already carries signals")
		return nil, nil, nil
	}
	event := PullRequestEvent{
		Repo:              "jerryfane/gitmoot",
		Branch:            "task-7",
		PullRequest:       7,
		TaskID:            "task-7",
		TaskTitle:         "Docs",
		LeadAgent:         "lead",
		Sender:            "lead",
		RequiredReviewers: []string{"audit"},
		ChangedPaths:      []string{"README.md"},
	}
	if err := engine.HandlePullRequestOpened(ctx, event); err != nil {
		t.Fatalf("HandlePullRequestOpened returned error: %v", err)
	}
	// Event signals are routine -> single review path, no coordinator.
	if jobExists(t, store, "review-coordinator/task-7/review-1") {
		t.Fatal("routine event signals must not create a coordinator")
	}
}

// TestRiskTiersOffIsByteIdentical is the invariant test: with risk tiers OFF, a
// PR whose changed paths WOULD classify high still takes the single native
// review fan-out, byte-for-byte identical to a control with no risk signals. No
// coordinator is created and the payloads match exactly.
func TestRiskTiersOffIsByteIdentical(t *testing.T) {
	run := func(t *testing.T, enabled bool, withSignals bool) string {
		store := openEngineStore(t)
		seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
		seedAgent(t, store, "audit", []string{"review"}, "jerryfane/gitmoot")
		engine := testEngine(store)
		engine.RiskTiersEnabled = enabled
		event := PullRequestEvent{
			Repo:              "jerryfane/gitmoot",
			Branch:            "task-7",
			PullRequest:       7,
			GoalID:            "goal-1",
			TaskID:            "task-7",
			TaskTitle:         "Workflow Engine",
			LeadAgent:         "lead",
			Sender:            "lead",
			RequiredReviewers: []string{"audit"},
		}
		if withSignals {
			event.ChangedPaths = []string{"internal/auth/session.go"}
			event.Labels = []string{"risk:high"}
		}
		if err := engine.HandlePullRequestOpened(context.Background(), event); err != nil {
			t.Fatalf("HandlePullRequestOpened returned error: %v", err)
		}
		// The single native review job must exist; no coordinator.
		if jobExists(t, store, "review-coordinator/task-7/review-1") {
			t.Fatal("risk-tiers-off path must not create a coordinator")
		}
		job := mustJob(t, store, "review-audit-task-7-review-1")
		return job.Payload
	}

	// Control: risk tiers off, no signals.
	control := run(t, false, false)
	// Off but signals present on the event (the daemon never populates them when
	// off, but assert defensively that the engine ignores them).
	offWithSignals := run(t, false, true)
	if control != offWithSignals {
		t.Fatalf("risk-tiers-off payload changed when signals were present:\n control=%s\n signals=%s", control, offWithSignals)
	}
}

// TestRiskTiersEnabledRoutineTakesSingleReviewPath pins that an ENABLED engine on
// a ROUTINE-classified PR still uses the unchanged single-review fan-out.
func TestRiskTiersEnabledRoutineTakesSingleReviewPath(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	seedAgent(t, store, "lead", []string{"implement"}, "jerryfane/gitmoot")
	seedAgent(t, store, "audit", []string{"review"}, "jerryfane/gitmoot")
	engine := testEngine(store)
	engine.RiskTiersEnabled = true

	err := engine.HandlePullRequestOpened(ctx, PullRequestEvent{
		Repo:              "jerryfane/gitmoot",
		Branch:            "task-7",
		PullRequest:       7,
		TaskID:            "task-7",
		TaskTitle:         "Docs tweak",
		LeadAgent:         "lead",
		Sender:            "lead",
		RequiredReviewers: []string{"audit"},
		ChangedPaths:      []string{"README.md", "docs/guide.md"},
	})
	if err != nil {
		t.Fatalf("HandlePullRequestOpened returned error: %v", err)
	}
	if jobExists(t, store, "review-coordinator/task-7/review-1") {
		t.Fatal("routine tier must not create a coordinator")
	}
	if !jobExists(t, store, "review-audit-task-7-review-1") {
		t.Fatal("routine tier must take the single native review path")
	}
}
