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

	// MessageEOF signals the end of the message stream.
	MessageEOF MessageType = "eof"
)

// Message is a structured output from an agent process.
type Message struct {
	// Type identifies the kind of message.
	Type MessageType `json:"type"`

	// Content is the text content (for Text, Error, System messages).
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
	RawLine string `json:"raw_line,omitempty"`

	// Timestamp is when the message was produced.
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
