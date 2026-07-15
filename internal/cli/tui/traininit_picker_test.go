package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gitmoot/gitmoot/internal/db"
)

func pickerField(checkRepo func(string) (bool, error), createRepo func(string) error) Field {
	return Field{
		Name:  "review_repo",
		Label: "Review repository",
		Kind:  FieldChoice,
		Choices: []Choice{
			{Value: "o/existing", Label: "o/existing"},
			{Custom: true, Label: "another repo…", Placeholder: "owner/repo"},
		},
		Prompt:     db.InteractivePrompt{ID: "picker-review-repo"},
		CheckRepo:  checkRepo,
		CreateRepo: createRepo,
	}
}

// repoFieldNamed is pickerField with a chosen name (for multi-field forms).
func repoFieldNamed(name string, checkRepo func(string) (bool, error), createRepo func(string) error) Field {
	f := pickerField(checkRepo, createRepo)
	f.Name = name
	f.Prompt = db.InteractivePrompt{ID: "picker-" + name}
	return f
}

func TestRepoPickerCreatedRepoAutoSelectedAcrossFields(t *testing.T) {
	createOne := func(string) error { return nil }
	missing := func(string) (bool, error) { return true, nil }
	interpret := func(_, text string) (string, string) {
		if !strings.Contains(text, "/") {
			return "", "reask"
		}
		return strings.TrimSpace(text), "ok"
	}
	review := repoFieldNamed("review_repo", missing, createOne)
	workspace := repoFieldNamed("workspace_repo", missing, createOne)
	// A non-repo field (no CreateRepo) must be left untouched.
	plain := Field{Name: "items", Label: "Items", Kind: FieldChoice,
		Choices: []Choice{{Value: "2", Label: "2"}}, Prompt: db.InteractivePrompt{ID: "items"}}

	form := NewTrainInit(newFakeStore(), []Field{review, workspace, plain}, nil, interpret, 0)
	var model tea.Model = form
	var lastCmd tea.Cmd
	step := func(msg tea.Msg) { next, cmd := model.Update(msg); model, lastCmd = next, cmd }

	step(initMsg{})
	step(tea.KeyMsg{Type: tea.KeyDown})  // → custom entry on review_repo
	step(tea.KeyMsg{Type: tea.KeyEnter}) // open text sub-state
	for _, r := range "o/fresh" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	step(tea.KeyMsg{Type: tea.KeyEnter}) // repo check
	step(lastCmd())                      // repoCheckMsg → missing
	step(key("c"))                       // create
	step(lastCmd())                      // repoCreatedMsg → registerCreatedRepo + commit → enters workspace_repo

	m := model.(TrainInitModel)
	if m.answers["review_repo"] != "o/fresh" {
		t.Fatalf("review_repo answer = %q, want o/fresh", m.answers["review_repo"])
	}
	// Both repo fields now carry the created repo as a choice and as their default.
	for _, name := range []string{"review_repo", "workspace_repo"} {
		f := fieldByName(m.fields, name)
		if f.Default != "o/fresh" {
			t.Fatalf("%s default = %q, want o/fresh", name, f.Default)
		}
		if countChoice(f.Choices, "o/fresh") != 1 {
			t.Fatalf("%s should list o/fresh exactly once: %+v", name, f.Choices)
		}
	}
	// The non-repo field is untouched.
	if plain := fieldByName(m.fields, "items"); plain.Default == "o/fresh" || countChoice(plain.Choices, "o/fresh") != 0 {
		t.Fatalf("non-repo field was polluted: %+v", plain)
	}
	// The form advanced to workspace_repo with the created repo pre-selected.
	if m.idx != 1 || m.choiceIdx != choiceIndex(m.fields[1].Choices, "o/fresh") {
		t.Fatalf("workspace_repo not pre-selected: idx=%d choiceIdx=%d", m.idx, m.choiceIdx)
	}
}

func fieldByName(fields []Field, name string) Field {
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	return Field{}
}

func countChoice(choices []Choice, value string) int {
	n := 0
	for _, c := range choices {
		if c.Value == value {
			n++
		}
	}
	return n
}

func TestRepoPickerSelectsKnownRepoDirectly(t *testing.T) {
	form := NewTrainInit(newFakeStore(), []Field{pickerField(nil, nil)}, nil, nil, 0)
	var model tea.Model = form
	step := func(msg tea.Msg) { next, _ := model.Update(msg); model = next }
	step(initMsg{})
	step(tea.KeyMsg{Type: tea.KeyEnter}) // first choice
	m := model.(TrainInitModel)
	if m.Result().Values["review_repo"] != "o/existing" {
		t.Fatalf("answers = %v", m.Result().Values)
	}
}

func TestRepoPickerCustomEntryRunsValidationAndRepoCheck(t *testing.T) {
	checked := ""
	created := ""
	field := pickerField(
		func(value string) (bool, error) { checked = value; return true, nil }, // missing
		func(value string) error { created = value; return nil },
	)
	interpret := func(name, text string) (string, string) {
		if !strings.Contains(text, "/") {
			return "", "reask"
		}
		return strings.TrimSpace(text), "ok"
	}
	form := NewTrainInit(newFakeStore(), []Field{field}, nil, interpret, 0)
	var model tea.Model = form
	var lastCmd tea.Cmd
	step := func(msg tea.Msg) {
		next, cmd := model.Update(msg)
		model = next
		lastCmd = cmd
	}
	step(initMsg{})
	step(tea.KeyMsg{Type: tea.KeyDown})  // → custom entry
	step(tea.KeyMsg{Type: tea.KeyEnter}) // open text sub-state
	if m := model.(TrainInitModel); m.input.Placeholder != "owner/repo" {
		t.Fatalf("placeholder = %q", m.input.Placeholder)
	}
	// Invalid shape re-asks.
	for _, r := range "nope" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	step(tea.KeyMsg{Type: tea.KeyEnter})
	if m := model.(TrainInitModel); m.inlineErr == "" {
		t.Fatal("invalid custom value must show an inline error")
	}
	// Valid shape goes through the repo check → missing → create offer.
	for i := 0; i < 4; i++ {
		step(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, r := range "o/new" {
		step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	step(tea.KeyMsg{Type: tea.KeyEnter})
	if lastCmd == nil {
		t.Fatal("valid custom value should run the repo check")
	}
	step(lastCmd()) // repoCheckMsg → missing → create offer
	step(key("c"))  // create
	if lastCmd == nil {
		t.Fatal("c should run the create")
	}
	step(lastCmd()) // repoCreatedMsg → commit
	m := model.(TrainInitModel)
	if checked != "o/new" || created != "o/new" {
		t.Fatalf("check/create = %q/%q", checked, created)
	}
	if m.Result().Values["review_repo"] != "o/new" {
		t.Fatalf("answers = %v", m.Result().Values)
	}
}
