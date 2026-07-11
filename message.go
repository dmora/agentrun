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

	// MessageSubagentResult is the terminal result of a subagent (e.g., the
	// Task/Agent tool) nested inside the parent turn — NOT the parent turn's
	// completion.
	//
	// Two sources produce this type:
	//   1. Demotion: a subagent's own "result" line (parent_tool_use_id set)
	//      is demoted so it does not terminate the turn (issue #57).
	//   2. Notification (Claude CLI): a background subagent never emits its own
	//      result line — its completion arrives as a separate system
	//      "task_notification" with a terminal status. The Claude backend maps
	//      that line to MessageSubagentResult with ParentToolUseID set to the
	//      notification's tool_use_id, so background completions are visible to
	//      the same consumers.
	//
	// Either way this type does not terminate the turn: RunTurn/drainOutput
	// stop only on the parent's own MessageResult. Consumers that watch for
	// turn completion (e.g., filter.ResultOnly) should ignore this type.
	//
	// Field semantics on this message:
	//   - Content: the subagent's result text or notification summary.
	//   - ParentToolUseID: the completing subagent's correlation id.
	//   - Subagents: outstanding subagent stats after this completion (when
	//     tracking is enabled).
	//   - Raw: the original event, for consumers needing per-subagent detail.
	//   - Usage, StopReason, IsError, Denials: cleared. These describe the
	//     subagent, not the parent turn, and are dropped so they cannot leak
	//     into the parent's turn accounting (StopReason in particular would
	//     otherwise be carried forward onto the parent's real MessageResult).
	MessageSubagentResult MessageType = "subagent_result"

	// MessageBackgroundTasks carries an authoritative snapshot of the backend's
	// currently in-flight background subagent tasks, emitted every time that
	// set changes. Tools holds one entry per in-flight task, with
	// ToolCall.ID = the backend's task id; the slice is non-nil even when empty
	// (an empty snapshot means the set drained to zero).
	//
	// Source:
	//   - Claude CLI: synthesized from the native "background_tasks_changed"
	//     system event, filtered to subagent tasks (task_type "local_agent");
	//     background Bash tasks are excluded. This is the load-bearing
	//     quiescence signal — it reports the true pending set across
	//     auto-resume boundaries, including a subagent revived under a fresh
	//     tool_use id (which name-based counting cannot see).
	//
	// This type does not terminate a turn and carries no Usage/StopReason.
	// Consumers that only care about turn completion should ignore it; subagent
	// tracking consumes it as the authoritative pending set.
	MessageBackgroundTasks MessageType = "background_tasks"

	// MessageContextWindow carries context window fill state emitted mid-turn.
	// Contains window capacity and current fill level.
	//
	// Sources:
	//   - ACP: produced by the usage_update notification (authoritative).
	//   - CLI: synthesized by the engine after mid-turn content messages
	//     (e.g., MessageText, MessageThinking) that have ContextUsedTokens
	//     populated. The content message retains its original Usage for
	//     backward compatibility; the synthesized MessageContextWindow
	//     carries only ContextUsedTokens and ContextSizeTokens. Consumers
	//     should not double-count — prefer MessageContextWindow for
	//     context fill monitoring.
	//
	// Only ContextSizeTokens and ContextUsedTokens are meaningful on this
	// message type. Other Usage fields (InputTokens, OutputTokens, CostUSD,
	// etc.) are zero and should be ignored — they belong on MessageResult.
	// Cost data is NOT included — CostUSD is authoritative only on
	// MessageResult to avoid double-counting.
	MessageContextWindow MessageType = "context_window"

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
	// When an assistant message carries multiple tool_use blocks, Tool holds
	// the LAST one (last-one-wins, preserved for backward compatibility). Use
	// Tools to see every tool_use block in the message.
	Tool *ToolCall `json:"tool,omitempty"`

	// Tools holds every tool_use block parsed from a single assistant message,
	// in wire order. Tool remains the last entry for back-compat. This exists
	// because a message may spawn several subagents at once (parallel fan-out):
	// last-one-wins would undercount them. Nil when the message carries no
	// tool_use blocks. On MessageBackgroundTasks messages, Tools instead
	// carries one entry per in-flight background task (ToolCall.ID = task id).
	Tools []*ToolCall `json:"tools,omitempty"`

	// ParentToolUseID is the id of the tool_use that owns this message, taken
	// from the backend's parent_tool_use_id. Non-empty means the message
	// originated inside a subagent sidechain (the value is the parent's spawn
	// tool_use id); empty means a parent-session (top-level) event. On a
	// MessageSubagentResult it is the completing subagent's correlation id —
	// the key subagent tracking removes from its pending set.
	ParentToolUseID string `json:"parent_tool_use_id,omitempty"`

	// Subagents reports outstanding subagent work at the time this message was
	// produced. Stamped on MessageResult and MessageSubagentResult, and only
	// when subagent tracking is enabled (cli.WithSubagentTools). Nil means the
	// backend does not track subagents — see SubagentStats for why nil differs
	// from Pending()==0.
	Subagents *SubagentStats `json:"subagents,omitempty"`

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

	// Init carries metadata from the agent's initialization handshake.
	// Set exclusively on MessageInit messages. Nil on all other types.
	// Only non-nil when at least one field contains meaningful data.
	Init *InitMeta `json:"init,omitempty"`

	// Process carries subprocess metadata from the engine.
	// Set on MessageInit messages by subprocess engines (CLI, ACP).
	// Nil on all other message types and for API-based engines.
	Process *ProcessMeta `json:"process,omitempty"`

	// IsError indicates a result with an agent-level error.
	// Set exclusively on MessageResult messages when the backend reports
	// is_error: true.
	//   - When true: Content contains the error description (e.g., "Prompt is too long").
	//   - When false (default): normal completion.
	IsError bool `json:"is_error,omitempty"`

	// Denials lists tool invocations that were denied during this turn.
	// Set exclusively on MessageResult messages when one or more tool calls
	// were blocked by permission controls (e.g., dontAsk mode, HITL rejection).
	// Nil means no denials occurred or the backend does not report them.
	//
	// Only populated for intentional deny decisions (operator/policy rejections).
	// Infrastructure errors (handler panics, protocol failures) are surfaced
	// via MessageError, not as denials.
	//
	// Attribution guarantee: exact when the turn completes normally.
	// After turn cancellation (Send returns ctx.Err), a stale permission
	// request from the cancelled turn may in rare cases be attributed to
	// the following turn — see PermissionHandler documentation for details.
	//
	// Sources:
	//   - Claude CLI: parsed from "permission_denials" array in result events.
	//     Attribution is always exact (agent reports per-turn summary).
	//   - ACP: accumulated when PermissionHandler returns false or no handler.
	//     Best-effort after cancellation (see above).
	//   - Codex/OpenCode: not populated (no structured denial reporting).
	Denials []PermissionDenial `json:"denials,omitempty"`

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

	// ID is the backend-assigned identifier of the tool_use content block
	// (e.g., Claude's "toolu_..." id). Empty when the backend does not report
	// a per-call id. Used to correlate a spawn with its later completion —
	// subagent tracking keys the pending set on this id (see SubagentStats).
	ID string `json:"id,omitempty"`

	// Input is the tool's input parameters as raw JSON.
	Input json.RawMessage `json:"input,omitempty"`

	// Output is the tool's result as raw JSON.
	Output json.RawMessage `json:"output,omitempty"`
}

