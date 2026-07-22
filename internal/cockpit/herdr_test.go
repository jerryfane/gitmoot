package cockpit

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestHerdrAgentPromptDelivered(t *testing.T) {
	var got []string
	client := herdrClient{run: func(_ context.Context, args ...string) (string, error) {
		got = append([]string(nil), args...)
		return `{"id":"prompt-1","result":{"type":"agent_prompted","agent":{"pane_id":"w1:p2"}}}`, nil
	}}
	delivered, stalled, err := client.agentPrompt(context.Background(), "w1:p2", "review this", "idle")
	if err != nil || !delivered || stalled {
		t.Fatalf("delivered=%v stalled=%v err=%v", delivered, stalled, err)
	}
	want := []string{"agent", "prompt", "w1:p2", "review this", "--wait", "--until", "idle"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%q want=%q", got, want)
	}
}

func TestHerdrAgentPromptStalledIsNotTransportError(t *testing.T) {
	client := herdrClient{run: func(_ context.Context, args ...string) (string, error) {
		return `{"id":"prompt-2","error":{"code":"agent_prompt_stalled","message":"state_change_seq remained 7"}}`, errors.New("exit status 1")
	}}
	delivered, stalled, err := client.agentPrompt(context.Background(), "w1:p2", "review this", "")
	if err != nil || delivered || !stalled {
		t.Fatalf("delivered=%v stalled=%v err=%v", delivered, stalled, err)
	}
}
