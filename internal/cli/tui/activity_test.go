package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func activityPageModel(t *testing.T, snap Snapshot) Model {
	t.Helper()
	m := sizedModel(Deps{Load: func() (Snapshot, error) { return snap, nil }})
	next, _ := m.Update(snapshotMsg{snap: snap, at: time.Unix(1, 0)})
	m = next.(Model)
	return tabToPage(t, m, pageActivity)
}

func TestActivityPageRendersActiveTrees(t *testing.T) {
	snap := Snapshot{
		Daemon: Daemon{Running: true},
		Activity: []ActivityRoot{{
			JobID: "root-1", Agent: "planner", Action: "implement", State: "running", Repo: "o/r",
			Children: []JobChild{
				{DelegationID: "d1", Agent: "impl-a", Action: "build the API", State: "running"},
				{DelegationID: "d2", Agent: "impl-b", Action: "write tests", State: "blocked"},
			},
			ContinuationID: "cont-1", ContinuationState: "queued",
			Total: 2, Running: 1, Blocked: 1, Done: 0,
		}},
	}
	m := activityPageModel(t, snap)
	view := m.View()
	for _, want := range []string{"root-1", "o/r", "impl-a", "build the API", "impl-b", "2 delegations", "continuation"} {
		if !strings.Contains(view, want) {
			t.Fatalf("activity view missing %q:\n%s", want, view)
		}
	}
	// The cursor selects the root; enter opens its detail.
	if r, ok := m.activityUnderCursor(); !ok || r.JobID != "root-1" {
		t.Fatalf("cursor should select root-1, got %+v ok=%v", r, ok)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.mode != modeJobDetail || m.activeJob.ID != "root-1" {
		t.Fatalf("enter should open the root's job detail, mode=%v job=%q", m.mode, m.activeJob.ID)
	}
	_ = cmd
}

func TestActivityPageEmpty(t *testing.T) {
	m := activityPageModel(t, Snapshot{Daemon: Daemon{Running: true}})
	if !strings.Contains(m.View(), "No active jobs") {
		t.Fatalf("empty activity page should say so:\n%s", m.View())
	}
}
