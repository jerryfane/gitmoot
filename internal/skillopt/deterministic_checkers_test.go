package skillopt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gitmoot/gitmoot/internal/db"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

// checkerOutcome builds an OBJECTIVE deterministic-checker outcome
// (Kind=OutcomeReviewed, Objective=true) for the test PR with the given tool
// dimensions.
func checkerOutcome(rubric map[string]float64) workflow.Outcome {
	return workflow.Outcome{
		Kind:        workflow.OutcomeReviewed,
		Objective:   true,
		Repo:        "owner/repo",
		PullRequest: 7,
		HeadSHA:     "deadbeef",
		Rubric:      rubric,
		Findings:    "deterministic checkers on PR #7: diff_size",
	}
}

func checkerItemFor(t *testing.T, store *db.Store, versionID, itemID string) (db.EvalReviewItem, bool) {
	t.Helper()
	items, err := store.ListEvalReviewItems(context.Background(), autoTraceRunIDPrefix+versionID)
	if err != nil {
		t.Fatalf("ListEvalReviewItems returned error: %v", err)
	}
	for _, item := range items {
		if item.ItemID == itemID {
			return item, true
		}
	}
	return db.EvalReviewItem{}, false
}

// TestProjectCheckerMeanOfDimensionScores: the tool dimensions project to the
// arithmetic MEAN of their DimensionScores (the #462 mean path), HasScore=true.
func TestProjectCheckerMeanOfDimensionScores(t *testing.T) {
	signal := projectChecker(checkerOutcome(map[string]float64{
		"diff_size": 1.0, "lint": 0.8, "complexity": 0.6,
	}))
	if !signal.HasScore {
		t.Fatal("non-empty tool dimensions must yield HasScore=true")
	}
	want := (1.0 + 0.8 + 0.6) / 3.0
	if diff := signal.Score - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("checker score = %v, want the mean %v", signal.Score, want)
	}
}

// TestProjectCheckerEmptyRubricNoScore: an EMPTY rubric (every checker skipped)
// yields HasScore=false (no fabricated neutral 0.5).
func TestProjectCheckerEmptyRubricNoScore(t *testing.T) {
	signal := projectChecker(checkerOutcome(map[string]float64{}))
	if signal.HasScore {
		t.Fatalf("empty rubric must yield HasScore=false, got score=%v", signal.Score)
	}
}

// TestCheckerRowCoexistsWithFloorAndReview: harvesting the verifiable floor, the
// subjective review, AND the objective checker writes THREE distinct rows in the
// SAME auto-trace run under distinct item ids + reviewers — the checker row NEVER
// overwrites the floor or the review.
func TestCheckerRowCoexistsWithFloorAndReview(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, &stubStatusReader{status: realCIStatus()})

	// Verifiable floor (merge with real CI).
	if err := h.Harvest(ctx, implementJob(), payload, workflow.Outcome{
		Kind: workflow.OutcomeMerged, Repo: "owner/repo", PullRequest: 7, HeadSHA: "deadbeef",
	}); err != nil {
		t.Fatalf("Harvest merge returned error: %v", err)
	}
	// Subjective cross-family review.
	if err := h.Harvest(ctx, implementJob(), payload, reviewOutcome(map[string]float64{"coverage": 0.5}, "claude", false)); err != nil {
		t.Fatalf("Harvest review returned error: %v", err)
	}
	// Objective deterministic checker.
	if err := h.Harvest(ctx, implementJob(), payload, checkerOutcome(map[string]float64{"diff_size": 0.9})); err != nil {
		t.Fatalf("Harvest checker returned error: %v", err)
	}

	events := feedbackForVersion(t, store, version.ID)
	if len(events) != 3 {
		t.Fatalf("expected the floor + review + checker rows (3), got %d: %+v", len(events), events)
	}
	var floor, review, checker *db.FeedbackEvent
	for i := range events {
		switch events[i].Reviewer {
		case autoTraceReviewer:
			floor = &events[i]
		case reviewReviewerID:
			review = &events[i]
		case checkerReviewerID:
			checker = &events[i]
		}
	}
	if floor == nil || review == nil || checker == nil {
		t.Fatalf("all three rows (floor/review/checker) must coexist; got %+v", events)
	}
	if checker.ItemID != checkerItemIDPrefix+"owner/repo#7" {
		t.Fatalf("checker item id = %q, want checker#owner/repo#7", checker.ItemID)
	}
	if checker.ItemID == floor.ItemID || checker.ItemID == review.ItemID {
		t.Fatalf("checker must use a DISTINCT item id; checker=%q floor=%q review=%q", checker.ItemID, floor.ItemID, review.ItemID)
	}
	if checker.Reviewer != checkerReviewerID {
		t.Fatalf("checker reviewer = %q, want %q", checker.Reviewer, checkerReviewerID)
	}
}

