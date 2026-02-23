package agentrun

import (
	"encoding/json"
	"time"
)

// MessageType identifies the kind of message from an agent process.
type MessageType string

const (
	// MessageText is assistant text output.
	MessageText MessageType = "text"

	// MessageToolUse indicates the agent is invoking a tool.
	MessageToolUse MessageType = "tool_use"

	// MessageToolResult contains the output of a tool invocation.
	MessageToolResult MessageType = "tool_result"

	// MessageError indicates an error from the agent or runtime.
	MessageError MessageType = "error"

	// MessageSystem contains system-level messages (e.g., status changes).
	MessageSystem MessageType = "system"

	// MessageInit is the handshake message sent at session start.
	MessageInit MessageType = "init"

	// MessageResult signals turn completion with optional usage data.
	MessageResult MessageType = "result"

	// MessageEOF signals the end of the message stream.
	MessageEOF MessageType = "eof"

	// MessageThinking contains complete thinking/reasoning content from models
	// with extended thinking enabled. Thinking content arrives as a complete
	// content block inside assistant messages.
	//
	// Requires OptionThinkingBudget to be set in Session.Options. Without it,
	// models think internally but do not expose thinking in their output.
	// The Completed() filter passes MessageThinking through; consumers wanting
	// only final text should use ResultOnly().
	MessageThinking MessageType = "thinking"

	// --- Streaming delta types ---
	//
	// Delta types carry partial content from token-level streaming.
	// Use filter.IsDelta() to test, filter.Completed() to drop them.

	// MessageTextDelta is a partial text token from streaming output.
	// Content holds the text fragment. Emitted when the backend supports
	// streaming and partial messages are enabled (default for Claude).
	MessageTextDelta MessageType = "text_delta"

	// MessageToolUseDelta is partial tool use input JSON from streaming output.
	// Content holds a JSON fragment. Emitted during incremental tool input.
	MessageToolUseDelta MessageType = "tool_use_delta"

	// MessageThinkingDelta is partial thinking content from streaming output.
	// Content holds a thinking text fragment.
	//
	// Emitted by the Claude CLI backend when OptionThinkingBudget is set and
	// streaming is enabled. Also emitted by API-based backends that expose
	// raw streaming thinking deltas.
	MessageThinkingDelta MessageType = "thinking_delta"
)

// Message is a structured output from an agent process.
type Message struct {
	// Type identifies the kind of message.
	Type MessageType `json:"type"`

	// Content is the text content. Holds complete text for Text messages,
	// error descriptions for Error, delta payloads (text fragment, JSON
	// fragment, or thinking fragment) for *Delta messages, and status
	// text for System messages.
	Content string `json:"content,omitempty"`

	// Tool contains tool invocation details (for ToolUse, ToolResult messages).
	Tool *ToolCall `json:"tool,omitempty"`

	// Usage contains token usage data (typically on Text messages).
	Usage *Usage `json:"usage,omitempty"`

	// Raw is the original unparsed JSON from the backend.
	// Backends populate this for pass-through or debugging.
	Raw json.RawMessage `json:"raw,omitempty"`

	// RawLine is the original unparsed output line from stdout.
	// Used for crash-recovery log pipelines and audit logging.
	//
	// RawLine may contain sensitive data (credentials, PHI) from agent
	// output. Implementations should redact or omit this field before
	// writing to persistent audit logs.
	RawLine string `json:"raw_line,omitempty"`

	// Timestamp is when the message was produced.
	// Producers should always set this; zero value serializes as
	// "0001-01-01T00:00:00Z".
	Timestamp time.Time `json:"timestamp"`
}

// ToolCall describes a tool invocation by the agent.
type ToolCall struct {
	// Name is the tool identifier.
	Name string `json:"name"`

	// Input is the tool's input parameters as raw JSON.
	Input json.RawMessage `json:"input,omitempty"`

	// Output is the tool's result as raw JSON.
	Output json.RawMessage `json:"output,omitempty"`
}

// Usage contains token usage data from the agent's model.
type Usage struct {
	// InputTokens is the cumulative context window fill.
	InputTokens int `json:"input_tokens"`

	// OutputTokens is the number of tokens generated.
	OutputTokens int `json:"output_tokens"`
}
