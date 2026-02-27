package codex

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
	"github.com/dmora/agentrun/engine/internal/errfmt"
)

// validUUID matches UUID format (any version, case-insensitive).
var validUUID = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
)

// isUUID reports whether s is a valid UUID string.
func isUUID(s string) bool {
	return validUUID.MatchString(s)
}

// eventParser parses a raw JSON event into an agentrun.Message.
type eventParser func(raw map[string]any, msg *agentrun.Message)

// eventParsers dispatches Codex event types to their parser functions.
// thread.started is handled inline (needs Backend state for threadID CAS).
// turn.started and item.started produce no message (ErrSkipLine).
var eventParsers = map[string]eventParser{
	"item.completed": parseItemCompleted,
	"turn.completed": parseTurnCompleted,
	"turn.failed":    parseTurnFailed,
	"error":          parseTopLevelError,
}

// itemParser parses item content from an item.completed event.
type itemParser func(item map[string]any, msg *agentrun.Message)

// itemParsers dispatches item types within item.completed events.
var itemParsers = map[string]itemParser{
	"agent_message":     parseAgentMessage,
	"reasoning":         parseReasoning,
	"command_execution": parseCommandExecution,
	"error":             parseItemError,
	"file_changes":      parseGenericTool("file_changes"),
	"web_search":        parseGenericTool("web_search"),
	"mcp_tool_call":     parseMCPToolCall,
}

// ParseLine parses a single JSONL output line from codex exec into a Message.
// Returns cli.ErrSkipLine for blank lines and no-op events (turn.started, item.started).
func (b *Backend) ParseLine(line string) (agentrun.Message, error) {
	if strings.TrimSpace(line) == "" {
		return agentrun.Message{}, cli.ErrSkipLine
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return agentrun.Message{}, fmt.Errorf("codex: invalid JSON: %w", err)
	}

	typeStr := jsonutil.GetString(raw, "type")
	if typeStr == "" {
		return agentrun.Message{}, fmt.Errorf("codex: missing or empty type field")
	}

	var msg agentrun.Message
	msg.Raw = json.RawMessage(line)
	msg.Timestamp = time.Now()

	// thread.started — inline (needs Backend state for atomic CAS).
	if typeStr == "thread.started" {
		b.parseThreadStarted(raw, &msg)
		return msg, nil
	}

	// No-op events.
	if typeStr == "turn.started" || typeStr == "item.started" {
		return agentrun.Message{}, cli.ErrSkipLine
	}

	if parser, ok := eventParsers[typeStr]; ok {
		parser(raw, &msg)
		return msg, nil
	}

	// Unknown event type → MessageSystem (graceful).
	msg.Type = agentrun.MessageSystem
	msg.Content = typeStr
	return msg, nil
}

// parseThreadStarted handles thread.started with thread ID write-once logic.
// First thread.started with valid UUID → MessageInit with ResumeID stored.
// First thread.started with non-UUID → MessageInit (sentinel stored, no ResumeID).
// Subsequent → MessageSystem.
func (b *Backend) parseThreadStarted(raw map[string]any, msg *agentrun.Message) {
	tid := jsonutil.GetString(raw, "thread_id")

	// Attempt write-once capture if ID is a valid UUID.
	// CAS against nil (first event) or sentinel (non-UUID came first).
	if tid != "" && isUUID(tid) {
		if b.threadID.CompareAndSwap(nil, &tid) ||
			b.threadID.CompareAndSwap(&noUUIDSentinel, &tid) {
			msg.Type = agentrun.MessageInit
			msg.ResumeID = tid
			return
		}
	}

	// First thread.started with non-UUID/empty ID — store sentinel so
	// subsequent events correctly fall through to MessageSystem.
	if b.threadID.CompareAndSwap(nil, &noUUIDSentinel) {
		msg.Type = agentrun.MessageInit
		return
	}

	// Subsequent thread.started → system message.
	msg.Type = agentrun.MessageSystem
	msg.Content = "thread.started"
	if tid != "" {
		msg.Content = "thread.started: " + tid
	}
}

// parseItemCompleted delegates to inner itemParsers based on item.type.
func parseItemCompleted(raw map[string]any, msg *agentrun.Message) {
	item := jsonutil.GetMap(raw, "item")
	if item == nil {
		msg.Type = agentrun.MessageSystem
		msg.Content = "item.completed: missing item"
		return
	}

	itemType := jsonutil.GetString(item, "type")
	if parser, ok := itemParsers[itemType]; ok {
		parser(item, msg)
		return
	}

	// Unknown item type → system message.
	msg.Type = agentrun.MessageSystem
	msg.Content = "item.completed/" + itemType
}