// TestCheckerRowObjectiveTaggedAndPersistsDimensions: the checker row carries the
// objective tag, the projected mean (checker_score), and the per-dimension tool
// scores on its eval item; the run keeps feedback_source=automatic_trace and no
// contract field/version changed.
func TestCheckerRowObjectiveTaggedAndPersistsDimensions(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, nil)

	if err := h.Harvest(ctx, implementJob(), payload, checkerOutcome(map[string]float64{"diff_size": 0.8, "lint": 0.6})); err != nil {
		t.Fatalf("Harvest checker returned error: %v", err)
	}

	events := feedbackForVersion(t, store, version.ID)
	if len(events) != 1 || events[0].Reviewer != checkerReviewerID {
		t.Fatalf("expected one gitmoot-checker row, got %+v", events)
	}
	if events[0].Source != autoTraceSource {
		t.Fatalf("checker row source = %q, want auto-trace (rides the automatic_trace run)", events[0].Source)
	}

	item, ok := checkerItemFor(t, store, version.ID, checkerItemIDPrefix+"owner/repo#7")
	if !ok {
		t.Fatal("checker eval_review_item missing")
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(item.MetadataJSON), &meta); err != nil {
		t.Fatalf("checker item metadata did not unmarshal: %v", err)
	}
	if meta[checkerItemMetadataObjectiveKey] != true {
		t.Fatalf("checker item must be objective=true, got %v", meta[checkerItemMetadataObjectiveKey])
	}
	if _, present := meta[reviewItemMetadataJudgeKey]; present {
		t.Fatal("the OBJECTIVE checker row must NOT carry the subjective judge_derived tag")
	}
	// The projected mean is persisted.
	score, ok := meta[checkerItemMetadataScoreKey].(float64)
	if !ok {
		t.Fatalf("checker item must carry the projected checker_score, got %v", meta[checkerItemMetadataScoreKey])
	}
	if want := (0.8 + 0.6) / 2.0; score-want > 1e-9 || score-want < -1e-9 {
		t.Fatalf("checker_score = %v, want the mean %v", score, want)
	}
	// The per-dimension tool scores are persisted.
	dims, ok := meta[checkerItemMetadataDimensionsKey].(map[string]any)
	if !ok {
		t.Fatalf("checker item must persist per-dimension scores, got %v", meta[checkerItemMetadataDimensionsKey])
	}
	if dims["diff_size"] != 0.8 || dims["lint"] != 0.6 {
		t.Fatalf("dimension_scores = %v, want diff_size=0.8 lint=0.6", dims)
	}

	// The run still stamps feedback_source=automatic_trace (no contract bump).
	run, err := store.GetEvalRun(ctx, autoTraceRunIDPrefix+version.ID)
	if err != nil {
		t.Fatalf("GetEvalRun returned error: %v", err)
	}
	pkg, err := ExportTrainingPackage(ctx, store, run.ID)
	if err != nil {
		t.Fatalf("ExportTrainingPackage returned error: %v", err)
	}
	if pkg.ContractVersion != ContractVersion || ContractVersion != 1 {
		t.Fatalf("ContractVersion = %d (const %d), want 1 — additive, no bump", pkg.ContractVersion, ContractVersion)
	}
}

