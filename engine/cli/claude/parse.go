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
		msg.ResumeID, msg.Init = parseInitFields(raw)
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
		msg.Content = rateLimitSummary(typeStr, raw)
	}

	// Subagent events (parent_tool_use_id != null) carry the subagent's
	// context window usage, not the parent's. Exclude their Usage to
	// prevent inflation of the parent's context fill tracking.
	// Raw JSON is preserved for consumers who need per-subagent usage.
	if isSubagentEvent(raw) {
		msg.Usage = nil
		// A subagent's terminal "result" line must not end the parent turn.
		// Demote it to MessageTaskResult so drainOutput waits for the
		// parent's own result (see issue #57). Only the parent's result
		// (no parent_tool_use_id) reaches parseResultMessage as MessageResult.
		if msg.Type == agentrun.MessageResult {
			msg.Type = agentrun.MessageTaskResult
			// Strip the result-only fields parseResultMessage populated: a
			// subagent's stop reason, error flag, and denials describe the
			// subagent, not the parent turn. Leaving StopReason set is the
			// worst offender — the engine's carry-forward would capture it
			// and apply it to the parent's real MessageResult (see PR #58).
			msg.StopReason = ""
			msg.IsError = false
			msg.Denials = nil
			// This line came from within the subagent's own sidechain, so
			// unlike a task_notification (ADR-4) the kind is never in
			// question here: provably BackgroundSubagent.
			msg.Tasks = []agentrun.BackgroundTask{{
				ID:   errfmt.SanitizeCode(msg.ParentToolUseID),
				Kind: agentrun.BackgroundSubagent,
			}}
		}
	}

	return msg, nil
}

// parseInitFields extracts the ResumeID and InitMeta shared by both init
// shapes (bare "init" and "system"/"init"). model and claude_code_version
// are sanitized identically to any other identifier field.
func parseInitFields(raw map[string]any) (resumeID string, init *agentrun.InitMeta) {
	resumeID = jsonutil.GetString(raw, "session_id")
	model := errfmt.SanitizeCode(jsonutil.GetString(raw, "model"))
	version := errfmt.SanitizeCode(jsonutil.GetString(raw, "claude_code_version"))
	if model != "" || version != "" {
		init = &agentrun.InitMeta{Model: model, AgentVersion: version}
	}
	return resumeID, init
}

// parseSystemMessage handles "system" events, detecting the init,
// background_tasks_changed, and task_notification subtypes.
func parseSystemMessage(raw map[string]any, msg *agentrun.Message) {
	subtype := jsonutil.GetString(raw, "subtype")
	switch subtype {
	case "init":
		msg.Type = agentrun.MessageInit
		msg.ResumeID, msg.Init = parseInitFields(raw)
	case "background_tasks_changed":
		parseBackgroundTasks(raw, msg)
	case "task_notification":
		parseTaskNotification(raw, msg)
	default:
		msg.Type = agentrun.MessageSystem
		msg.Content = jsonutil.GetString(raw, "message")
		if msg.Content == "" {
			msg.Content = systemSubtypeSummary(subtype, raw)
		}
	}
}

// systemSubtypeSummary synthesizes Content for system subtypes that carry no
// "message" key but do carry other structured, decision-relevant fields:
// hook lifecycle events and extended-thinking token estimates. task_started
// and task_updated are intentionally excluded (tracked separately, see issue
// #61 D2) and fall through with empty Content, unchanged from prior behavior.
func systemSubtypeSummary(subtype string, raw map[string]any) string {
	switch subtype {
	case "hook_started":
		return "hook_started: " + jsonutil.GetString(raw, "hook_name") +
			" (" + jsonutil.GetString(raw, "hook_event") + ")"
	case "hook_progress":
		return errfmt.Truncate("hook_progress: " + jsonutil.GetString(raw, "hook_name") +
			" stdout=" + jsonutil.GetString(raw, "stdout") +
			" stderr=" + jsonutil.GetString(raw, "stderr"))
	case "hook_response":
		return fmt.Sprintf("hook_response: %s outcome=%s exit_code=%d",
			jsonutil.GetString(raw, "hook_name"),
			jsonutil.GetString(raw, "outcome"),
			jsonutil.GetInt(raw, "exit_code"))
	case "thinking_tokens":
		return fmt.Sprintf("thinking_tokens: estimated=%d", jsonutil.GetInt(raw, "estimated_tokens"))
	default:
		return ""
	}
}

// subagentTaskType and shellTaskType are the Claude task_type values with a
// direct BackgroundKind correspondence (ADR-1): local_agent is a spawned
// subagent (the Task/Agent tool), local_bash is a backgrounded shell command
// (run_in_background). Any other task_type passes through sanitized under
// its own tag — see backgroundTaskKind.
const (
	subagentTaskType = "local_agent"
	shellTaskType    = "local_bash"
)