// SubagentStats reports the parent turn's outstanding subagent work at the
// moment a message was produced. It is stamped on MessageResult and
// MessageSubagentResult when — and only when — the engine is configured to
// track subagents (cli.WithSubagentTools with a non-empty set). A nil
// Subagents field therefore means "this backend does not track subagents"
// (the default), NOT "zero subagents": consumers must treat nil and
// non-nil-with-Pending()==0 differently. This distinction is what lets a
// consumer safely wait ("linger") for background subagents to quiesce only
// on backends that can actually report quiescence.
//
// Per-backend matrix (mirrors the Denials field convention):
//   - Claude CLI: populated when cli.WithSubagentTools is set. The pending
//     set is driven authoritatively by the CLI's native background-task feed
//     (MessageBackgroundTasks), with name-based tool_use counting as a
//     fallback before the first native snapshot arrives.
//   - Codex/OpenCode/agy/ACP: never populated (nil) — they neither emit the
//     native feed nor the MessageSubagentResult demote, and crucible does not
//     opt them into tracking.
type SubagentStats struct {
	// Started is the cumulative count of distinct subagents observed to start
	// during the current subprocess (reset on MessageInit).
	Started int `json:"started"`

	// Finished is the cumulative count of subagents observed to complete.
	Finished int `json:"finished"`

	// PendingIDs is a snapshot of the currently-outstanding subagent ids,
	// capped at 32 entries (memory bound; the full count is Started-Finished).
	PendingIDs []string `json:"pending_ids,omitempty"`
}

