// Package transcript converts runtime tee logs into a small internal event
// model and renders a human-readable, redacted transcript.
package transcript

// Kind identifies one normalized transcript event. This is an internal model,
// not a persisted or public wire schema.
type Kind string

const (
	KindAgentText  Kind = "agent_text"
	KindToolCall   Kind = "tool_call"
	KindToolResult Kind = "tool_result"
	KindUsage      Kind = "usage"
	KindLifecycle  Kind = "lifecycle"
	KindRaw        Kind = "raw"
)

// Event is the normalized representation shared by all runtime translators.
type Event struct {
	Kind         Kind
	Text         string
	Name         string
	InputDigest  string
	Status       string
	OutputDigest string
	InputTokens  int
	OutputTokens int
	Phase        string
	Detail       string
	RawLine      string
}