// terminalTaskStatuses are the task_notification statuses that mean a
// background task has finished (as opposed to a progress update).
var terminalTaskStatuses = map[string]struct{}{
	"completed": {},
	"failed":    {},
	"error":     {},
	"cancelled": {},
	"canceled":  {},
	"timeout":   {},
	"stopped":   {},
}

// backgroundTaskKind maps a background_tasks_changed entry's task_type to a
// BackgroundKind (ADR-1). local_agent and local_bash have a direct
// correspondence; any other value (including empty — a future/unrecognized
// CLI task type) passes through sanitized under its own tag. There is no
// identity-erasing "other" bucket, so a new backend task type never requires
// a change here.
func backgroundTaskKind(taskType string) agentrun.BackgroundKind {
	switch taskType {
	case subagentTaskType:
		return agentrun.BackgroundSubagent
	case shellTaskType:
		return agentrun.BackgroundShell
	default:
		return agentrun.BackgroundKind(errfmt.SanitizeCode(taskType))
	}
}

// parseBackgroundTasks maps a "background_tasks_changed" event to
// MessageBackgroundTasks. Tasks (ADR-3) carries one kind-tagged
// BackgroundTask per wire entry, unconditionally — no task_type is filtered
// out here, so a bash-only snapshot is no longer indistinguishable from "no
// work" (the wire-level bug this fixes). Tasks is always non-nil (empty
// means the set drained).
//
// Tools is left unset — the Tools-as-pending-set overload this message type
// used to carry is gone; cli.backgroundTracker (and any other consumer)
// reads Tasks directly and filters by kind itself.
func parseBackgroundTasks(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageBackgroundTasks
	tasks, _ := raw["tasks"].([]any)
	entries := make([]agentrun.BackgroundTask, 0, len(tasks))
	for _, t := range tasks {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		id := errfmt.SanitizeCode(jsonutil.GetString(tm, "task_id"))
		if id == "" {
			// An entry without an id cannot be correlated or removed later —
			// skip it rather than emit a dead payload entry.
			continue
		}
		entries = append(entries, agentrun.BackgroundTask{
			ID:          id,
			Kind:        backgroundTaskKind(jsonutil.GetString(tm, "task_type")),
			Description: errfmt.Truncate(jsonutil.GetString(tm, "description")),
		})
	}
	msg.Tasks = entries
}

// parseTaskNotification maps a terminal "task_notification" (a background
// task's completion, which never arrives as its own result line) to
// MessageTaskResult, correlating it via the notification's tool_use_id.
// Non-terminal notifications stay plain system messages. task_notification
// carries no task_type, so the completing task's kind is unknowable here;
// downstream tracking scopes correctly anyway via the tracker's idempotent
// remove (an id it never added is simply not in its set).
func parseTaskNotification(raw map[string]any, msg *agentrun.Message) {
	status := jsonutil.GetString(raw, "status")
	if _, terminal := terminalTaskStatuses[status]; !terminal {
		msg.Type = agentrun.MessageSystem
		msg.Content = jsonutil.GetString(raw, "message")
		return
	}
	msg.Type = agentrun.MessageTaskResult
	msg.ParentToolUseID = jsonutil.GetString(raw, "tool_use_id")
	msg.Content = jsonutil.GetString(raw, "summary")
	// Tasks correlates by task_id (the snapshot's id-space, not tool_use_id)
	// with Kind left empty: a terminal task_notification carries no task_type,
	// so the completing task's kind is genuinely unknowable statelessly here
	// (a finished shell task and a finished subagent are wire-identical) —
	// see MessageTaskResult and ADR-4.
	msg.Tasks = []agentrun.BackgroundTask{{ID: errfmt.SanitizeCode(jsonutil.GetString(raw, "task_id"))}}
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

// rateLimitSummary synthesizes Content for a bare top-level "rate_limit_event"
// line, which carries no "message" key of its own — the decision-relevant
// data lives in its rate_limit_info object instead. Empty for any other
// typeStr, or when rate_limit_info is absent or malformed.
func rateLimitSummary(typeStr string, raw map[string]any) string {
	if typeStr != "rate_limit_event" {
		return ""
	}
	info, ok := raw["rate_limit_info"].(map[string]any)
	if !ok {
		return ""
	}
	status := jsonutil.GetString(info, "status")
	kind := jsonutil.GetString(info, "rateLimitType")
	if status == "" && kind == "" {
		return ""
	}
	return "rate_limit_event: status=" + status + " type=" + kind
}
