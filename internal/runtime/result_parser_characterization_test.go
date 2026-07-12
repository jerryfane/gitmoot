package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func readTranscriptFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", name))
	if err != nil {
		t.Fatalf("read transcript fixture %s: %v", name, err)
	}
	return string(b)
}

func TestResultParsersCharacterization(t *testing.T) {
	t.Run("codex_result_and_usage", func(t *testing.T) {
		raw, in, out, ok := parseCodexJSONResult(readTranscriptFixture(t, "codex.jsonl"))
		if !ok || raw != "Implemented the renderer.\n\nAll focused tests pass." || in != 16504 || out != 20 {
			t.Fatalf("parseCodexJSONResult = (%q, %d, %d, %v)", raw, in, out, ok)
		}
	})

	t.Run("kimi_content_resume_and_usage", func(t *testing.T) {
		content, sessionID, usage, err := parseKimiStreamJSON(readTranscriptFixture(t, "kimi.jsonl"))
		if err != nil {
			t.Fatalf("parseKimiStreamJSON: %v", err)
		}
		if content != "Implemented the renderer." || sessionID != "session_sanitized" || usage != (kimiUsage{}) {
			t.Fatalf("parseKimiStreamJSON = (%q, %q, %+v)", content, sessionID, usage)
		}
	})

	t.Run("claude_result_and_usage", func(t *testing.T) {
		summary, in, out := parseClaudeJSONResult(readTranscriptFixture(t, "claude.json"))
		if summary != "Implemented the renderer." || in != 1234 || out != 567 {
			t.Fatalf("parseClaudeJSONResult = (%q, %d, %d)", summary, in, out)
		}
	})
}
