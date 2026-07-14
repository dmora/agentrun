package opencode

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
	"github.com/dmora/agentrun/engine/internal/errfmt"
	"github.com/dmora/agentrun/engine/internal/stoputil"
)

// eventParser parses a raw JSON event into an agentrun.Message.
type eventParser func(raw map[string]any, msg *agentrun.Message)

// eventParsers dispatches OpenCode event types to their parser functions.
// Adding a new event type = one map entry + one function.
// step_start is handled inline because it needs Backend state (sessionID).
var eventParsers = map[string]eventParser{
	"text":        parseText,
	"tool_use":    parseToolUse,
	"step_finish": parseStepFinish,
	"reasoning":   parseReasoning,
	"error":       parseError,
}

// ParseLine parses a single nd-JSON output line from OpenCode into a Message.
// Returns cli.ErrSkipLine for blank or whitespace-only lines.
//
// OpenCode emits 6 event types: step_start, text, tool_use, step_finish,
// reasoning, error. All events include a top-level "timestamp" field
// (millisecond Unix epoch) and "sessionID".
func (b *Backend) ParseLine(line string) (agentrun.Message, error) {
	if strings.TrimSpace(line) == "" {
		return agentrun.Message{}, cli.ErrSkipLine
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return agentrun.Message{}, fmt.Errorf("opencode: invalid JSON: %w", err)
	}

	typeStr := jsonutil.GetString(raw, "type")
	if typeStr == "" {
		return agentrun.Message{}, fmt.Errorf("opencode: missing or empty type field")
	}

	var msg agentrun.Message
	msg.Raw = json.RawMessage(line)
	msg.Timestamp = parseTimestamp(raw)

	// step_start handled inline — needs Backend state for sessionID capture.
	if typeStr == "step_start" {
		b.parseStepStart(raw, &msg)
		return msg, nil
	}

	if parser, ok := eventParsers[typeStr]; ok {
		parser(raw, &msg)
		return msg, nil
	}

	// Unknown event type → MessageSystem (graceful, not error).
	msg.Type = agentrun.MessageSystem
	msg.Content = typeStr
	return msg, nil
}

// parseStepStart handles step_start events with session ID write-once logic.
// First step_start → MessageInit (ID captured). Subsequent → MessageSystem.
//
// Note: if the first step_start has an invalid session ID format, MessageInit
// is still emitted (to avoid blocking the engine) but the ID is not stored.
// A subsequent step_start with a valid ID will store it and emit a second
// MessageInit. Consumers should tolerate multiple MessageInit events.
func (b *Backend) parseStepStart(raw map[string]any, msg *agentrun.Message) {
	sid := jsonutil.GetString(raw, "sessionID")

	// Attempt write-once capture if ID looks valid.
	if sid != "" && validateSessionID(sid) == nil {
		if b.sessionID.CompareAndSwap(nil, &sid) {
			msg.Type = agentrun.MessageInit
			msg.ResumeID = sid
			return
		}
	}

	// First step_start that didn't CAS (invalid or empty sessionID) —
	// still emit MessageInit so the engine doesn't block waiting for init.
	// Session ID just won't be stored. Content stays empty.
	if b.sessionID.Load() == nil {
		msg.Type = agentrun.MessageInit
		return
	}

	// Subsequent step_start (ID already captured) → system message.
	msg.Type = agentrun.MessageSystem
	msg.Content = "step_start"
	if sid != "" {
		msg.Content = "step_start: " + sid
	}
}

// parseText handles "text" events — complete text blocks from the assistant.
func parseText(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageText
	if part := jsonutil.GetMap(raw, "part"); part != nil {
		msg.Content = jsonutil.GetString(part, "text")
	}
}

// parseToolUse handles "tool_use" events — always post-completion with both
// input and output. Mapped to MessageToolResult with both Input and Output
// populated on the ToolCall.
//
// A state.status of "error" has no "output" key (only "error"); in that case
// the error is surfaced on both Tool.Output (marshaled, like any other state
// field) and msg.Content, since Output alone leaves no failure signal
// anywhere on the Message.
func parseToolUse(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageToolResult
	part := jsonutil.GetMap(raw, "part")
	if part == nil {
		msg.Tool = &agentrun.ToolCall{}
		return
	}

	tool := &agentrun.ToolCall{
		Name: jsonutil.GetString(part, "tool"),
		ID:   jsonutil.GetString(part, "callID"),
	}

	state := jsonutil.GetMap(part, "state")
	tool.Input = marshalField(state, "input")
	if jsonutil.GetString(state, "status") == "error" {
		tool.Output = marshalField(state, "error")
		msg.Content = errfmt.Truncate(jsonutil.GetString(state, "error"))
	} else {
		tool.Output = marshalField(state, "output")
	}
	msg.Tool = tool
}

// stepFinishStopReasons maps OpenCode step_finish part.reason values to root
// StopReason constants. Reasons with no entry here pass through sanitized
// (stoputil), matching the Claude backend's raw-value fallback convention.
var stepFinishStopReasons = map[string]agentrun.StopReason{
	"stop":       agentrun.StopEndTurn,
	"tool-calls": agentrun.StopToolUse,
}

