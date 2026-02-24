package opencode

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
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
			msg.Content = sid
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
func parseToolUse(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageToolResult
	part := jsonutil.GetMap(raw, "part")
	if part == nil {
		msg.Tool = &agentrun.ToolCall{}
		return
	}

	tool := &agentrun.ToolCall{
		Name: jsonutil.GetString(part, "tool"),
	}

	tool.Input = marshalField(jsonutil.GetMap(part, "state"), "input")
	tool.Output = marshalField(jsonutil.GetMap(part, "state"), "output")
	msg.Tool = tool
}

// parseStepFinish handles "step_finish" events — turn completion with usage.
func parseStepFinish(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageResult
	msg.Usage = parseTokens(raw)
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

	code := jsonutil.GetString(errObj, "name")
	message := ""
	if data := jsonutil.GetMap(errObj, "data"); data != nil {
		message = jsonutil.GetString(data, "message")
	}
	// Fallback: error.message directly if data.message is empty.
	if message == "" {
		message = jsonutil.GetString(errObj, "message")
	}
	msg.Content = formatError(code, message)
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
// Path: raw.part.tokens.{input, output}
// Returns nil if no tokens map is present.
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
	return &agentrun.Usage{
		InputTokens:  input,
		OutputTokens: output,
	}
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

// maxErrorLen caps error content to prevent unbounded propagation.
const maxErrorLen = 4096

// formatError formats an error code and message pair.
// Content is capped at maxErrorLen bytes, truncated at a valid UTF-8 boundary.
func formatError(code, message string) string {
	var content string
	if code != "" {
		content = code + ": " + message
	} else {
		content = message
	}
	if len(content) > maxErrorLen {
		content = content[:maxErrorLen]
		for !utf8.ValidString(content) {
			content = content[:len(content)-1]
		}
		return content
	}
	return content
}
