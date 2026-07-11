package claude

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
	"github.com/dmora/agentrun/engine/internal/errfmt"
	"github.com/dmora/agentrun/engine/internal/stoputil"
)

// ParseLine parses a single line of Claude's stream-json output into a Message.
// Returns cli.ErrSkipLine for blank or whitespace-only lines.
func (b *Backend) ParseLine(line string) (agentrun.Message, error) {
	if strings.TrimSpace(line) == "" {
		return agentrun.Message{}, cli.ErrSkipLine
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return agentrun.Message{}, fmt.Errorf("claude: invalid JSON: %w", err)
	}

	typeStr := jsonutil.GetString(raw, "type")
	if typeStr == "" {
		return agentrun.Message{}, fmt.Errorf("claude: missing or empty type field")
	}

	var msg agentrun.Message
	msg.Raw = json.RawMessage(line)
	// Stamp the parent tool-use correlation id up front. Type-specific parsing
	// may override it (a task_notification uses its own tool_use_id as the
	// completing subagent's correlation id).
	msg.ParentToolUseID = parentToolUseID(raw)

	switch typeStr {
	case "system":
		parseSystemMessage(raw, &msg)
	case "init":
		msg.Type = agentrun.MessageInit
		msg.ResumeID = jsonutil.GetString(raw, "session_id")
		if model := errfmt.SanitizeCode(jsonutil.GetString(raw, "model")); model != "" {
			msg.Init = &agentrun.InitMeta{Model: model}
		}
	case "assistant":
		parseAssistantMessage(raw, &msg)
	case "tool":
		parseToolMessage(raw, &msg)
	case "result":
		parseResultMessage(raw, &msg)
	case "error":
		parseErrorMessage(raw, &msg)
	case "stream_event":
		// Two-level dispatch: stream_event wraps an inner event with its
		// own type discriminator. See parseStreamEvent for the inner dispatch.
		parseStreamEvent(raw, &msg)
	default:
		msg.Type = sanitizeUnknownType(typeStr)
	}

	// Subagent events (parent_tool_use_id != null) carry the subagent's
	// context window usage, not the parent's. Exclude their Usage to
	// prevent inflation of the parent's context fill tracking.
	// Raw JSON is preserved for consumers who need per-subagent usage.
	if isSubagentEvent(raw) {
		msg.Usage = nil
		// A subagent's terminal "result" line must not end the parent turn.
		// Demote it to MessageSubagentResult so drainOutput waits for the
		// parent's own result (see issue #57). Only the parent's result
		// (no parent_tool_use_id) reaches parseResultMessage as MessageResult.
		if msg.Type == agentrun.MessageResult {
			msg.Type = agentrun.MessageSubagentResult
			// Strip the result-only fields parseResultMessage populated: a
			// subagent's stop reason, error flag, and denials describe the
			// subagent, not the parent turn. Leaving StopReason set is the
			// worst offender — the engine's carry-forward would capture it
			// and apply it to the parent's real MessageResult (see PR #58).
			msg.StopReason = ""
			msg.IsError = false
			msg.Denials = nil
		}
	}

	return msg, nil
}

// parseSystemMessage handles "system" events, detecting the init,
// background_tasks_changed, and task_notification subtypes.
func parseSystemMessage(raw map[string]any, msg *agentrun.Message) {
	switch jsonutil.GetString(raw, "subtype") {
	case "init":
		msg.Type = agentrun.MessageInit
		msg.ResumeID = jsonutil.GetString(raw, "session_id")
		if model := errfmt.SanitizeCode(jsonutil.GetString(raw, "model")); model != "" {
			msg.Init = &agentrun.InitMeta{Model: model}
		}
	case "background_tasks_changed":
		parseBackgroundTasks(raw, msg)
	case "task_notification":
		parseTaskNotification(raw, msg)
	default:
		msg.Type = agentrun.MessageSystem
		msg.Content = jsonutil.GetString(raw, "message")
	}
}

// subagentTaskType is the Claude task_type that denotes a spawned subagent
// (the Task/Agent tool). Background Bash tasks report "local_bash" and are
// deliberately excluded from the subagent pending set — a station may leave a
// long-running server task running, which would never quiesce.
const subagentTaskType = "local_agent"

