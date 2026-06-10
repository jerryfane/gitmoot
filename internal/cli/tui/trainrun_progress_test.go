package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
)

func TestTrainRunLongPhaseShowsSpinnerElapsedAndHeader(t *testing.T) {
	snap := TrainRunSnapshot{
		SessionID:        "s",
		Phase:            "optimizer_running",
		PhaseStartedAt:   time.Now().Add(-5 * time.Minute),
		OptimizerBackend: "claude",
		OptimizerModel:   "claude-sonnet-4-6",
		OptimizerAttempt: "attempt-001",
	}
	m := trainRunModelWithDeps(t, TrainRunDeps{Load: func() (TrainRunSnapshot, error) { return snap, nil }}, snap)
	if !m.spinning {
		t.Fatal("a long-phase snapshot should arm the spinner")
	}
	view := m.View()
	for _, want := range []string{"optimizing", "elapsed 5m0s", "optimizer: claude · claude-sonnet-4-6 · attempt-001"} {
		if !strings.Contains(view, want) {
			t.Fatalf("long-phase view missing %q:\n%s", want, view)
		}
	}
}

func TestTrainRunSpinnerDisarmsOutsideLongPhases(t *testing.T) {
	long := TrainRunSnapshot{SessionID: "s", Phase: "optimizer_running"}
	m := trainRunModelWithDeps(t, TrainRunDeps{Load: func() (TrainRunSnapshot, error) { return long, nil }}, long)
	if !m.spinning {
		t.Fatal("long phase should arm the spinner")
	}
	next, _ := m.Update(trainSnapshotMsg{snap: TrainRunSnapshot{SessionID: "s", Phase: "candidate_created"}, at: time.Unix(2, 0)})
	m = next.(TrainRunModel)
	if m.spinning {
		t.Fatal("leaving the long phase should disarm the spinner")
	}
	// A stale spinner tick must not re-arm a dead chain.
	if _, cmd := m.Update(spinner.TickMsg{ID: m.spin.ID()}); cmd != nil {
		t.Fatal("stale spinner tick must be dropped")
	}
}

func TestTrainRunLiveElapsedFallsBackToSnapshotText(t *testing.T) {
	m := TrainRunModel{snap: TrainRunSnapshot{Elapsed: "2m"}}
	if got := m.liveElapsed(); got != "2m" {
		t.Fatalf("fallback elapsed = %q", got)
	}
}

func TestTrainRunSkillPreview(t *testing.T) {
	snap := TrainRunSnapshot{
		SessionID:        "s",
		Phase:            "optimizer_completed_candidate",
		ActionPhase:      "candidate_promoted",
		Terminal:         true,
		CandidateVersion: "tpl@v2",
	}
	asked := ""
	deps := TrainRunDeps{
		Load: func() (TrainRunSnapshot, error) { return snap, nil },
		TemplateVersionContent: func(versionRef string) (string, error) {
			asked = versionRef
			return "---\nid: tpl\n---\n\n# The Skill\n\nBody.", nil
		},
	}
	m := trainRunModelWithDeps(t, deps, snap)
	if !strings.Contains(m.View(), "v view skill") {
		t.Fatalf("footer should offer the preview:\n%s", m.View())
	}
	next, cmd := m.Update(key("v"))
	m = next.(TrainRunModel)
	if cmd == nil {
		t.Fatal("v should load the skill content")
	}
	if !strings.Contains(m.View(), "loading…") {
		t.Fatalf("pager should show loading first:\n%s", m.View())
	}
	next, _ = m.Update(cmd())
	m = next.(TrainRunModel)
	if asked != "tpl@v2" {
		t.Fatalf("TemplateVersionContent asked for %q", asked)
	}
	if !strings.Contains(m.View(), "# The Skill") || !strings.Contains(m.View(), "skill — tpl@v2") {
		t.Fatalf("pager missing content:\n%s", m.View())
	}
	// esc returns to the normal terminal screen.
	next, _ = m.Update(key("v"))
	m = next.(TrainRunModel)
	if m.mode != trainModeNormal || !strings.Contains(m.View(), "n start next iteration") {
		t.Fatalf("v again should close the pager, mode=%v", m.mode)
	}
}

func TestTrainRunSkillPreviewInvalidatesOnNewCandidate(t *testing.T) {
	snap := TrainRunSnapshot{
		SessionID: "s", Phase: "optimizer_completed_candidate",
		ActionPhase: "candidate_promoted", Terminal: true, CandidateVersion: "tpl@v2",
	}
	contents := map[string]string{"tpl@v2": "old body v2", "tpl@v3": "new body v3"}
	deps := TrainRunDeps{
		Load:                   func() (TrainRunSnapshot, error) { return snap, nil },
		TemplateVersionContent: func(ref string) (string, error) { return contents[ref], nil },
	}
	m := trainRunModelWithDeps(t, deps, snap)
	next, cmd := m.Update(key("v"))
	m = next.(TrainRunModel)
	next, _ = m.Update(cmd())
	m = next.(TrainRunModel)
	if !strings.Contains(m.View(), "old body v2") {
		t.Fatalf("v2 content missing:\n%s", m.View())
	}
	next, _ = m.Update(key("v")) // close
	m = next.(TrainRunModel)
	// A new candidate arrives; v must fetch the NEW content, not reuse v2's.
	fresh := snap
	fresh.CandidateVersion = "tpl@v3"
	next, _ = m.Update(trainSnapshotMsg{snap: fresh, at: time.Unix(2, 0)})
	m = next.(TrainRunModel)
	next, cmd = m.Update(key("v"))
	m = next.(TrainRunModel)
	if cmd == nil {
		t.Fatal("a new candidate must re-fetch")
	}
	next, _ = m.Update(cmd())
	m = next.(TrainRunModel)
	if !strings.Contains(m.View(), "new body v3") || strings.Contains(m.View(), "old body v2") {
		t.Fatalf("stale cache shown for the new candidate:\n%s", m.View())
	}
}

func TestTrainRunSkillPreviewUnavailableWithoutCandidate(t *testing.T) {
	snap := TrainRunSnapshot{SessionID: "s", Phase: "review_published", ActionPhase: "review_published"}
	deps := TrainRunDeps{
		Load:                   func() (TrainRunSnapshot, error) { return snap, nil },
		TemplateVersionContent: func(string) (string, error) { t.Fatal("must not load"); return "", nil },
	}
	m := trainRunModelWithDeps(t, deps, snap)
	next, cmd := m.Update(key("v"))
	m = next.(TrainRunModel)
	if cmd != nil || m.mode != trainModeNormal {
		t.Fatal("v without a candidate must be a no-op")
	}
}