// Pending returns the number of subagents that started but have not yet been
// observed to finish. Zero means quiescent (all tracked subagents done);
// a positive value means the parent turn ended while background work is still
// in flight. Nil-safe: a nil receiver reports 0.
func (s *SubagentStats) Pending() int {
	if s == nil {
		return 0
	}
	return s.Started - s.Finished
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
	// InputTokens is the number of input tokens consumed by the model for
	// this interaction. On per-call messages (e.g., assistant events) this
	// reflects a single API call; on MessageResult it may be cumulative
	// across all calls in the turn. For derived context-fill estimation,
	// see ContextUsedTokens.
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

	// ContextSizeTokens is the total context window capacity in tokens.
	// Omitted when zero (0 means not reported by this backend).
	// Set on MessageContextWindow messages (ACP usage_update).
	ContextSizeTokens int `json:"context_size_tokens,omitempty"`

	// ContextUsedTokens is the current context window fill level in tokens.
	// Omitted when zero (0 means not reported by this backend, per omitempty).
	//
	// Sources:
	//   - MessageContextWindow: authoritative measurement from the agent
	//     protocol (ACP usage_update). When ContextUsedTokens is 0 (fresh
	//     session), the field is omitted in JSON per omitempty — consumers
	//     can infer context is being reported (but empty) when
	//     ContextSizeTokens > 0 on the same message.
	//   - MessageResult (CLI engine): derived estimate using the maximum of
	//     (InputTokens + CacheReadTokens + CacheWriteTokens) across all
	//     per-call usage events in the turn — representing peak context fill.
	//     Only set when the backend emits per-call usage on non-result
	//     messages (e.g., Claude CLI assistant events). Backends that report
	//     usage only on the result event (e.g., Codex, OpenCode) leave this
	//     field unset (0) because the CLI engine lacks trustworthy per-call
	//     data. Not set when the backend already provides an authoritative
	//     value.
	//   - Mid-turn CLI messages (e.g., MessageText, MessageThinking): per-call
	//     context fill for that specific API call (InputTokens + CacheReadTokens
	//     + CacheWriteTokens). Unlike MessageResult which carries the peak across
	//     all calls in the turn, mid-turn messages reflect the instantaneous fill
	//     of a single call. Only set when the backend hasn't already provided an
	//     authoritative value.
	ContextUsedTokens int `json:"context_used_tokens,omitempty"`
}

// InitMeta carries metadata from the agent's initialization handshake.
// Set exclusively on MessageInit messages. Nil on all other message types.
//
// Not all backends populate every field — AgentName and AgentVersion are
// currently only populated by ACP backends. Consumers should check
// individual fields rather than assuming non-nil InitMeta means all
// fields are present.
//
// Nil-guard contract: backends only set Init when at least one field is
// non-empty. A non-nil InitMeta always has meaningful data.
type InitMeta struct {
	// Model is the model identifier reported by the backend at session start.
	// Reflects the agent's reported model during handshake; may differ from
	// the active model if the caller overrides via Session.Model.
	// Empty means the backend did not report a model on init.
	// Sanitized: control chars rejected, truncated to 128 bytes at parse time.
	Model string `json:"model,omitempty"`

	// AgentName is the agent implementation's name (e.g., "opencode", "claude-code").
	// Empty means the backend did not report an agent name.
	// Currently populated by ACP backends only.
	// Sanitized: control chars rejected, truncated to 128 bytes at parse time.
	AgentName string `json:"agent_name,omitempty"`

	// AgentVersion is the agent implementation's version string.
	// Empty means the backend did not report a version.
	// Currently populated by ACP backends only.
	// Sanitized: control chars rejected, truncated to 128 bytes at parse time.
	AgentVersion string `json:"agent_version,omitempty"`
}

// ProcessMeta describes the OS subprocess backing a session.
// Set on MessageInit messages by subprocess engines. Nil for API-based
// engines and for all non-init message types.
//
// All fields are snapshot values captured at init time. For spawn-per-turn
// backends (e.g., OpenCode, Codex), PID reflects the first subprocess and
// may change between turns — callers should not treat PID as a stable
// session-lifetime identifier.
//
// Nil-guard contract: engines only set Process when PID > 0.
// A non-nil ProcessMeta always has meaningful data.
type ProcessMeta struct {
	// PID is the OS process identifier of the subprocess.
	// Per the nil-guard contract on ProcessMeta, this field is always > 0.
	PID int `json:"pid,omitempty"`

	// Binary is the resolved path to the subprocess executable.
	// Populated from exec.Cmd.Path (the result of exec.LookPath).
	// Empty means not available.
	Binary string `json:"binary,omitempty"`
}

// PermissionDenial records a tool invocation that was denied during a turn.
type PermissionDenial struct {
	// Tool is the denied tool name. Sanitized via errfmt.SanitizeCode
	// (control chars rejected, 128-byte cap).
	Tool string `json:"tool,omitempty"`

	// Reason is a human-readable explanation of why the tool was denied.
	// Sanitized via errfmt.Truncate (4096-byte cap).
	Reason string `json:"reason,omitempty"`
}
