package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/config"
	"github.com/gitmoot/gitmoot/internal/db"
	"github.com/gitmoot/gitmoot/internal/events"
)

// pacePolicy is autoPromotePolicy with the #687 PACE gate turned on (RFC defaults:
// alpha 0.05 -> threshold 20, lambda 0.5). All the existing guardrails stay on, so
// PACE is a strictly ADDITIONAL gate.
func pacePolicy() config.SkillOptPolicy {
	policy := autoPromotePolicy(1, 0.9)
	policy.PaceEnabled = true
	policy.PaceAlpha = config.DefaultPaceAlpha
	policy.PaceLambda = config.DefaultPaceLambda
	policy.PaceMaxPairs = config.DefaultPaceMaxPairs
	return policy
}

// TestRunCandidateNotifyPaceOffByteIdentical proves pace_enabled=false is a no-op:
// with every other guardrail passing, the candidate promotes exactly as before even
// when the pairwise win/loss counts would NOT cross the PACE threshold (0/0 here).
// The e-process is never consulted when off.
func TestRunCandidateNotifyPaceOffByteIdentical(t *testing.T) {
	ctx := context.Background()
	store, version, candidate, _ := candidateNotifyFixture(t)
	autoPromoteCandidateScore(&candidate, 0.96)
	sink := &recordingSink{}

	// autoPromotePolicy has PaceEnabled=false; pass 0/0 pairwise counts.
	if err := runCandidateNotify(ctx, store, sink, autoPromotePolicy(1, 0.9), candidate, version, []db.FeedbackEvent{realCIFeedbackEvent()}, false, nil, 0, "", 0, 0); err != nil {
		t.Fatalf("runCandidateNotify returned error: %v", err)
	}
	if got := len(sink.byType(events.EventCandidateAutoPromoted)); got != 1 {
		t.Fatalf("auto_promoted emits = %d, want 1 (PACE off must not block)", got)
	}
	after, err := store.GetAgentTemplateVersionByID(ctx, version.ID)
	if err != nil {
		t.Fatalf("GetAgentTemplateVersionByID returned error: %v", err)
	}
	if after.State != "current" {
		t.Fatalf("version state = %q, want current", after.State)
	}
}

