package skillopt

import (
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/db"
)

// negFeedback is a harvested negative outcome (choice "b"): a changes-requested
// or reverted result, the lowest band.
func negFeedback() db.FeedbackEvent {
	return db.FeedbackEvent{Choice: "b", Reviewer: autoTraceReviewer, Source: autoTraceSource, Reasoning: "changes requested"}
}

func repeat(event db.FeedbackEvent, n int) []db.FeedbackEvent {
	out := make([]db.FeedbackEvent, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, event)
	}
	return out
}

func TestEvaluateCanaryRegression(t *testing.T) {
	cases := []struct {
		name                string
		canary              []db.FeedbackEvent
		champion            []db.FeedbackEvent
		minSamples          int
		canaryUnavailable   bool
		championUnavailable bool
		want                CanaryDecision
		reasonHas           string
	}{
		{
			// (a) canary MATERIALLY worse than champion with >= minSamples => rollback.
			name:       "material regression rolls back",
			canary:     repeat(negFeedback(), 4),
			champion:   repeat(realCIFeedback(), 4),
			minSamples: 3,
			want:       CanaryRollback,
			reasonHas:  "rolling back",
		},
		{
			// (b) fewer than minSamples canary outcomes => continue (keep sampling),
			// even though the few it has are terrible.
			name:       "thin canary samples hold",
			canary:     repeat(negFeedback(), 2),
			champion:   repeat(realCIFeedback(), 10),
			minSamples: 3,
			want:       CanaryContinue,
			reasonHas:  "below min_samples",
		},
		{
			// (c) canary feedback unavailable (read error) => continue: NEVER roll back
			// on evidence we could not read.
			name:              "canary unavailable holds (never roll back on unread evidence)",
			canary:            nil,
			champion:          repeat(realCIFeedback(), 10),
			minSamples:        3,
			canaryUnavailable: true,
			want:              CanaryContinue,
			reasonHas:         "unavailable",
		},
		{
			// (d) canary at parity-or-better than champion => graduate.
			name:       "parity or better graduates",
			canary:     repeat(realCIFeedback(), 5),
			champion:   repeat(realCIFeedback(), 5),
			minSamples: 3,
			want:       CanaryGraduate,
			reasonHas:  "graduating",
		},
		{
			// Canary strictly better than champion => graduate.
			name:       "improvement graduates",
			canary:     repeat(realCIFeedback(), 5),
			champion:   repeat(noCIFeedback(), 5),
			minSamples: 3,
			want:       CanaryGraduate,
			reasonHas:  "graduating",
		},
		{
			// No champion baseline (empty) => continue: cannot confirm regression OR
			// non-regression, so HOLD (fail-safe, never graduate without a baseline).
			name:       "no champion baseline holds",
			canary:     repeat(realCIFeedback(), 5),
			champion:   nil,
			minSamples: 3,
			want:       CanaryContinue,
			reasonHas:  "no champion baseline",
		},
		{
			// Champion feedback unavailable (read error) => continue (no baseline).
			name:                "champion unavailable holds",
			canary:              repeat(realCIFeedback(), 5),
			champion:            nil,
			minSamples:          3,
			championUnavailable: true,
			want:                CanaryContinue,
			reasonHas:           "champion feedback unavailable",
		},
		{
			// minSamples <= 0 (unset floor) => continue: never act without a real floor.
			name:       "no sample floor holds",
			canary:     repeat(negFeedback(), 10),
			champion:   repeat(realCIFeedback(), 10),
			minSamples: 0,
			want:       CanaryContinue,
			reasonHas:  "no canary sample floor",
		},
		{
			// A mild dip WITHIN the tolerance band (champion all real-CI=1.0, canary all
			// near-positive=0.6 => delta 0.4 >= 0.2) is material => rollback. Confirms the
			// band semantics: near-positive is materially below strong-positive.
			name:       "near-positive canary vs strong-positive champion is material",
			canary:     repeat(noCIFeedback(), 5),
			champion:   repeat(realCIFeedback(), 5),
			minSamples: 3,
			want:       CanaryRollback,
			reasonHas:  "rolling back",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := EvaluateCanaryRegression(tc.canary, tc.champion, "", tc.minSamples, tc.canaryUnavailable, tc.championUnavailable)
			if verdict.Decision != tc.want {
				t.Fatalf("Decision = %q, want %q (reason: %q)", verdict.Decision, tc.want, verdict.Reason)
			}
			if tc.reasonHas != "" && !strings.Contains(verdict.Reason, tc.reasonHas) {
				t.Fatalf("reason %q does not contain %q", verdict.Reason, tc.reasonHas)
			}
		})
	}
}

// at sets a feedback event's CreatedAt to an RFC3339 instant so the bounded-window
// comparator can include/exclude it.
func at(event db.FeedbackEvent, ts string) db.FeedbackEvent {
	event.CreatedAt = ts
	return event
}

