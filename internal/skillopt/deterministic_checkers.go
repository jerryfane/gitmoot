package skillopt

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// checkerReviewerID is the FIXED, tool-derived reviewer id every OBJECTIVE
// deterministic-checker row carries (#485). It is a single non-human sentinel (like
// autoTraceReviewer and reviewReviewerID) so the objective row is never mistaken
// for a human ranking, and it is DISTINCT from BOTH autoTraceReviewer (gitmoot-auto,
// the verifiable floor) AND reviewReviewerID (gitmoot-review, the subjective LLM
// rubric) so the objective row coexists with both under the run's UNIQUE
// (run_id,item_id,reviewer,source,source_url) key instead of overwriting either.
//
// The objective row is a THIRD coexisting signal: it is OBJECTIVE and un-gameable
// (plain tool measurements, no LLM), so the export/optimizer can weight it
// distinctly from the subjective judge row via this reviewer sentinel + the
// objective item tag. A re-evaluation of the SAME PR re-upserts the SAME checker
// row in place (the reviewer id is constant), so the row count stays stable.
const checkerReviewerID = "gitmoot-checker"

// checkerItemIDPrefix namespaces the objective checker eval item so it is a DISTINCT
// item id from BOTH the verifiable-floor item (repo#pr) AND the subjective review
// item (review#repo#pr): the checker item is checker#<repo>#<pr>. A distinct item id
// means the objective row never overwrites either sibling even though all three live
// in the same auto-trace:<versionID> run.
const checkerItemIDPrefix = "checker#"

const (
	// checkerItemMetadataObjectiveKey marks a checker row OBJECTIVE (tool-measured,
	// non-LLM) on its eval item so the export/optimizer can recognize and weight it
	// distinctly from the subjective judge_derived review row, WITHOUT a new contract
	// field. The run-level feedback_source stays automatic_trace (no contract bump).
	checkerItemMetadataObjectiveKey = "objective"
	// checkerItemMetadataScoreKey carries the projected dimension MEAN ([0,1]) on the
	// checker item so the magnitude the tools measured is recoverable by the
	// export/optimizer (the same no-new-field lever the review row uses).
	checkerItemMetadataScoreKey = "checker_score"
	// checkerItemMetadataDimensionsKey persists the per-dimension tool scores (the
	// surviving Rubric map) so each tool's individual magnitude is recoverable, not
	// just the aggregate mean — keeping the objective row fully auditable.
	checkerItemMetadataDimensionsKey = "dimension_scores"
)

// checkerChoiceThreshold is the projected-mean cutoff mapping an objective quality
// LEVEL onto the a/b choice the auto-trace pipeline reads (recommendValidate counts
// "a" => baselineWins, "b" => candidateWins), mirroring the review row's convention:
// an objective signal whose mean dimension score is below this cutoff is a NEGATIVE
// quality signal and registers as "b"; at or above it is positive-leaning ("a").
const checkerChoiceThreshold = 0.5

// checkerChoice derives the a/b choice from the projected objective quality so a
// poor objective signal registers as a non-baseline vote instead of silently
// inverting into a baseline win, mirroring reviewChoice. It is only consulted for a
// scored signal (the caller skips the write on HasScore=false).
func checkerChoice(signal NormalizedSignal) string {
	if signal.HasScore && signal.Score < checkerChoiceThreshold {
		return "b"
	}
	return "a"
}

// projectChecker maps the deterministic-checker tool dimensions to a
// NormalizedSignal via ProjectSignal over a synthetic EvaluatorScore whose
// DimensionScores ARE the tool-derived Rubric (#485), exactly like projectReview.
// ProjectSignal takes the arithmetic MEAN of DimensionScores when Soft is absent
// (the #462 rubric-as-score path), so no new aggregation is invented. An EMPTY
// rubric (every checker skipped) yields HasScore=false (no fabricated neutral 0.5),
// so writeCheckerFeedback skips the write entirely.
func projectChecker(outcome workflow.Outcome) NormalizedSignal {
	dims := map[string]float64{}
	for k, v := range outcome.Rubric {
		dims[k] = v
	}
	findings := strings.TrimSpace(outcome.Findings)
	if findings == "" {
		findings = fmt.Sprintf("Deterministic checkers on PR #%d.", outcome.PullRequest)
	}
	return ProjectSignal(&EvaluatorScore{DimensionScores: dims}, &RankedFeedbackEvent{Reasoning: findings}, nil)
}