// parseStepFinish handles "step_finish" events. OpenCode emits one step_finish
// per agentic step, not one per turn: a step ending with part.reason
// "tool-calls" means the model will invoke more tools and the turn continues,
// while "stop" (or an absent reason, for fixtures that predate this field)
// means the turn is done. Only the latter maps to MessageResult — mapping
// every step_finish to MessageResult stops RunTurn's drain at the first tool
// call, truncating the rest of the turn.
//
// The intermediate "tool-calls" step deliberately does NOT carry Usage (see
// comment on that branch below) — only the terminal step_finish (mapped to
// MessageResult) reports tokens, consistent with OpenCode reporting usage
// only on the result event.
func parseStepFinish(raw map[string]any, msg *agentrun.Message) {
	part := jsonutil.GetMap(raw, "part")
	reason := jsonutil.GetString(part, "reason")

	if reason == "tool-calls" {
		msg.Type = agentrun.MessageSystem
		msg.Content = "step_finish: tool-calls"
		// Usage is intentionally left nil here, even though this event's
		// part.tokens carries real per-step counts. engine/cli's generic
		// mid-turn enrichment (applyContextFill/synthesizeContextWindow in
		// engine/cli/process.go) treats Usage on ANY non-MessageResult message
		// as a trustworthy per-call context-fill sample and would synthesize a
		// MessageContextWindow from it. That would silently contradict the
		// documented, deliberate invariant that backends reporting usage only
		// on the result event (Codex, OpenCode) leave ContextUsedTokens at 0
		// (see message.go's Usage.ContextUsedTokens doc and CLAUDE.md's
		// context-fill section) — the engine has no per-call signal it can
		// trust for these backends. The full per-step token counts remain
		// available to consumers via msg.Raw.
		return
	}

	msg.Type = agentrun.MessageResult
	msg.Usage = parseTokens(raw)
	if reason == "" {
		return
	}
	if sr, ok := stepFinishStopReasons[reason]; ok {
		msg.StopReason = sr
		return
	}
	msg.StopReason = stoputil.Sanitize(reason)
}

// parseReasoning handles "reasoning" events — thinking content from --thinking.
func parseReasoning(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageThinking
	if part := jsonutil.GetMap(raw, "part"); part != nil {
		msg.Content = jsonutil.GetString(part, "text")
	}
}

// parseError handles "error" events.
func parseError(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageError
	errObj := jsonutil.GetMap(raw, "error")
	if errObj == nil {
		msg.Content = "unknown error"
		return
	}

	// OpenCode wire format uses "name" for error code (not "code").
	msg.ErrorCode = errfmt.SanitizeCode(jsonutil.GetString(errObj, "name"))
	message := ""
	if data := jsonutil.GetMap(errObj, "data"); data != nil {
		message = jsonutil.GetString(data, "message")
	}
	// Fallback: error.message directly if data.message is empty.
	if message == "" {
		message = jsonutil.GetString(errObj, "message")
	}
	msg.Content = errfmt.Truncate(message)
}

// parseTimestamp extracts a millisecond Unix timestamp from the "timestamp" field.
// Returns time.Now() if the field is missing or invalid.
func parseTimestamp(raw map[string]any) time.Time {
	ts := jsonutil.GetFloat(raw, "timestamp")
	if ts > 0 {
		return time.UnixMilli(int64(ts))
	}
	return time.Now()
}

// parseTokens extracts token usage from a step_finish event.
// Path: raw.part.tokens.{input, output, reasoning, cache.{read, write}},
// raw.part.cost. Returns nil if no input/output tokens are present.
func parseTokens(raw map[string]any) *agentrun.Usage {
	part := jsonutil.GetMap(raw, "part")
	if part == nil {
		return nil
	}
	tokens := jsonutil.GetMap(part, "tokens")
	if tokens == nil {
		return nil
	}

	input := jsonutil.GetInt(tokens, "input")
	output := jsonutil.GetInt(tokens, "output")
	if input == 0 && output == 0 {
		return nil
	}

	// Sanitize cost the same way Claude does: NaN/Inf/negative collapse to 0.
	cost := jsonutil.GetFloat(part, "cost")
	if math.IsInf(cost, 0) || math.IsNaN(cost) || cost < 0 {
		cost = 0
	}

	usage := &agentrun.Usage{
		InputTokens:    input,
		OutputTokens:   output,
		ThinkingTokens: jsonutil.GetInt(tokens, "reasoning"),
		CostUSD:        cost,
	}
	if cache := jsonutil.GetMap(tokens, "cache"); cache != nil {
		usage.CacheReadTokens = jsonutil.GetInt(cache, "read")
		usage.CacheWriteTokens = jsonutil.GetInt(cache, "write")
	}
	return usage
}

// marshalField marshals m[key] to json.RawMessage if present, else returns nil.
// On marshal failure, returns a diagnostic JSON string rather than nil to
// avoid silent data loss.
func marshalField(m map[string]any, key string) json.RawMessage {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`"[marshal error: %v]"`, err))
	}
	return data
}