// TestEvaluateCanaryRegressionWindow proves the comparator bounds BOTH sides to the
// canary window (CreatedAt >= windowStart): a champion whose stale PRE-window
// outcomes drag its lifetime mean DOWN no longer produces a low baseline that
// wrongly graduates a regressing canary. With windowing, only the champion's
// concurrent (in-window) strong outcomes count, so the regressing canary rolls back.
func TestEvaluateCanaryRegressionWindow(t *testing.T) {
	const windowStart = "2026-06-27T12:00:00Z"
	// Champion: a long history of PRE-window negatives (which drag its lifetime mean
	// far below parity) PLUS healthy in-window real-CI positives. Lifetime mean =
	// (25*0.0 + 5*1.0)/30 ≈ 0.167; windowed baseline = 5*1.0/5 = 1.0.
	champion := append(
		repeatAt(negFeedback(), 25, "2026-06-01T00:00:00Z"),
		repeatAt(realCIFeedback(), 5, "2026-06-27T13:00:00Z")...,
	)
	// Canary: all in-window negatives (score 0.0) — a genuine regression vs the
	// windowed champion.
	canary := repeatAt(negFeedback(), 5, "2026-06-27T13:30:00Z")

	// With windowing the champion baseline is the in-window 1.0, so the canary's 0.0
	// is materially below (0.0 < 1.0-0.2) => rollback.
	if v := EvaluateCanaryRegression(canary, champion, windowStart, 3, false, false); v.Decision != CanaryRollback {
		t.Fatalf("windowed: Decision = %q, want %q (reason: %q)", v.Decision, CanaryRollback, v.Reason)
	}
	// WITHOUT a window (empty), the champion's lifetime mean (~0.167) is dragged low
	// by the pre-window negatives, so the canary's 0.0 is NOT materially below it
	// (0.0 >= 0.167-0.2) => the regression is MASKED and it GRADUATES. This is the
	// apples-to-oranges bug the window fixes; asserting it here pins the contrast.
	if v := EvaluateCanaryRegression(canary, champion, "", 3, false, false); v.Decision != CanaryGraduate {
		t.Fatalf("lifetime (no window): Decision = %q, want %q (reason: %q)", v.Decision, CanaryGraduate, v.Reason)
	}
}

// TestParseCanaryWindowTime proves the window parser accepts both the RFC3339
// canary_started_at / harvester-default created_at AND SQLite's CURRENT_TIMESTAMP
// 'YYYY-MM-DD HH:MM:SS' form, all as the same UTC instant.
func TestParseCanaryWindowTime(t *testing.T) {
	rfc, ok := parseCanaryWindowTime("2026-06-27T12:00:00Z")
	if !ok {
		t.Fatal("RFC3339 must parse")
	}
	sqlite, ok := parseCanaryWindowTime("2026-06-27 12:00:00")
	if !ok {
		t.Fatal("SQLite datetime must parse")
	}
	if !rfc.Equal(sqlite) {
		t.Fatalf("RFC3339 %v and SQLite %v must be the same instant", rfc, sqlite)
	}
	if _, ok := parseCanaryWindowTime("  "); ok {
		t.Fatal("empty value must not parse (fail-open sentinel)")
	}
}

func repeatAt(event db.FeedbackEvent, n int, ts string) []db.FeedbackEvent {
	out := make([]db.FeedbackEvent, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, at(event, ts))
	}
	return out
}

// TestCanaryVersionScoreBands proves the coarse per-version score reuses the #465
// harvest vocabulary: a real-CI positive is the strong band, a near-neutral
// choice-"a" the mid band, a choice-"b" the low band, and non-a/b rows are not
// countable.
func TestCanaryVersionScoreBands(t *testing.T) {
	if score, n := canaryVersionScore([]db.FeedbackEvent{realCIFeedback()}); score != canaryScoreStrongPositive || n != 1 {
		t.Fatalf("real-CI score = (%v, %d), want (%v, 1)", score, n, canaryScoreStrongPositive)
	}
	if score, n := canaryVersionScore([]db.FeedbackEvent{noCIFeedback()}); score != canaryScoreNearPositive || n != 1 {
		t.Fatalf("near-positive score = (%v, %d), want (%v, 1)", score, n, canaryScoreNearPositive)
	}
	if score, n := canaryVersionScore([]db.FeedbackEvent{negFeedback()}); score != canaryScoreNegative || n != 1 {
		t.Fatalf("negative score = (%v, %d), want (%v, 1)", score, n, canaryScoreNegative)
	}
	// A non-a/b row carries no verdict and is not counted.
	if score, n := canaryVersionScore([]db.FeedbackEvent{{Choice: ""}}); score != 0 || n != 0 {
		t.Fatalf("uncountable score = (%v, %d), want (0, 0)", score, n)
	}
}
