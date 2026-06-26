package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/events"
	"github.com/jerryfane/gitmoot/internal/skillopt"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// notifyAndMaybeAutoPromoteCandidate is the SINGLE shared post-import step (#471)
// that BOTH CLI callers (runSkillOptImport for manual `skillopt import` and
// importSkillOptTrainCandidate for `train continue`) invoke AFTER
// ImportCandidatePackageWithOptions returns the just-committed PENDING version.
//
// It preserves the "importing never promotes" invariant: the import write itself
// stays side-effect-pure (no emit, no promote); the notify + auto-promote is this
// separate, config-gated step layered on top. It:
//
//  1. Builds the best-effort event sink the SAME way the daemon does
//     (buildDaemonEventSink, nil when [events] is OFF), so emission is nil-safe and
//     byte-identical when the event stream is unset.
//  2. Loads the candidate's eval_run feedback events (when an evalRunID is
//     resolvable; unresolvable -> empty -> the sample-count guardrail fails safe).
//  3. Emits candidate.awaiting_promotion EXACTLY ONCE (independent of the promote
//     policy), carrying the version id (JobID), template id (RootID), and a
//     score/samples/CI reason in Detail.
//  4. Evaluates the pure skillopt.EvaluateAutoPromote guardrails; on a pass it calls
//     the EXISTING store.PromoteAgentTemplateVersion and emits candidate.auto_promoted.
//
// It is best-effort and NEVER fails the import: a sink build error, a feedback
// read error, or an emit are all swallowed (the candidate is already durably
// pending). A promote error IS surfaced (it is a real, requested mutation that
// failed), but only AFTER the awaiting_promotion notify already fired.
func notifyAndMaybeAutoPromoteCandidate(ctx context.Context, store *db.Store, home string, candidate skillopt.CandidatePackage, version db.AgentTemplateVersion, evalRunID string) error {
	policy, perr := loadSkillOptPolicy(home)
	if perr != nil {
		// Fail-safe: a malformed [skillopt] config never auto-promotes; treat as the
		// disabled default so we still notify (with the default policy off).
		policy = config.DefaultSkillOptPolicy()
	}

	// Resolve the candidate's eval_run feedback events. A read error or an empty/
	// unresolvable run id degrades to no samples, which the min_samples guardrail
	// treats as a hard do-not-promote (notify only) — a sparse import never promotes.
	var feedbackEvents []db.FeedbackEvent
	if runID := strings.TrimSpace(evalRunID); runID != "" && store != nil {
		if list, err := store.ListFeedbackEvents(ctx, runID); err == nil {
			feedbackEvents = list
		}
	}

	// Build the sink the SAME way the daemon does (nil when [events] is OFF), so the
	// emit path is nil-safe and byte-identical when the event stream is unset.
	sink := buildDaemonEventSink(store, home)
	return runCandidateNotify(ctx, store, sink, policy, candidate, version, feedbackEvents)
}

// runCandidateNotify is the testable core of the post-import notify+auto-promote
// step: given a resolved sink, the [skillopt] policy, the candidate, the pending
// version, and the eval_run feedback events, it (3) emits candidate.awaiting_promotion
// EXACTLY ONCE and (4) on a guardrails pass calls the existing
// PromoteAgentTemplateVersion then emits candidate.auto_promoted. Splitting it from
// the home/sink/feedback resolution lets a recording sink assert the exactly-once
// emit and the no-double-emit invariant deterministically.
func runCandidateNotify(ctx context.Context, store *db.Store, sink events.Sink, policy config.SkillOptPolicy, candidate skillopt.CandidatePackage, version db.AgentTemplateVersion, feedbackEvents []db.FeedbackEvent) error {
	decision := skillopt.EvaluateAutoPromote(policy, candidate, feedbackEvents)

	// (3) Always-on notify (when [events] is configured), independent of the
	// promotion policy: emit candidate.awaiting_promotion exactly once.
	awaitingDetail := candidateAwaitingDetail(version, candidate, len(feedbackEvents), decision)
	emitCandidateEvent(ctx, sink, events.EventCandidateAwaitingPromotion, version, "awaiting_promotion", awaitingDetail)

	// (4) Auto-promote only on a guardrails pass; on a fail the awaiting_promotion
	// notify above is the only emit.
	if !decision.Promote {
		return nil
	}
	if store == nil {
		return nil
	}
	promoted, err := store.PromoteAgentTemplateVersion(ctx, version.ID)
	if err != nil {
		return fmt.Errorf("auto-promote candidate %s: %w", version.ID, err)
	}
	emitCandidateEvent(ctx, sink, events.EventCandidateAutoPromoted, promoted, "auto_promoted", decision.Reason)
	return nil
}

// emitCandidateEvent maps a candidate version onto the #446 Event contract and
// emits it best-effort (nil-safe). JobID = version id, RootID = template id, Repo
// is empty (templates are not repo-scoped), and the detail is redacted/path-
// scrubbed by NewEvent via workflow.RedactCommentText so no absolute path or
// secret leaves the box.
func emitCandidateEvent(ctx context.Context, sink events.Sink, eventType events.EventType, version db.AgentTemplateVersion, status, detail string) {
	events.EmitEvent(ctx, sink, events.NewEvent(
		eventType,
		version.ID,
		version.TemplateID,
		"",
		status,
		detail,
		time.Time{},
		workflow.RedactCommentText,
	))
}

// candidateAwaitingDetail builds the human-facing reason for the
// candidate.awaiting_promotion event: the candidate's score (when present), the
// eval_run sample count, and — on an auto_promote-on run — the guardrail decision
// reason so a reader sees WHY it will or will not auto-promote.
func candidateAwaitingDetail(version db.AgentTemplateVersion, candidate skillopt.CandidatePackage, samples int, decision skillopt.AutoPromoteDecision) string {
	score := "n/a"
	if candidate.Summary.Score != nil {
		score = fmt.Sprintf("%.4g", *candidate.Summary.Score)
	}
	detail := fmt.Sprintf("candidate %s for template %s awaiting promotion (score %s, %d samples)", version.ID, version.TemplateID, score, samples)
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		detail += "; " + reason
	}
	return detail
}