// terminalTaskStatuses are the task_notification statuses that mean a
// background task has finished (as opposed to a progress update).
var terminalTaskStatuses = map[string]struct{}{
	"completed": {},
	"failed":    {},
	"error":     {},
	"cancelled": {},
	"canceled":  {},
	"timeout":   {},
}

// parseBackgroundTasks maps a "background_tasks_changed" event to
// MessageBackgroundTasks, carrying one ToolCall per in-flight subagent task
// (ID = task id) in Tools. Non-subagent tasks (e.g., local_bash) are filtered
// out here so the generic tracker can treat the slice as the authoritative
// subagent pending set. Tools is always non-nil (empty means the set drained).
func parseBackgroundTasks(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageBackgroundTasks
	tasks, _ := raw["tasks"].([]any)
	out := make([]*agentrun.ToolCall, 0, len(tasks))
	for _, t := range tasks {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		if jsonutil.GetString(tm, "task_type") != subagentTaskType {
			continue
		}
		if id := jsonutil.GetString(tm, "task_id"); id != "" {
			out = append(out, &agentrun.ToolCall{ID: id})
		}
	}
	msg.Tools = out
}

// parseTaskNotification maps a terminal "task_notification" (a background
// subagent's completion, which never arrives as its own result line) to
// MessageSubagentResult, correlating it via the notification's tool_use_id.
// Non-terminal notifications stay plain system messages. task_notification
// carries no task_type, so subagent scoping is handled downstream by the
// tracker's idempotent remove (a non-subagent id was never in the set).
func parseTaskNotification(raw map[string]any, msg *agentrun.Message) {
	status := jsonutil.GetString(raw, "status")
	if _, terminal := terminalTaskStatuses[status]; !terminal {
		msg.Type = agentrun.MessageSystem
		msg.Content = jsonutil.GetString(raw, "message")
		return
	}
	msg.Type = agentrun.MessageSubagentResult
	msg.ParentToolUseID = jsonutil.GetString(raw, "tool_use_id")
	msg.Content = jsonutil.GetString(raw, "summary")
}

// parseAssistantMessage handles "assistant" events with text and optional tool_use.
func parseAssistantMessage(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageText

	// Try nested message.content array first (standard format).
	if message, ok := raw["message"].(map[string]any); ok {
		parseAssistantContent(message, msg)
		msg.Usage = extractTokenUsage(message)
	}

	// Fallback: flat "text" field.
	if msg.Content == "" {
		if text, ok := raw["text"].(string); ok {
			msg.Content = text
		}
	}

	// Fallback: flat "content" field.
	if msg.Content == "" {
		if content, ok := raw["content"].(string); ok {
			msg.Content = content
		}
	}
}

// parseAssistantContent iterates the content array inside an assistant message,
// concatenating text blocks, capturing thinking blocks, and capturing tool_use
// blocks. Every tool_use block is appended to msg.Tools (in wire order);
// msg.Tool holds the last one (last-one-wins, kept for backward compatibility).
// Capturing all of them matters for parallel fan-out — several subagents spawned
// in one assistant message would otherwise be undercounted.
//
// When the content array contains only thinking blocks (no text), the message
// type is set to MessageThinking. Otherwise it stays MessageText and thinking
// content is available in msg.Raw for consumers who need it.
func parseAssistantContent(message map[string]any, msg *agentrun.Message) {
	contentArr, ok := message["content"].([]any)
	if !ok {
		return
	}

	var text, thinking strings.Builder
	for _, c := range contentArr {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		ct, _ := cm["type"].(string)
		switch ct {
		case "thinking":
			if t, ok := cm["thinking"].(string); ok {
				thinking.WriteString(t)
			}
		case "tool_use":
			tc := extractToolCall(cm)
			msg.Tool = tc
			msg.Tools = append(msg.Tools, tc)
		default:
			// "text" and any other content block type with a "text" field.
			if t, ok := cm["text"].(string); ok {
				text.WriteString(t)
			}
		}
	}

	if text.Len() > 0 {
		msg.Content = text.String()
		return
	}
	// No text content — if we have thinking, emit as MessageThinking.
	if thinking.Len() > 0 {
		msg.Type = agentrun.MessageThinking
		msg.Content = thinking.String()
	}
}

