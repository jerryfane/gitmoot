package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jerryfane/gitmoot/internal/db"
)

type fakeStore struct {
	upserts  []string
	deletes  []string
	resolved map[string]db.InteractivePrompt
}

func newFakeStore() *fakeStore { return &fakeStore{resolved: map[string]db.InteractivePrompt{}} }

func (f *fakeStore) UpsertInteractivePrompt(_ context.Context, p db.InteractivePrompt) error {
	f.upserts = append(f.upserts, p.ID)
	return nil
}

func (f *fakeStore) GetInteractivePrompt(_ context.Context, id string) (db.InteractivePrompt, error) {
	if p, ok := f.resolved[id]; ok {
		return p, nil
	}
	return db.InteractivePrompt{ID: id, State: db.InteractivePromptStatePending}, nil
}

func (f *fakeStore) DeleteInteractivePrompt(_ context.Context, id string) error {
	f.deletes = append(f.deletes, id)
	return nil
}

func tiFields() []Field {
	return []Field{
		{Name: "name", Label: "Training name", Kind: FieldText, Prompt: db.InteractivePrompt{ID: "ti.name"}},
		{Name: "template", Label: "Template", Kind: FieldTemplate, Prompt: db.InteractivePrompt{ID: "ti.template"}, Choices: []Choice{
			{Value: "planner", Label: "planner"},
			{Value: "writer", Label: "writer"},
			{Custom: true, Label: "Custom file"},
		}},
		{Name: "preview", Label: "Preview", Kind: FieldChoice, Prompt: db.InteractivePrompt{ID: "ti.preview"}, Default: "text-table", Choices: []Choice{
			{Value: "none", Label: "none"},
			{Value: "text-table", Label: "text-table"},
			{Value: "vue", Label: "vue"},
		}},
	}
}

func tiSummary(answers map[string]string) [][]string {
	return [][]string{{"name", answers["name"]}, {"template", answers["template"]}, {"preview", answers["preview"]}}
}

func startTI(t *testing.T, store PromptStore, fields []Field) TrainInitModel {
	t.Helper()
	m := NewTrainInit(store, fields, tiSummary, nil, time.Millisecond)
	next, _ := m.Update(initMsg{})
	tm := next.(TrainInitModel)
	if tm.state != tiField || tm.idx != 0 {
		t.Fatalf("expected first field active, state=%v idx=%d", tm.state, tm.idx)
	}
	return tm
}

