// update.go maps ACP session/update notifications to agentrun.Message values.
//
// ACP session/update notifications arrive as a two-level envelope:
//
//	outer: {"sessionId":"...", "update": <inner>}
//	inner: {"sessionUpdate":"agent_message_chunk", "content":{...}}
//
// makeUpdateHandler (in process.go) unpacks the outer sessionNotification,
// then calls parseSessionUpdate(inner) which dispatches on the "sessionUpdate"
// discriminator field via the updateParsers map.
//
// Adding a new update type = one map entry + one function.
package acp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/internal/errfmt"
)

// ErrCodeToolCallFailed is the ErrorCode for failed tool calls.
// Library-defined (not from ACP wire format) — the ACP protocol has no
// structured error code on tool_call_update failures.
const ErrCodeToolCallFailed = "tool_call_failed"

// updateParser converts a raw session update into a Message.
// Returns nil to indicate the update should be silently consumed (e.g. usage_update).
type updateParser func(update json.RawMessage) *agentrun.Message

// updateParsers dispatches ACP sessionUpdate discriminator values to their parser functions.
var updateParsers = map[string]updateParser{
	"agent_message_chunk":       contentChunkParser(agentrun.MessageTextDelta),
	"agent_thought_chunk":       contentChunkParser(agentrun.MessageThinkingDelta),
	"user_message_chunk":        contentChunkParser(agentrun.MessageSystem),
	"tool_call":                 parseToolCall,
	"tool_call_update":          parseToolCallUpdate,
	"plan":                      parsePlan,
	"current_mode_update":       parseCurrentModeUpdate,
	"config_option_update":      parseConfigOptionUpdate,
	"session_info_update":       parseSessionInfoUpdate,
	"usage_update":              parseUsageUpdate,
	"available_commands_update": parseAvailableCommandsUpdate,
}

// parseSessionUpdate maps an ACP session/update inner payload to an agentrun.Message.
// Returns nil for updates that should be silently consumed (usage_update).
// Unknown types produce a MessageSystem with the sessionUpdate value as content.
func parseSessionUpdate(update json.RawMessage) *agentrun.Message {
	if len(update) == 0 {
		msg := agentrun.Message{
			Type:      agentrun.MessageSystem,
			Content:   "unknown",
			Timestamp: time.Now(),
		}
		return &msg
	}

	// Extract the discriminator.
	var header sessionUpdateHeader
	if err := json.Unmarshal(update, &header); err != nil {
		msg := agentrun.Message{
			Type:      agentrun.MessageError,
			Content:   errfmt.Truncate(fmt.Sprintf("acp: unmarshal session update header: %v", err)),
			Timestamp: time.Now(),
		}
		return &msg
	}

	if header.SessionUpdate == "" {
		msg := agentrun.Message{
			Type:      agentrun.MessageSystem,
			Content:   "unknown",
			Timestamp: time.Now(),
		}
		return &msg
	}

	if parser, ok := updateParsers[header.SessionUpdate]; ok {
		m := parser(update)
		if m == nil {
			return nil // silent consumption (e.g. usage_update)
		}
		if m.Timestamp.IsZero() {
			m.Timestamp = time.Now()
		}
		return m
	}

	// Unknown type → MessageSystem with the discriminator value.
	msg := agentrun.Message{
		Type:      agentrun.MessageSystem,
		Content:   header.SessionUpdate,
		Timestamp: time.Now(),
	}
	return &msg
}

// unmarshalError produces a MessageError for a failed unmarshal in a parser.
func unmarshalError(updateType string, err error) *agentrun.Message {
	msg := agentrun.Message{
		Type:      agentrun.MessageError,
		Content:   errfmt.Truncate(fmt.Sprintf("acp: unmarshal %s: %v", updateType, err)),
		Timestamp: time.Now(),
	}
	return &msg
}

// --- Content chunks (agent_message_chunk, agent_thought_chunk, user_message_chunk) ---