// extractToolCall builds a ToolCall from a content block map. The block "id"
// (Claude's "toolu_..." tool_use id) is captured so a spawn can be correlated
// with its later completion — subagent tracking keys on it.
func extractToolCall(cm map[string]any) *agentrun.ToolCall {
	tool := &agentrun.ToolCall{
		Name: jsonutil.GetString(cm, "name"),
		ID:   jsonutil.GetString(cm, "id"),
	}
	if input, ok := cm["input"]; ok {
		if data, err := json.Marshal(input); err == nil {
			tool.Input = data
		}
	}
	return tool
}

// parseToolMessage handles "tool" events (completed tool execution results).
func parseToolMessage(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageToolResult
	tool := extractToolCall(raw)
	if output, ok := raw["output"]; ok {
		if data, err := json.Marshal(output); err == nil {
			tool.Output = data
		}
	}
	msg.Tool = tool
}

// parseResultMessage handles "result" events (turn completion with optional usage).
func parseResultMessage(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageResult
	if text, ok := raw["text"].(string); ok {
		msg.Content = text
	}
	// "result" field takes precedence over "text" when both are present.
	if result, ok := raw["result"].(string); ok {
		msg.Content = result
	}
	msg.Usage = extractTokenUsage(raw)
	// Extract stop_reason directly from result event (may be null/empty
	// in streaming mode, but populated in non-streaming or future CLI versions).
	if sr := jsonutil.GetString(raw, "stop_reason"); sr != "" {
		msg.StopReason = stoputil.Sanitize(sr)
	}
	msg.Denials = extractPermissionDenials(raw)
	msg.IsError, _ = raw["is_error"].(bool)
}

// parseErrorMessage handles "error" events.
func parseErrorMessage(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageError
	msg.ErrorCode = errfmt.SanitizeCode(jsonutil.GetString(raw, "code"))
	msg.Content = jsonutil.GetString(raw, "message")
	// Fallback: "error" field as string.
	if msg.Content == "" {
		msg.Content = jsonutil.GetString(raw, "error")
	}
	msg.Content = errfmt.Truncate(msg.Content)
}

// parseStreamEvent handles "stream_event" wrapper events from --include-partial-messages.
// Dispatches content_block_delta subtypes to delta message types; lifecycle events
// (message_start, content_block_start/stop, message_stop) become MessageSystem.
func parseStreamEvent(raw map[string]any, msg *agentrun.Message) {
	event, ok := raw["event"].(map[string]any)
	if !ok {
		msg.Type = agentrun.MessageSystem
		msg.Content = "stream_event: missing or invalid event field"
		return
	}

	eventType := jsonutil.GetString(event, "type")
	switch eventType {
	case "content_block_delta":
		parseContentBlockDelta(event, msg)
	case "message_delta":
		msg.Type = agentrun.MessageSystem
		msg.Content = "stream_event: message_delta"
		// Extract stop_reason from delta and set on this message.
		// The engine readLoop carries it forward to the next MessageResult.
		if delta, ok := event["delta"].(map[string]any); ok {
			if sr := jsonutil.GetString(delta, "stop_reason"); sr != "" {
				msg.StopReason = stoputil.Sanitize(sr)
			}
		}
	default:
		// message_start, content_block_start, content_block_stop,
		// message_stop — all lifecycle events.
		msg.Type = agentrun.MessageSystem
		msg.Content = "stream_event: " + eventType
	}
}