// parseAgentMessage handles item.completed/agent_message → MessageText.
func parseAgentMessage(item map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageText
	msg.Content = jsonutil.GetString(item, "text")
}

// parseReasoning handles item.completed/reasoning → MessageThinking.
func parseReasoning(item map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageThinking
	msg.Content = jsonutil.GetString(item, "text")
}

// parseCommandExecution handles item.completed/command_execution → MessageToolResult.
// Tool.Name = "command_execution", Tool.Input = command string, Tool.Output = full marshaled item.
func parseCommandExecution(item map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageToolResult
	msg.Tool = &agentrun.ToolCall{
		Name:   "command_execution",
		Input:  marshalString(jsonutil.GetString(item, "command")),
		Output: marshalItem(item),
	}
}

// parseItemError handles item.completed/error → MessageError.
func parseItemError(item map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageError
	msg.ErrorCode = errfmt.SanitizeCode(jsonutil.GetString(item, "code"))
	message := jsonutil.GetString(item, "message")
	if message == "" {
		message = jsonutil.GetString(item, "text")
	}
	if message == "" {
		message = "unknown error"
	}
	msg.Content = errfmt.Truncate(message)
}

// parseGenericTool returns an itemParser that marshals the full item as Tool.Output.
func parseGenericTool(name string) itemParser {
	return func(item map[string]any, msg *agentrun.Message) {
		msg.Type = agentrun.MessageToolResult
		msg.Tool = &agentrun.ToolCall{
			Name:   name,
			Output: marshalItem(item),
		}
	}
}

// parseMCPToolCall handles item.completed/mcp_tool_call → MessageToolResult.
// Extracts tool name from item; marshals full item as Output.
func parseMCPToolCall(item map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageToolResult
	name := jsonutil.GetString(item, "name")
	if name == "" {
		name = jsonutil.GetString(item, "tool_name")
	}
	if name == "" {
		name = "mcp_tool_call"
	}
	msg.Tool = &agentrun.ToolCall{
		Name:   name,
		Output: marshalItem(item),
	}
}

// parseTurnCompleted handles turn.completed → MessageResult with usage.
func parseTurnCompleted(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageResult
	msg.Usage = parseUsage(raw)
}

// parseTurnFailed handles turn.failed → MessageError.
func parseTurnFailed(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageError
	errObj := jsonutil.GetMap(raw, "error")
	if errObj == nil {
		msg.Content = "turn failed"
		return
	}
	msg.ErrorCode = errfmt.SanitizeCode(jsonutil.GetString(errObj, "code"))
	message := jsonutil.GetString(errObj, "message")
	if message == "" {
		message = "turn failed"
	}
	msg.Content = errfmt.Truncate(message)
}

// parseTopLevelError handles top-level "error" events.
func parseTopLevelError(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageError
	msg.ErrorCode = errfmt.SanitizeCode(jsonutil.GetString(raw, "code"))
	message := jsonutil.GetString(raw, "message")
	if message == "" {
		message = "unknown error"
	}
	msg.Content = errfmt.Truncate(message)
}

// parseUsage extracts token usage from turn.completed events.
// Path: raw.usage.{input_tokens, cached_input_tokens, output_tokens}
func parseUsage(raw map[string]any) *agentrun.Usage {
	usage := jsonutil.GetMap(raw, "usage")
	if usage == nil {
		return nil
	}

	u := &agentrun.Usage{
		InputTokens:     jsonutil.GetInt(usage, "input_tokens"),
		OutputTokens:    jsonutil.GetInt(usage, "output_tokens"),
		CacheReadTokens: jsonutil.GetInt(usage, "cached_input_tokens"),
	}
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 {
		return nil
	}
	return u
}

// marshalString converts a string to json.RawMessage.
// On marshal failure, returns a diagnostic JSON string rather than nil
// to indicate that Tool.Input existed but couldn't be serialized.
func marshalString(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`"[marshal error: %v]"`, err))
	}
	return data
}

// marshalItem marshals a map to json.RawMessage for Tool.Output.
func marshalItem(item map[string]any) json.RawMessage {
	if item == nil {
		return nil
	}
	data, err := json.Marshal(item)
	if err != nil {
		return json.RawMessage(fmt.Sprintf(`"[marshal error: %v]"`, err))
	}
	return data
}