// TestRunCandidateNotifyPaceBlocksInsufficientEvidence proves that with PACE on and
// the recorded candidate-vs-champion pairwise outcomes NOT decisive (a few wins that
// never cross 1/alpha), a guardrails-pass candidate is NOT promoted: a pace_blocked
// notify fires and the version stays pending. This is the additional-gate behavior.
func TestRunCandidateNotifyPaceBlocksInsufficientEvidence(t *testing.T) {
	ctx := context.Background()
	store, version, candidate, _ := candidateNotifyFixture(t)
	autoPromoteCandidateScore(&candidate, 0.96)
	sink := &recordingSink{}

	// 3 wins, 0 losses: wealth 1.5^3 = 3.375 < 20 -> continue (not decisive).
	if err := runCandidateNotify(ctx, store, sink, pacePolicy(), candidate, version, []db.FeedbackEvent{realCIFeedbackEvent()}, false, nil, 0, "", 3, 0); err != nil {
		t.Fatalf("runCandidateNotify returned error: %v", err)
	}
	if got := len(sink.byType(events.EventCandidateAutoPromoted)); got != 0 {
		t.Fatalf("auto_promoted emits = %d, want 0 (PACE not decisive)", got)
	}
	// The awaiting-type stream carries a pace_blocked event with a PACE reason.
	var blocked bool
	for _, ev := range sink.byType(events.EventCandidateAwaitingPromotion) {
		if ev.Status == "pace_blocked" && strings.Contains(ev.Detail, "PACE") {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("expected a pace_blocked notify with a PACE reason, got %+v", sink.byType(events.EventCandidateAwaitingPromotion))
	}
	after, err := store.GetAgentTemplateVersionByID(ctx, version.ID)
	if err != nil {
		t.Fatalf("GetAgentTemplateVersionByID returned error: %v", err)
	}
	if after.State != "pending" {
		t.Fatalf("version state = %q, want pending (PACE blocked)", after.State)
	}
}

// TestRunCandidateNotifyPaceBudgetExhaustedRejects proves budget exhaustion is a
// fail-safe notify-only: a coin-flip tally that spends the whole pair budget without
// crossing rejects (pace_blocked), never promotes.
func TestRunCandidateNotifyPaceBudgetExhaustedRejects(t *testing.T) {
	ctx := context.Background()
	store, version, candidate, _ := candidateNotifyFixture(t)
	autoPromoteCandidateScore(&candidate, 0.96)
	sink := &recordingSink{}

	policy := pacePolicy()
	policy.PaceMaxPairs = 10 // small budget so 6 wins / 6 losses exhausts it
	if err := runCandidateNotify(ctx, store, sink, policy, candidate, version, []db.FeedbackEvent{realCIFeedbackEvent()}, false, nil, 0, "", 6, 6); err != nil {
		t.Fatalf("runCandidateNotify returned error: %v", err)
	}
	if got := len(sink.byType(events.EventCandidateAutoPromoted)); got != 0 {
		t.Fatalf("auto_promoted emits = %d, want 0 (budget exhausted)", got)
	}
	var rejected bool
	for _, ev := range sink.byType(events.EventCandidateAwaitingPromotion) {
		if ev.Status == "pace_blocked" && strings.Contains(ev.Detail, "reject") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatalf("expected a pace_blocked reject notify, got %+v", sink.byType(events.EventCandidateAwaitingPromotion))
	}
}

// TestRunCandidateNotifyPaceCommitsPromotes proves a clearly-better candidate (a
// dominant win/loss tally that crosses 1/alpha) passes the PACE gate and promotes,
// AND that the existing guardrails still had to pass (they do here).
func TestRunCandidateNotifyPaceCommitsPromotes(t *testing.T) {
	ctx := context.Background()
	store, version, candidate, _ := candidateNotifyFixture(t)
	autoPromoteCandidateScore(&candidate, 0.96)
	sink := &recordingSink{}

	// 10 wins, 1 loss: 1.5^10 * 0.5 = 28.8 >= 20 -> commit.
	if err := runCandidateNotify(ctx, store, sink, pacePolicy(), candidate, version, []db.FeedbackEvent{realCIFeedbackEvent()}, false, nil, 0, "", 10, 1); err != nil {
		t.Fatalf("runCandidateNotify returned error: %v", err)
	}
	if got := len(sink.byType(events.EventCandidateAutoPromoted)); got != 1 {
		t.Fatalf("auto_promoted emits = %d, want 1 (PACE commit)", got)
	}
	after, err := store.GetAgentTemplateVersionByID(ctx, version.ID)
	if err != nil {
		t.Fatalf("GetAgentTemplateVersionByID returned error: %v", err)
	}
	if after.State != "current" {
		t.Fatalf("version state = %q, want current (PACE committed)", after.State)
	}
}

// TestRunCandidateNotifyPaceIsAdditionalNotReplacement proves PACE does NOT rescue a
// candidate that fails a PRE-EXISTING guardrail: even a decisive PACE tally (10-1)
// cannot promote when require_external_ci fails, because PACE only runs AFTER the
// existing guardrails already passed.
func TestRunCandidateNotifyPaceIsAdditionalNotReplacement(t *testing.T) {
	ctx := context.Background()
	store, version, candidate, _ := candidateNotifyFixture(t)
	autoPromoteCandidateScore(&candidate, 0.96)
	sink := &recordingSink{}

	nearNeutral := db.FeedbackEvent{Choice: "a", Reasoning: "PR #7 merged through an empty gate (no external CI)."}
	if err := runCandidateNotify(ctx, store, sink, pacePolicy(), candidate, version, []db.FeedbackEvent{nearNeutral}, false, nil, 0, "", 10, 1); err != nil {
		t.Fatalf("runCandidateNotify returned error: %v", err)
	}
	if got := len(sink.byType(events.EventCandidateAutoPromoted)); got != 0 {
		t.Fatalf("auto_promoted emits = %d, want 0 (existing guardrail must still block)", got)
	}
	// No pace_blocked either — the existing guardrail short-circuits before PACE.
	for _, ev := range sink.byType(events.EventCandidateAwaitingPromotion) {
		if ev.Status == "pace_blocked" {
			t.Fatalf("PACE ran despite a failing prior guardrail; PACE must be downstream of Promote=true")
		}
	}
}
