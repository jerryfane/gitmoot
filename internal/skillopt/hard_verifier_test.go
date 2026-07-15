package skillopt

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gitmoot/gitmoot/internal/db"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

// hardOutcome builds a HARD-verifier outcome (Kind=OutcomeReviewed, HardVerifier=true)
// for the test PR with the given verdict and per-command results.
func hardOutcome(passed bool, results map[string]float64) workflow.Outcome {
	verdict := "FAIL"
	if passed {
		verdict = "pass"
	}
	return workflow.Outcome{
		Kind:         workflow.OutcomeReviewed,
		HardVerifier: true,
		HardPassed:   passed,
		Repo:         "owner/repo",
		PullRequest:  7,
		HeadSHA:      "deadbeef",
		Rubric:       results,
		Findings:     "Hard verifiers on PR #7 [" + verdict + "]",
	}
}

func hardItemFor(t *testing.T, store *db.Store, versionID, itemID string) (db.EvalReviewItem, bool) {
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

// TestProjectHardVerifierPassIsStrongPositive: a PASS projects to the authoritative
// Hard=1.0 (Score=1.0, HasScore=true) — an evidence-backed positive.
func TestProjectHardVerifierPassIsStrongPositive(t *testing.T) {
	signal := projectHardVerifier(hardOutcome(true, map[string]float64{"go test ./...": 1.0}))
	if !signal.HasScore || signal.Score != 1.0 {
		t.Fatalf("hard PASS projection = score=%v has=%v, want score=1.0 has=true", signal.Score, signal.HasScore)
	}
	if hardVerifierChoice(signal) != "a" {
		t.Fatalf("hard PASS choice = %q, want a", hardVerifierChoice(signal))
	}
}

// TestProjectHardVerifierFailIsAuthoritativeZero: a FAIL projects to the
// authoritative gate-fail Hard=0 (Score=0, HasScore=true) — NOT absent — and the
// choice flips to "b" (a non-baseline vote), with the failure reasoning carried.
func TestProjectHardVerifierFailIsAuthoritativeZero(t *testing.T) {
	signal := projectHardVerifier(hardOutcome(false, map[string]float64{"go test ./...": 0.0}))
	if !signal.HasScore || signal.Score != 0 {
		t.Fatalf("hard FAIL projection = score=%v has=%v, want score=0 has=true (authoritative gate-fail)", signal.Score, signal.HasScore)
	}
	if hardVerifierChoice(signal) != "b" {
		t.Fatalf("hard FAIL choice = %q, want b (non-baseline vote)", hardVerifierChoice(signal))
	}
	if signal.Feedback == "" {
		t.Fatal("hard FAIL must carry failure reasoning")
	}
}

// TestHardVerifierRowCoexistsWithAllSiblings: harvesting the verifiable floor, the
// subjective review, the objective checker, AND the hard verifier writes FOUR
// distinct rows in the SAME auto-trace run under distinct item ids + reviewers — the
// hard row NEVER overwrites any sibling.
func TestHardVerifierRowCoexistsWithAllSiblings(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, &stubStatusReader{status: realCIStatus()})

	if err := h.Harvest(ctx, implementJob(), payload, workflow.Outcome{
		Kind: workflow.OutcomeMerged, Repo: "owner/repo", PullRequest: 7, HeadSHA: "deadbeef",
	}); err != nil {
		t.Fatalf("Harvest merge returned error: %v", err)
	}
	if err := h.Harvest(ctx, implementJob(), payload, reviewOutcome(map[string]float64{"coverage": 0.5}, "claude", false)); err != nil {
		t.Fatalf("Harvest review returned error: %v", err)
	}
	if err := h.Harvest(ctx, implementJob(), payload, checkerOutcome(map[string]float64{"diff_size": 0.9})); err != nil {
		t.Fatalf("Harvest checker returned error: %v", err)
	}
	if err := h.Harvest(ctx, implementJob(), payload, hardOutcome(true, map[string]float64{"go test ./...": 1.0})); err != nil {
		t.Fatalf("Harvest hard verifier returned error: %v", err)
	}

	events := feedbackForVersion(t, store, version.ID)
	if len(events) != 4 {
		t.Fatalf("expected floor + review + checker + hard rows (4), got %d: %+v", len(events), events)
	}
	var floor, review, checker, hard *db.FeedbackEvent
	for i := range events {
		switch events[i].Reviewer {
		case autoTraceReviewer:
			floor = &events[i]
		case reviewReviewerID:
			review = &events[i]
		case checkerReviewerID:
			checker = &events[i]
		case hardVerifierReviewerID:
			hard = &events[i]
		}
	}
	if floor == nil || review == nil || checker == nil || hard == nil {
		t.Fatalf("all four rows must coexist; got %+v", events)
	}
	if hard.ItemID != hardVerifierItemIDPrefix+"owner/repo#7" {
		t.Fatalf("hard item id = %q, want hard#owner/repo#7", hard.ItemID)
	}
	if hard.ItemID == floor.ItemID || hard.ItemID == review.ItemID || hard.ItemID == checker.ItemID {
		t.Fatalf("hard row must use a DISTINCT item id; hard=%q floor=%q review=%q checker=%q", hard.ItemID, floor.ItemID, review.ItemID, checker.ItemID)
	}
	if hard.Choice != "a" {
		t.Fatalf("hard PASS row choice = %q, want a", hard.Choice)
	}
}

