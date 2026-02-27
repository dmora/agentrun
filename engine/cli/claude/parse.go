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

	switch typeStr {
	case "system":
		parseSystemMessage(raw, &msg)
	case "init":
		msg.Type = agentrun.MessageInit
		msg.ResumeID = jsonutil.GetString(raw, "session_id")
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

	return msg, nil
}

// parseSystemMessage handles "system" events, detecting init subtype.
func parseSystemMessage(raw map[string]any, msg *agentrun.Message) {
	subtype := jsonutil.GetString(raw, "subtype")
	if subtype == "init" {
		msg.Type = agentrun.MessageInit
		msg.ResumeID = jsonutil.GetString(raw, "session_id")
		return
	}
	msg.Type = agentrun.MessageSystem
	msg.Content = jsonutil.GetString(raw, "message")
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
// blocks (last one wins).
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
			msg.Tool = extractToolCall(cm)
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

// extractToolCall builds a ToolCall from a content block map.
func extractToolCall(cm map[string]any) *agentrun.ToolCall {
	tool := &agentrun.ToolCall{
		Name: jsonutil.GetString(cm, "name"),
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
}

// parseErrorMessage handles "error" events.
func parseErrorMessage(raw map[string]any, msg *agentrun.Message) {
	msg.Type = agentrun.MessageError
	code := jsonutil.GetString(raw, "code")
	message := jsonutil.GetString(raw, "message")
	// Fallback: "error" field as string.
	if message == "" {
		message = jsonutil.GetString(raw, "error")
	}
	if code != "" {
		msg.Content = code + ": " + message
	} else {
		msg.Content = message
	}
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
