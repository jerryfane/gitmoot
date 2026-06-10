package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/db"
)

func dashboardTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	paths := config.PathsForHome(home)
	if err := config.Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	return home
}

func seedDashboardPrompt(t *testing.T, home, id, question string, choices []string) {
	t.Helper()
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	if err := store.UpsertInteractivePrompt(context.Background(), db.InteractivePrompt{
		ID:            id,
		Question:      question,
		Choices:       choices,
		Required:      true,
		AnswerFormat:  "text",
		SourceCommand: "test",
	}); err != nil {
		t.Fatalf("UpsertInteractivePrompt returned error: %v", err)
	}
}

func TestDashboardSnapshotRendersSections(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "dash.prompt.one", "Pick a value", nil)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"dashboard", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("dashboard exit code = %d, stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"daemon: stopped",
		"repos: 0",
		"agents: 0",
		"runtime_sessions: 0",
		"jobs: 0",
		"branch_locks: 0",
		"train_sessions: 0",
		"pending_prompts: 1",
		"dash.prompt.one\tPick a value",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard output missing %q:\n%s", want, out)
		}
	}
}

func TestDashboardJSONPromptsMatchInteractiveList(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "dash.prompt.alpha", "Alpha?", nil)
	seedDashboardPrompt(t, home, "dash.prompt.beta", "Beta?", []string{"x", "y"})

	var dashOut, dashErr bytes.Buffer
	if code := Run([]string{"dashboard", "--home", home, "--json"}, &dashOut, &dashErr); code != 0 {
		t.Fatalf("dashboard --json exit code = %d, stderr=%s", code, dashErr.String())
	}
	var snapshot dashboardSnapshot
	if err := json.Unmarshal(dashOut.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode dashboard snapshot: %v\n%s", err, dashOut.String())
	}
	dashIDs := map[string]bool{}
	for _, prompt := range snapshot.PendingPrompts {
		dashIDs[prompt.ID] = true
	}

	var listOut, listErr bytes.Buffer
	if code := Run([]string{"interactive", "list", "--home", home, "--state", "pending", "--json"}, &listOut, &listErr); code != 0 {
		t.Fatalf("interactive list exit code = %d, stderr=%s", code, listErr.String())
	}
	var listPrompts []db.InteractivePrompt
	if err := json.Unmarshal(listOut.Bytes(), &listPrompts); err != nil {
		t.Fatalf("decode interactive list: %v\n%s", err, listOut.String())
	}
	if len(listPrompts) != len(snapshot.PendingPrompts) {
		t.Fatalf("dashboard pending prompts (%d) != interactive list (%d)", len(snapshot.PendingPrompts), len(listPrompts))
	}
	for _, prompt := range listPrompts {
		if !dashIDs[prompt.ID] {
			t.Fatalf("interactive list prompt %q missing from dashboard: %+v", prompt.ID, snapshot.PendingPrompts)
		}
	}
}

func TestDashboardAnswerResolvesPromptThroughSharedAPI(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "dash.prompt.answerable", "Choose", []string{"keep", "drop"})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"dashboard", "--home", home, "--answer", "dash.prompt.answerable", "--value", "keep", "--source", "test"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("dashboard --answer exit code = %d, stderr=%s", code, stderr.String())
	}
	// The snapshot after answering shows no pending prompts.
	if !strings.Contains(stdout.String(), "pending_prompts: 0") {
		t.Fatalf("answered prompt should not remain pending:\n%s", stdout.String())
	}
	// The prompt is resolved through the same store API interactive answer uses.
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	prompt, err := store.GetInteractivePrompt(context.Background(), "dash.prompt.answerable")
	if err != nil {
		t.Fatalf("GetInteractivePrompt returned error: %v", err)
	}
	if prompt.State != db.InteractivePromptStateResolved || prompt.AnswerValue != "keep" || prompt.AnswerSource != "test" {
		t.Fatalf("prompt not resolved via shared API: %+v", prompt)
	}
}

func TestDashboardAnswerRejectsInvalidChoiceAndKeepsPrompt(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "dash.prompt.choice", "Choose", []string{"keep", "drop"})

	var stdout, stderr bytes.Buffer
	code := Run([]string{"dashboard", "--home", home, "--answer", "dash.prompt.choice", "--value", "bogus"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("invalid dashboard answer exit code = %d, want 1; stderr=%s", code, stderr.String())
	}
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	prompt, err := store.GetInteractivePrompt(context.Background(), "dash.prompt.choice")
	if err != nil {
		t.Fatalf("GetInteractivePrompt returned error: %v", err)
	}
	if prompt.State != db.InteractivePromptStatePending {
		t.Fatalf("invalid answer should leave prompt pending: %+v", prompt)
	}
}

func TestDashboardWithoutAnswerDoesNotMutate(t *testing.T) {
	home := dashboardTestHome(t)
	seedDashboardPrompt(t, home, "dash.prompt.untouched", "Choose", nil)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"dashboard", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("dashboard exit code = %d, stderr=%s", code, stderr.String())
	}
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
	prompt, err := store.GetInteractivePrompt(context.Background(), "dash.prompt.untouched")
	if err != nil {
		t.Fatalf("GetInteractivePrompt returned error: %v", err)
	}
	if prompt.State != db.InteractivePromptStatePending {
		t.Fatalf("dashboard without --answer must not resolve prompts: %+v", prompt)
	}
}