// contentChunkParser returns an updateParser that extracts content.text from a ContentChunk.
func contentChunkParser(mt agentrun.MessageType) updateParser {
	return func(update json.RawMessage) *agentrun.Message {
		var d struct {
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(update, &d); err != nil {
			return unmarshalError("content_chunk", err)
		}
		msg := agentrun.Message{
			Type:    mt,
			Content: d.Content.Text,
		}
		return &msg
	}
}

// --- Tool events ---

func parseToolCall(update json.RawMessage) *agentrun.Message {
	var d toolCallUpdate
	if err := json.Unmarshal(update, &d); err != nil {
		return unmarshalError("tool_call", err)
	}
	msg := agentrun.Message{
		Type: agentrun.MessageToolUse,
		Tool: &agentrun.ToolCall{
			Name:  d.Title,
			Input: d.RawInput,
		},
	}
	return &msg
}

func parseToolCallUpdate(update json.RawMessage) *agentrun.Message {
	var d toolCallUpdate
	if err := json.Unmarshal(update, &d); err != nil {
		return unmarshalError("tool_call_update", err)
	}

	switch d.Status {
	case "completed":
		output := extractToolOutput(d)
		msg := agentrun.Message{
			Type: agentrun.MessageToolResult,
			Tool: &agentrun.ToolCall{
				Name:   d.Title,
				Output: output,
			},
		}
		return &msg

	case "failed":
		msg := agentrun.Message{
			Type:      agentrun.MessageError,
			ErrorCode: ErrCodeToolCallFailed,
			Content:   errfmt.Truncate(fmt.Sprintf("tool_call failed: %s", d.Title)),
		}
		return &msg

	default: // in_progress, pending
		msg := agentrun.Message{
			Type:    agentrun.MessageSystem,
			Content: fmt.Sprintf("tool_call_update: %s (%s)", d.Title, d.Status),
		}
		return &msg
	}
}

// extractToolOutput gets the output from a completed tool call,
// preferring structured content text over rawOutput.
// Falls through to rawOutput if content is absent, unparseable, or empty.
func extractToolOutput(d toolCallUpdate) json.RawMessage {
	if text := extractContentText(d.Content); text != "" {
		b, _ := json.Marshal(text) // json.Marshal(string) cannot fail
		return b
	}
	if len(d.RawOutput) > 0 {
		return d.RawOutput
	}
	return nil
}

// extractContentText parses the ACP content block array and returns the
// first text value, or "" if the array is absent/empty/unparseable.
func extractContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []struct {
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil || len(blocks) == 0 {
		return ""
	}
	return blocks[0].Content.Text
}

// --- Plan ---

func parsePlan(update json.RawMessage) *agentrun.Message {
	var d struct {
		Entries []struct {
			Content  string `json:"content"`
			Priority string `json:"priority"`
			Status   string `json:"status"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(update, &d); err != nil {
		return unmarshalError("plan", err)
	}
	var b strings.Builder
	for i, e := range d.Entries {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Content)
	}
	msg := agentrun.Message{
		Type:    agentrun.MessageText,
		Content: b.String(),
	}
	return &msg
}

// --- Status/metadata events ---

func parseCurrentModeUpdate(update json.RawMessage) *agentrun.Message {
	var d struct {
		CurrentModeID string `json:"currentModeId"`
	}
	if err := json.Unmarshal(update, &d); err != nil {
		return unmarshalError("current_mode_update", err)
	}
	msg := agentrun.Message{
		Type:    agentrun.MessageSystem,
		Content: "mode:" + d.CurrentModeID,
	}
	return &msg
}

func parseConfigOptionUpdate(_ json.RawMessage) *agentrun.Message {
	msg := agentrun.Message{
		Type:    agentrun.MessageSystem,
		Content: "config_option_update",
	}
	return &msg
}

func parseSessionInfoUpdate(update json.RawMessage) *agentrun.Message {
	var d struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(update, &d); err != nil {
		return unmarshalError("session_info_update", err)
	}
	msg := agentrun.Message{
		Type:    agentrun.MessageSystem,
		Content: "session_info:" + d.Title,
	}
	return &msg
}

// parseUsageUpdate silently consumes incremental context-window usage notifications.
//
// Per-turn token usage (for cost tracking) is surfaced via handlePromptResult
// (promptResult.Usage → Message.Usage on MessageResult). This function handles
// a different signal: context window fill level (size/used), which is not yet
// surfaced. Defer accumulation until an orchestrator use case requires it.
//
// See also: handlePromptResult in process.go (authoritative turn-level usage).
func parseUsageUpdate(_ json.RawMessage) *agentrun.Message {
	return nil
}

func parseAvailableCommandsUpdate(_ json.RawMessage) *agentrun.Message {
	msg := agentrun.Message{
		Type:    agentrun.MessageSystem,
		Content: "available_commands_update",
	}
	return &msg
}