// TestCheckerChoiceTracksQualityLevel: the checker's a/b choice is DERIVED from the
// projected mean — a low-mean (poor) objective signal registers "b", a high-mean
// (good) one registers "a".
func TestCheckerChoiceTracksQualityLevel(t *testing.T) {
	ctx := context.Background()

	storeLow := newTraceStore(t)
	versionLow, payloadLow := installTraceTemplate(t, storeLow, "planner")
	hLow := NewOutcomeHarvester(storeLow, nil)
	if err := hLow.Harvest(ctx, implementJob(), payloadLow, checkerOutcome(map[string]float64{"diff_size": 0.2, "lint": 0.3})); err != nil {
		t.Fatalf("Harvest low checker returned error: %v", err)
	}
	low := feedbackForVersion(t, storeLow, versionLow.ID)
	if len(low) != 1 || low[0].Choice != "b" {
		t.Fatalf("a low-mean checker must write Choice=b, got %+v", low)
	}

	storeHigh := newTraceStore(t)
	versionHigh, payloadHigh := installTraceTemplate(t, storeHigh, "planner")
	hHigh := NewOutcomeHarvester(storeHigh, nil)
	if err := hHigh.Harvest(ctx, implementJob(), payloadHigh, checkerOutcome(map[string]float64{"diff_size": 1.0, "lint": 0.9})); err != nil {
		t.Fatalf("Harvest high checker returned error: %v", err)
	}
	high := feedbackForVersion(t, storeHigh, versionHigh.ID)
	if len(high) != 1 || high[0].Choice != "a" {
		t.Fatalf("a high-mean checker must write Choice=a, got %+v", high)
	}
}

// TestCheckerEmptyRubricWritesNoRow: an EMPTY rubric (HasScore=false, every checker
// skipped) is non-informative, so the harvester writes NO checker row at all.
func TestCheckerEmptyRubricWritesNoRow(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, nil)

	if err := h.Harvest(ctx, implementJob(), payload, checkerOutcome(map[string]float64{})); err != nil {
		t.Fatalf("Harvest empty-rubric checker returned error: %v", err)
	}
	if events := feedbackForVersion(t, store, version.ID); len(events) != 0 {
		t.Fatalf("an empty (HasScore=false) checker rubric must write no row, got %+v", events)
	}
}

// TestCheckerRowReUpsertsInPlace: re-evaluating the SAME PR re-upserts the SAME
// checker row (stable row count) — a corrective overwrite, not a duplicate.
func TestCheckerRowReUpsertsInPlace(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, nil)

	for i := 0; i < 3; i++ {
		if err := h.Harvest(ctx, implementJob(), payload, checkerOutcome(map[string]float64{"diff_size": 0.7})); err != nil {
			t.Fatalf("Harvest checker iteration %d returned error: %v", i, err)
		}
	}
	events := feedbackForVersion(t, store, version.ID)
	if len(events) != 1 {
		t.Fatalf("re-evaluation must re-upsert in place (1 row), got %d: %+v", len(events), events)
	}
}

// TestHarvestCheckerOutOfScopeJobSkips: an objective checker outcome against a
// non-implement (out-of-scope) job writes no checker row (the inScope gate applies).
func TestHarvestCheckerOutOfScopeJobSkips(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, nil)

	reviewJob := db.Job{ID: "job-review-1", Type: "review"}
	if err := h.Harvest(ctx, reviewJob, payload, checkerOutcome(map[string]float64{"diff_size": 0.5})); err != nil {
		t.Fatalf("Harvest returned error: %v", err)
	}
	if events := feedbackForVersion(t, store, version.ID); len(events) != 0 {
		t.Fatalf("out-of-scope checker must write nothing, got %+v", events)
	}
}
