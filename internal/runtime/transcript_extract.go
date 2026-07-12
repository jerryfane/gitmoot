package runtime

import (
	"encoding/json"
	"strings"
)

// StreamUsage is the token-count shape shared by the runtime wire formats.
// It is internal to gitmoot; callers should treat missing usage as zero.
type StreamUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// CodexStreamEvent is the verified subset of a codex exec --json line. ItemRaw
// deliberately retains the complete item object so consumers can render
// unverified item types generically without inventing field schemas.
type CodexStreamEvent struct {
	Type         string
	ItemType     string
	Text         string
	ItemRaw      json.RawMessage
	Usage        StreamUsage
	Message      string
	ErrorMessage string
}

// ExtractCodexStreamEvent owns the codex JSONL wire-format knowledge shared by
// result parsing and transcript rendering.
func ExtractCodexStreamEvent(line string) (CodexStreamEvent, error) {
	var wire struct {
		Type    string          `json:"type"`
		Item    json.RawMessage `json:"item"`
		Usage   StreamUsage     `json:"usage"`
		Message string          `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &wire); err != nil {
		return CodexStreamEvent{}, err
	}
	event := CodexStreamEvent{
		Type:         wire.Type,
		ItemRaw:      wire.Item,
		Usage:        wire.Usage,
		Message:      wire.Message,
		ErrorMessage: wire.Error.Message,
	}
	if len(wire.Item) != 0 {
		var item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(wire.Item, &item); err == nil {
			event.ItemType = item.Type
			event.Text = item.Text
		}
	}
	return event, nil
}

// ClaudeResultEnvelope is the verified subset of Claude Code's single final
// --output-format json envelope.
type ClaudeResultEnvelope struct {
	Result string
	Usage  StreamUsage
}

// ExtractClaudeResultEnvelope owns Claude's final-envelope wire format.
func ExtractClaudeResultEnvelope(stdout string) (ClaudeResultEnvelope, error) {
	var wire struct {
		Result string      `json:"result"`
		Usage  StreamUsage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(stdout), &wire); err != nil {
		return ClaudeResultEnvelope{}, err
	}
	return ClaudeResultEnvelope{Result: wire.Result, Usage: wire.Usage}, nil
}

// KimiStreamEvent is the verified subset of one Kimi stream-json line.
type KimiStreamEvent struct {
	Role        string
	Type        string
	ContentText string
	SessionID   string
	Usage       *StreamUsage
}

// ExtractKimiStreamEvent owns Kimi's stream-json wire format, including its
// two observed content encodings (a string and an array of text parts).
func ExtractKimiStreamEvent(line string) (KimiStreamEvent, error) {
	var wire struct {
		Role      string          `json:"role"`
		Type      string          `json:"type"`
		Content   json.RawMessage `json:"content"`
		SessionID string          `json:"session_id"`
		Usage     *StreamUsage    `json:"usage"`
	}
	if err := json.Unmarshal([]byte(line), &wire); err != nil {
		return KimiStreamEvent{}, err
	}
	return KimiStreamEvent{
		Role:        wire.Role,
		Type:        wire.Type,
		ContentText: extractKimiContentText(wire.Content),
		SessionID:   wire.SessionID,
		Usage:       wire.Usage,
	}, nil
}

func extractKimiContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var result strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			result.WriteString(part.Text)
		}
	}
	return result.String()
}