// writeCheckerFeedback upserts the OBJECTIVE deterministic-checker row into the
// EXISTING auto-trace:<versionID> run (#485): the same run as the verifiable floor
// and the subjective review (so the optimizer sees all three), under a DISTINCT item
// id (checker#repo#pr) and the FIXED tool-derived reviewer sentinel
// (gitmoot-checker), so it coexists with both siblings on the UNIQUE
// (run_id,item_id,reviewer,source,source_url) key instead of overwriting either —
// and so a re-evaluation of the same PR overwrites the checker row in place rather
// than accumulating a stale duplicate. The run keeps feedback_source=automatic_trace
// (no contract bump, ContractVersion=1 unchanged); the checker item carries the
// objective tag, the projected mean (checker_score), and the per-dimension tool
// scores so the export/optimizer can weight it distinctly. The a/b choice tracks the
// projected mean (poor objective signal => "b").
//
// It writes ONLY eval_runs/eval_review_items/feedback_events — no candidate, no
// promotion path — so promotion stays manual.
func (h *OutcomeHarvester) writeCheckerFeedback(ctx context.Context, version db.AgentTemplateVersion, outcome workflow.Outcome, signal NormalizedSignal) error {
	// An empty rubric (HasScore=false) carries NO quality level: every checker
	// skipped (no tool, no checkout, error, timeout) means there is nothing to
	// record. Skip it entirely — matching the HasScore=false intent — so only a
	// scored objective signal ever lands a feedback row.
	if !signal.HasScore {
		return nil
	}
	runID := AutoTraceRunID(version.ID)
	itemID := checkerItemID(outcome)
	sourceURL := pullRequestURL(outcome.Repo, outcome.PullRequest)

	// Reuse the EXISTING auto-trace run (same metadata: feedback_source=automatic_trace,
	// validate mode); an upsert keyed by the run id is idempotent so the verifiable
	// floor's and the review's run metadata are preserved verbatim.
	if err := h.Store.UpsertEvalRun(ctx, db.EvalRun{
		ID:                runID,
		TemplateID:        strings.TrimSpace(version.TemplateID),
		TemplateVersionID: strings.TrimSpace(version.ID),
		TargetRepo:        strings.TrimSpace(outcome.Repo),
		State:             "ready",
		Mode:              db.EvalRunModeValidate,
		MetadataJSON:      autoTraceRunMetadata(),
	}); err != nil {
		return fmt.Errorf("upsert auto-trace eval_run (checker): %w", err)
	}

	if err := h.Store.UpsertEvalReviewItem(ctx, db.EvalReviewItem{
		RunID:        runID,
		ItemID:       itemID,
		Title:        checkerItemTitle(outcome),
		MetadataJSON: checkerItemMetadata(outcome, signal),
	}); err != nil {
		return fmt.Errorf("upsert auto-trace eval_review_item (checker): %w", err)
	}

	if err := h.Store.UpsertFeedbackEvent(ctx, db.FeedbackEvent{
		RunID:     runID,
		ItemID:    itemID,
		Choice:    checkerChoice(signal),
		Reasoning: signal.Feedback,
		Reviewer:  checkerReviewerID,
		Source:    autoTraceSource,
		SourceURL: sourceURL,
	}); err != nil {
		return fmt.Errorf("upsert auto-trace feedback_event (checker): %w", err)
	}
	return nil
}

// checkerItemID is the DISTINCT eval item id for the objective checker row
// (checker#repo#pr) so it never overwrites the verifiable-floor row (repo#pr) or the
// subjective review row (review#repo#pr) in the same run, while a re-evaluation of
// the SAME PR re-upserts the SAME checker row in place (stable row count).
func checkerItemID(outcome workflow.Outcome) string {
	return checkerItemIDPrefix + autoTraceItemID(outcome)
}

// checkerItemTitle is the human-readable title of the objective checker item.
func checkerItemTitle(outcome workflow.Outcome) string {
	repo := strings.TrimSpace(outcome.Repo)
	if outcome.PullRequest > 0 {
		return fmt.Sprintf("Deterministic checkers: %s PR #%d", repo, outcome.PullRequest)
	}
	return "Deterministic checkers: " + repo
}

// checkerItemMetadata carries the objective tag + the per-dimension tool scores on
// the checker eval item so the export/optimizer can recognize it as objective
// (un-gameable, non-LLM) and recover each tool's magnitude, WITHOUT a new contract
// field. It persists the projected MEAN (checker_score) so the aggregate magnitude
// reaches the row, and the surviving Rubric map (dimension_scores) so each
// individual tool dimension is auditable — the same no-new-field lever the review
// row uses. The dimension names are also listed (checkers) for quick filtering.
func checkerItemMetadata(outcome workflow.Outcome, signal NormalizedSignal) string {
	dims := map[string]float64{}
	names := make([]string, 0, len(outcome.Rubric))
	for k, v := range outcome.Rubric {
		dims[k] = v
		names = append(names, k)
	}
	sort.Strings(names)
	meta := map[string]any{
		"repo":                           strings.TrimSpace(outcome.Repo),
		"pull_request":                   outcome.PullRequest,
		checkerItemMetadataObjectiveKey:  true,
		checkerItemMetadataDimensionsKey: dims,
		"checkers":                       names,
	}
	if signal.HasScore {
		meta[checkerItemMetadataScoreKey] = signal.Score
	}
	raw, _ := json.Marshal(meta)
	return string(raw)
}