// TestHardVerifierFailRowIsNegative: a FAILED hard verifier lands a choice "b"
// (negative) row even though the PR merged — the un-gameable signal that the merged
// code fails a clean build/test.
func TestHardVerifierFailRowIsNegative(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, nil)

	if err := h.Harvest(ctx, implementJob(), payload, hardOutcome(false, map[string]float64{"go test ./...": 0.0})); err != nil {
		t.Fatalf("Harvest hard verifier returned error: %v", err)
	}
	events := feedbackForVersion(t, store, version.ID)
	if len(events) != 1 {
		t.Fatalf("expected exactly one hard row, got %d: %+v", len(events), events)
	}
	if events[0].Reviewer != hardVerifierReviewerID || events[0].Choice != "b" {
		t.Fatalf("hard FAIL row = reviewer %q choice %q, want %q / b", events[0].Reviewer, events[0].Choice, hardVerifierReviewerID)
	}
}

// TestHardVerifierItemMetadataRoundTrips: the hard item carries the hard_verifier
// tag, the binary verdict, the projected Hard score, and the per-command results, all
// recoverable via a JSON round-trip (contract round-trip, no new contract field).
func TestHardVerifierItemMetadataRoundTrips(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, nil)

	outcome := hardOutcome(false, map[string]float64{"go build ./...": 1.0, "go test ./...": 0.0})
	if err := h.Harvest(ctx, implementJob(), payload, outcome); err != nil {
		t.Fatalf("Harvest returned error: %v", err)
	}
	item, ok := hardItemFor(t, store, version.ID, hardVerifierItemID(outcome))
	if !ok {
		t.Fatalf("hard item %q not found", hardVerifierItemID(outcome))
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(item.MetadataJSON), &meta); err != nil {
		t.Fatalf("hard item metadata is not valid JSON: %v (%q)", err, item.MetadataJSON)
	}
	if meta[hardVerifierItemMetadataKey] != true {
		t.Fatalf("hard item must be tagged %s=true, got %v", hardVerifierItemMetadataKey, meta[hardVerifierItemMetadataKey])
	}
	if meta[hardVerifierItemMetadataPassedKey] != false {
		t.Fatalf("hard_passed must be false for a FAIL, got %v", meta[hardVerifierItemMetadataPassedKey])
	}
	score, ok := meta[hardVerifierItemMetadataScoreKey].(float64)
	if !ok || score != 0 {
		t.Fatalf("hard_score = %v, want 0 (fail)", meta[hardVerifierItemMetadataScoreKey])
	}
	cmds, ok := meta[hardVerifierItemMetadataCommandsKey].(map[string]any)
	if !ok {
		t.Fatalf("command_results must be a map, got %T (%v)", meta[hardVerifierItemMetadataCommandsKey], meta[hardVerifierItemMetadataCommandsKey])
	}
	if cmds["go build ./..."] != 1.0 || cmds["go test ./..."] != 0.0 {
		t.Fatalf("command_results = %v, want go build=1.0 go test=0.0", cmds)
	}
}

// TestHardVerifierReEvaluationOverwritesInPlace: a re-verification of the SAME PR
// (verdict flip pass -> fail) re-upserts the SAME row in place (row count stays 1),
// mirroring the corrective-overwrite discipline of the other auto-trace rows.
func TestHardVerifierReEvaluationOverwritesInPlace(t *testing.T) {
	ctx := context.Background()
	store := newTraceStore(t)
	version, payload := installTraceTemplate(t, store, "planner")
	h := NewOutcomeHarvester(store, nil)

	if err := h.Harvest(ctx, implementJob(), payload, hardOutcome(true, map[string]float64{"go test ./...": 1.0})); err != nil {
		t.Fatalf("Harvest pass returned error: %v", err)
	}
	if err := h.Harvest(ctx, implementJob(), payload, hardOutcome(false, map[string]float64{"go test ./...": 0.0})); err != nil {
		t.Fatalf("Harvest fail returned error: %v", err)
	}
	events := feedbackForVersion(t, store, version.ID)
	if len(events) != 1 {
		t.Fatalf("re-verification must overwrite in place (1 row), got %d: %+v", len(events), events)
	}
	if events[0].Choice != "b" {
		t.Fatalf("overwritten hard row choice = %q, want b (the later FAIL)", events[0].Choice)
	}
}
