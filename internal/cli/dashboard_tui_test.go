package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/db"
)

func TestShouldLaunchTUI(t *testing.T) {
	cases := []struct {
		name       string
		flags      dashboardFlags
		stdoutTTY  bool
		stdinTTY   bool
		wantLaunch bool
	}{
		{"both ttys no flags", dashboardFlags{}, true, true, true},
		{"stdout not tty", dashboardFlags{}, false, true, false},
		{"stdin not tty", dashboardFlags{}, true, false, false},
		{"plain", dashboardFlags{plain: true}, true, true, false},
		{"json", dashboardFlags{jsonOutput: true}, true, true, false},
		{"all", dashboardFlags{all: true}, true, true, false},
		{"watch", dashboardFlags{watch: true}, true, true, false},
		{"answer", dashboardFlags{answerID: "p1"}, true, true, false},
		{"answer whitespace", dashboardFlags{answerID: "  "}, true, true, true},
		{"dismiss", dashboardFlags{dismissID: "p1"}, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldLaunchTUI(tc.flags, tc.stdoutTTY, tc.stdinTTY); got != tc.wantLaunch {
				t.Fatalf("shouldLaunchTUI(%+v, %v, %v) = %v, want %v", tc.flags, tc.stdoutTTY, tc.stdinTTY, got, tc.wantLaunch)
			}
		})
	}
}

// TestDashboardNonTTYStaysPlain guards the core compatibility promise: with a
// bytes.Buffer (never a terminal), the dashboard prints the one-shot snapshot
// and never launches the TUI, regardless of the new --plain flag default.
func TestDashboardNonTTYStaysPlain(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "compat.prompt", "Choose", nil)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"dashboard", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("dashboard exit = %d, stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"daemon:", "pending_prompts: 1", "needs attention:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected plain snapshot to contain %q:\n%s", want, out)
		}
	}
}

// TestToTUISnapshotCarriesPromptDetails verifies the snapshot exposes the full
// prompt records the TUI needs, sourced from the same store query.
func TestToTUISnapshotCarriesPromptDetails(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "detail.prompt", "Pick one", []string{"a", "b"})

	paths, err := initializedPaths(home)
	if err != nil {
		t.Fatalf("initializedPaths: %v", err)
	}
	snap, err := buildDashboardSnapshot(home, paths)
	if err != nil {
		t.Fatalf("buildDashboardSnapshot: %v", err)
	}
	if len(snap.promptDetails) != 1 || snap.promptDetails[0].ID != "detail.prompt" {
		t.Fatalf("promptDetails = %+v", snap.promptDetails)
	}
	tuiSnap := toTUISnapshot(snap)
	if len(tuiSnap.Prompts) != 1 || tuiSnap.Prompts[0].ID != "detail.prompt" || len(tuiSnap.Prompts[0].Choices) != 2 {
		t.Fatalf("tui prompts = %+v", tuiSnap.Prompts)
	}
}

// TestDashboardTUIDepsActions exercises the injected Answer/Dismiss closures end
// to end against a real store, the same APIs the model will call.
func TestDashboardTUIDepsActions(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "act.answer", "Choose", []string{"keep", "drop"})
	seedDashboardPrompt(t, home, "act.dismiss", "Choose", nil)

	deps := dashboardTUIDeps(home, 0)
	if err := deps.Answer("act.answer", "keep"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if err := deps.Dismiss("act.dismiss"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	answered, err := store.GetInteractivePrompt(context.Background(), "act.answer")
	if err != nil {
		t.Fatalf("get answered: %v", err)
	}
	if answered.State != db.InteractivePromptStateResolved || answered.AnswerValue != "keep" || answered.AnswerSource != "dashboard-tui" {
		t.Fatalf("answered prompt = %+v", answered)
	}
	remaining, err := store.ListInteractivePrompts(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != "act.answer" {
		t.Fatalf("dismiss should remove act.dismiss only: %+v", remaining)
	}
}