// parseContentBlockDelta extracts delta content from a content_block_delta event.
func parseContentBlockDelta(event map[string]any, msg *agentrun.Message) {
	delta, ok := event["delta"].(map[string]any)
	if !ok {
		msg.Type = agentrun.MessageSystem
		msg.Content = "content_block_delta: missing or invalid delta field"
		return
	}

	switch jsonutil.GetString(delta, "type") {
	case "text_delta":
		msg.Type = agentrun.MessageTextDelta
		msg.Content = jsonutil.GetString(delta, "text")
	case "input_json_delta":
		msg.Type = agentrun.MessageToolUseDelta
		msg.Content = jsonutil.GetString(delta, "partial_json")
	case "thinking_delta":
		msg.Type = agentrun.MessageThinkingDelta
		msg.Content = jsonutil.GetString(delta, "thinking")
	case "signature_delta":
		// Integrity verification — opaque data, not consumer-visible content.
		msg.Type = agentrun.MessageSystem
		msg.Content = jsonutil.GetString(delta, "signature")
	default:
		msg.Type = agentrun.MessageSystem
		msg.Content = "content_block_delta: unknown delta type: " + jsonutil.GetString(delta, "type")
	}
}

// extractPermissionDenials extracts the "permission_denials" array from a result
// event. Each entry is expected to have "tool" and "reason" string fields.
// Returns nil when the array is absent, empty, or contains no valid entries.
func extractPermissionDenials(raw map[string]any) []agentrun.PermissionDenial {
	arr, ok := raw["permission_denials"].([]any)
	if !ok || len(arr) == 0 {
		return nil
	}
	var denials []agentrun.PermissionDenial
	for _, item := range arr {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		tool := errfmt.SanitizeCode(jsonutil.GetString(entry, "tool"))
		reason := errfmt.Truncate(jsonutil.GetString(entry, "reason"))
		if tool == "" && reason == "" {
			continue
		}
		denials = append(denials, agentrun.PermissionDenial{
			Tool:   tool,
			Reason: reason,
		})
	}
	if len(denials) == 0 {
		return nil
	}
	return denials
}

// extractTokenUsage extracts token usage from a source map.
// Returns nil if no meaningful usage data is present (all fields zero).
//
// Token counts come from the "usage" sub-object. Cost comes from the source
// root (result events have total_cost_usd at top level, not inside usage).
func extractTokenUsage(source map[string]any) *agentrun.Usage {
	u := &agentrun.Usage{}

	// Token counts come from the "usage" sub-object (may be absent).
	if usage, ok := source["usage"].(map[string]any); ok {
		u.InputTokens = jsonutil.GetInt(usage, "input_tokens")
		u.OutputTokens = jsonutil.GetInt(usage, "output_tokens")
		u.CacheReadTokens = jsonutil.GetInt(usage, "cache_read_input_tokens")
		u.CacheWriteTokens = jsonutil.GetInt(usage, "cache_creation_input_tokens")
		u.ThinkingTokens = jsonutil.GetInt(usage, "thinking_tokens")
	}

	// Cost lives at the source level (result event), not inside usage.
	// Parsed independently so cost is captured even if "usage" is absent.
	cost := jsonutil.GetFloat(source, "total_cost_usd")
	if math.IsInf(cost, 0) || math.IsNaN(cost) || cost < 0 {
		cost = 0
	}
	u.CostUSD = cost

	// Return nil only when ALL fields are zero (nothing reported).
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 &&
		u.ThinkingTokens == 0 && u.CostUSD == 0 {
		return nil
	}
	return u
}

// isSubagentEvent returns true when the raw JSONL event has a non-null
// parent_tool_use_id, indicating it originated from a subagent (e.g., the
// Agent tool) rather than the parent session. Parent events have null or
// missing parent_tool_use_id.
func isSubagentEvent(raw map[string]any) bool {
	v, ok := raw["parent_tool_use_id"]
	return ok && v != nil
}

// parentToolUseID returns the event's parent_tool_use_id as a string, or ""
// when it is absent or null. This is the subagent-sidechain correlation id.
func parentToolUseID(raw map[string]any) string {
	v, ok := raw["parent_tool_use_id"]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// sanitizeUnknownType converts an unknown type string to a MessageType.
// Types that are too long or contain control characters are mapped to
// MessageSystem to prevent unbounded type values.
func sanitizeUnknownType(typeStr string) agentrun.MessageType {
	const maxTypeLen = 64
	if len(typeStr) > maxTypeLen {
		return agentrun.MessageSystem
	}
	for _, r := range typeStr {
		if unicode.IsControl(r) {
			return agentrun.MessageSystem
		}
	}
	return agentrun.MessageType(typeStr)
}
