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
	// The completion reason is in Message.StopReason (not Content).
	// Token usage and cost data are in Message.Usage.
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

	// Content is the text content. Semantics vary by Type:
	//   - MessageText: assistant text output
	//   - MessageError: human-readable error description (see ErrorCode for machine-readable)
	//   - MessageSystem: status text
	//   - MessageResult: optional result text (may be empty; see StopReason for completion signal)
	//   - *Delta types: partial content fragment (text, JSON, or thinking)
	Content string `json:"content,omitempty"`

	// Tool contains tool invocation details (for ToolUse, ToolResult messages).
	Tool *ToolCall `json:"tool,omitempty"`

	// Usage contains token usage data (typically on Text messages).
	Usage *Usage `json:"usage,omitempty"`

	// StopReason indicates why the agent's turn ended.
	// Set exclusively on MessageResult messages. Empty means the backend
	// did not report a stop reason.
	//
	// Consumers should handle unknown values gracefully — backends may
	// report values beyond the StopReason constants defined in this package.
	StopReason StopReason `json:"stop_reason,omitempty"`

	// ErrorCode is the machine-readable error code from the backend.
	// Set exclusively on MessageError messages. Human-readable description
	// is in Content. Empty means no structured code was provided.
	//
	// Intentionally a plain string (not a named type like StopReason):
	// error codes have no universal constants across backends — CLI backends
	// emit string codes (e.g., "rate_limit"), ACP emits library-defined
	// constants (e.g., acp.ErrCodeToolCallFailed). A named type would imply
	// root-level constants that don't exist. Consumers should match on raw
	// string values or use backend-exported constants where available.
	//
	// Backend parsers populate this field when the wire format includes a
	// structured error code. Empty means no code was provided.
	ErrorCode string `json:"error_code,omitempty"`

	// ResumeID is the backend-assigned session identifier for resume.
	// Set exclusively on MessageInit messages. Consumers persist this value
	// and pass it back via OptionResumeID to resume the session later.
	// Empty means the backend could not capture a session ID.
	ResumeID string `json:"resume_id,omitempty"`

	// Raw is the original unparsed JSON from the backend.
	// Backends populate this for pass-through or debugging.
	Raw json.RawMessage `json:"raw,omitempty"`

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

// StopReason indicates why an agent's turn ended.
// This is output vocabulary — backends populate it, consumers read it.
// Unknown values pass through as-is (the type is an open set, not a closed enum).
//
// No Valid() method: unlike Mode/HITL/Effort (input vocabulary validated
// before reaching a subprocess), StopReason is read-only output that
// consumers match on. Adding Valid() would imply a closed set and force
// updates to root when a new backend introduces a new stop reason.
type StopReason string

const (
	// StopEndTurn means the agent completed its response normally.
	StopEndTurn StopReason = "end_turn"

	// StopMaxTokens means the response was truncated due to token limits.
	StopMaxTokens StopReason = "max_tokens"

	// StopToolUse means the agent stopped to invoke a tool.
	StopToolUse StopReason = "tool_use"
)

// Usage contains token usage data from the agent's model.
type Usage struct {
	// InputTokens is the cumulative context window fill.
	// Always serialized (0 means zero tokens used).
	InputTokens int `json:"input_tokens"`

	// OutputTokens is the number of tokens generated.
	// Always serialized (0 means zero tokens generated).
	OutputTokens int `json:"output_tokens"`

	// CacheReadTokens is tokens served from cache instead of recomputed.
	// Omitted when zero (0 means not reported by this backend).
	CacheReadTokens int `json:"cache_read_tokens,omitempty"`

	// CacheWriteTokens is tokens written to cache for future reuse.
	// Omitted when zero (0 means not reported by this backend).
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`

	// ThinkingTokens is tokens used for model reasoning/thinking.
	// Omitted when zero (0 means not reported by this backend).
	ThinkingTokens int `json:"thinking_tokens,omitempty"`

	// CostUSD is the estimated cost in USD for this turn.
	// Omitted when zero. Always a finite non-negative value; parsers
	// must sanitize NaN/Inf to zero before populating this field.
	// Approximate — not suitable for billing reconciliation.
	CostUSD float64 `json:"cost_usd,omitempty"`
}