func TestTrainInitTextSubmitAdvances(t *testing.T) {
	m := startTI(t, newFakeStore(), tiFields())
	next, _ := m.Update(key("smithyx"))
	m = next.(TrainInitModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(TrainInitModel)
	if m.Result().Values["name"] != "smithyx" {
		t.Fatalf("name not recorded: %+v", m.Result().Values)
	}
	if m.idx != 1 || m.fields[m.idx].Name != "template" {
		t.Fatalf("expected to advance to template, idx=%d", m.idx)
	}
}

func TestTrainInitEmptyTextInlineError(t *testing.T) {
	m := startTI(t, newFakeStore(), tiFields())
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(TrainInitModel)
	if m.idx != 0 || m.state != tiField {
		t.Fatalf("empty submit must not advance, idx=%d state=%v", m.idx, m.state)
	}
	if m.inlineErr == "" || !strings.Contains(m.View(), m.inlineErr) {
		t.Fatalf("expected inline error in view:\n%s", m.View())
	}
}

func TestTrainInitChoiceDefaultPreselected(t *testing.T) {
	m := startTI(t, newFakeStore(), tiFields())
	// name → template → preview.
	m = advanceText(t, m, "smithyx")
	m = advanceChoice(t, m) // template: select first (planner)
	if m.fields[m.idx].Name != "preview" {
		t.Fatalf("expected preview field, got %s", m.fields[m.idx].Name)
	}
	if m.choiceIdx != 1 {
		t.Fatalf("default 'text-table' should preselect idx 1, got %d", m.choiceIdx)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(TrainInitModel)
	if m.Result().Values["preview"] != "text-table" {
		t.Fatalf("preview answer = %q", m.Result().Values["preview"])
	}
}

func TestTrainInitTemplateCustomFile(t *testing.T) {
	m := startTI(t, newFakeStore(), tiFields())
	m = advanceText(t, m, "smithyx")
	// On template field: move to "Custom file" (index 2).
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(TrainInitModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(TrainInitModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(TrainInitModel)
	if m.state != tiCustomPath {
		t.Fatalf("custom file should open the path sub-state, state=%v", m.state)
	}
	// Esc returns to the list, prompt record still pending (same field).
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(TrainInitModel)
	if m.state != tiField || m.idx != 1 {
		t.Fatalf("esc should return to the template list, state=%v idx=%d", m.state, m.idx)
	}
	// Re-open and submit a path.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(TrainInitModel)
	next, _ = m.Update(key("./my/template.md"))
	m = next.(TrainInitModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(TrainInitModel)
	if m.Result().Values["template"] != "./my/template.md" {
		t.Fatalf("custom path answer = %q", m.Result().Values["template"])
	}
}

func TestTrainInitExternalAnswerAdvancesWithFlash(t *testing.T) {
	m := startTI(t, newFakeStore(), tiFields())
	resolved := db.InteractivePrompt{ID: "ti.name", State: db.InteractivePromptStateResolved, AnswerValue: "remote-name", AnswerSource: "agent"}
	next, _ := m.Update(pollResultMsg{gen: m.gen, prompt: resolved})
	m = next.(TrainInitModel)
	if m.Result().Values["name"] != "remote-name" {
		t.Fatalf("external answer not recorded: %+v", m.Result().Values)
	}
	if !m.external {
		t.Fatal("external flag should be set")
	}
	if !strings.Contains(m.flash, "answered externally by agent") {
		t.Fatalf("flash = %q", m.flash)
	}
	if m.idx != 1 {
		t.Fatalf("should advance after external answer, idx=%d", m.idx)
	}
}

func TestTrainInitStalePollIgnored(t *testing.T) {
	m := startTI(t, newFakeStore(), tiFields())
	before := m.idx
	resolved := db.InteractivePrompt{ID: "ti.name", State: db.InteractivePromptStateResolved, AnswerValue: "x"}
	next, _ := m.Update(pollResultMsg{gen: m.gen - 1, prompt: resolved})
	m = next.(TrainInitModel)
	if m.idx != before || len(m.Result().Values) != 0 {
		t.Fatalf("stale poll result must be ignored, idx=%d values=%+v", m.idx, m.Result().Values)
	}
	_, cmd := m.Update(pollTickMsg{gen: m.gen - 1})
	if cmd != nil {
		t.Fatal("stale tick should produce no command")
	}
}

func TestTrainInitConfirmAcceptDeclineAbort(t *testing.T) {
	single := []Field{{Name: "name", Label: "Name", Kind: FieldText, Prompt: db.InteractivePrompt{ID: "ti.name"}}}

	// Accept.
	m := startTI(t, newFakeStore(), single)
	m = advanceText(t, m, "n")
	if m.state != tiConfirm {
		t.Fatalf("last field should reach confirm, state=%v", m.state)
	}
	next, _ := m.Update(key("y"))
	m = next.(TrainInitModel)
	if m.state != tiDone || m.Result().Aborted {
		t.Fatalf("y should accept: state=%v aborted=%v", m.state, m.Result().Aborted)
	}

	// Decline.
	m = startTI(t, newFakeStore(), single)
	m = advanceText(t, m, "n")
	next, _ = m.Update(key("n"))
	m = next.(TrainInitModel)
	if !m.Result().Aborted {
		t.Fatal("n should abort at confirm")
	}

	// Esc aborts from a field.
	m = startTI(t, newFakeStore(), single)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(TrainInitModel)
	if !m.Result().Aborted {
		t.Fatal("esc should abort")
	}
}

func TestTrainInitAutoAcceptWhenExternallyAnswered(t *testing.T) {
	single := []Field{{Name: "name", Label: "Name", Kind: FieldText, Prompt: db.InteractivePrompt{ID: "ti.name"}}}
	m := startTI(t, newFakeStore(), single)
	resolved := db.InteractivePrompt{ID: "ti.name", State: db.InteractivePromptStateResolved, AnswerValue: "x", AnswerSource: "agent"}
	next, _ := m.Update(pollResultMsg{gen: m.gen, prompt: resolved})
	m = next.(TrainInitModel)
	if m.state != tiDone {
		t.Fatalf("externally-answered last field should auto-accept, state=%v", m.state)
	}
	if m.Result().Aborted || !m.Result().ExternallyAnswered {
		t.Fatalf("result = %+v", m.Result())
	}
}

func TestTrainInitProgressHeader(t *testing.T) {
	m := startTI(t, newFakeStore(), tiFields())
	if !strings.Contains(m.View(), "[1/3]") {
		t.Fatalf("expected progress header:\n%s", m.View())
	}
}

// TestTrainInitPromptCmdsAgainstRealStore proves the upsert/check/delete commands
// drive real SQL and observe an external answer, one prompt at a time.
func TestTrainInitPromptCmdsAgainstRealStore(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "gitmoot.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	prompt := db.InteractivePrompt{ID: "ti.name", Question: "Training name?", Required: true}
	if msg := upsertPromptCmd(store, prompt, 1)(); msg.(promptReadyMsg).err != nil {
		t.Fatalf("upsert: %v", msg.(promptReadyMsg).err)
	}
	res := checkPromptCmd(store, "ti.name", 1)().(pollResultMsg)
	if res.err != nil || res.prompt.State != db.InteractivePromptStatePending {
		t.Fatalf("expected pending, got state=%q err=%v", res.prompt.State, res.err)
	}
	if _, err := store.AnswerInteractivePrompt(context.Background(), "ti.name", "remote", "agent"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	res = checkPromptCmd(store, "ti.name", 1)().(pollResultMsg)
	if res.prompt.State != db.InteractivePromptStateResolved || res.prompt.AnswerValue != "remote" {
		t.Fatalf("expected resolved 'remote', got %+v", res.prompt)
	}
	if msg := deletePromptCmd(store, "ti.name")(); msg.(promptGoneMsg).err != nil {
		t.Fatalf("delete: %v", msg.(promptGoneMsg).err)
	}
	remaining, err := store.ListInteractivePrompts(context.Background(), "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("prompt should be gone: %+v", remaining)
	}
}

// advanceText types value into the current text field and submits.
func advanceText(t *testing.T, m TrainInitModel, value string) TrainInitModel {
	t.Helper()
	next, _ := m.Update(key(value))
	m = next.(TrainInitModel)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(TrainInitModel)
}

// advanceChoice selects the current choice (enter) on a choice/template field.
func advanceChoice(t *testing.T, m TrainInitModel) TrainInitModel {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(TrainInitModel)
}
