package agentrun

import "strings"

// TurnSummary accumulates turn-level data from individual messages.
// Not concurrent-safe — designed for single-goroutine use (matching
// the typical RunTurn handler pattern).
//
// Zero value is ready to use. After feeding messages via Add, fields
// contain the accumulated state of the turn.
type TurnSummary struct {
	// TextBlocks contains ordered MessageText content blocks.
	// Each MessageText message appends one entry — no concatenation.
	// Consumers who want a single string can strings.Join themselves.
	TextBlocks []string

	// ThinkingBlocks contains ordered MessageThinking content blocks.
	// Same preservation semantics as TextBlocks.
	ThinkingBlocks []string

	// ToolCalls contains tool invocations observed during the turn.
	// Captured from every Tools entry when present (a single message may
	// carry several tool_use blocks — parallel fan-out), falling back to
	// the single Tool field (MessageToolUse, or Claude CLI embedding a
	// tool call in MessageText/MessageThinking). MessageBackgroundTasks
	// is excluded: its Tools are in-flight task snapshots, not invocations.
	ToolCalls []ToolCall

	// Usage is from MessageResult. Nil until a result is received.
	Usage *Usage

	// StopReason is from MessageResult.
	StopReason StopReason

	// Denials lists permission denials from MessageResult.
	Denials []PermissionDenial

	// IsError indicates whether the result carried an agent-level error.
	IsError bool

	// Errors contains error messages from MessageError messages.
	Errors []string

	// Result is true if a MessageResult was received.
	Result bool

	// Messages contains all messages in order, including deltas.
	Messages []Message
}

// Add dispatches msg by type, accumulating relevant fields.
// Delta types are appended to Messages but do not populate
// TextBlocks, ThinkingBlocks, or other structured fields.
func (s *TurnSummary) Add(msg Message) {
	s.Messages = append(s.Messages, msg)

	// Skip field accumulation for delta types.
	if strings.HasSuffix(string(msg.Type), "_delta") {
		return
	}

	// Capture tool calls from any message type. This handles explicit
	// MessageToolUse as well as tool calls embedded in content messages
	// (e.g., Claude CLI attaches Tool to MessageText/MessageThinking).
	// Tools carries every tool_use block when one message holds several
	// (parallel fan-out); Tool is last-one-wins and would undercount, so
	// it is only the fallback. MessageBackgroundTasks is excluded: there
	// Tools holds in-flight task snapshots, not invocations.
	if msg.Type != MessageBackgroundTasks {
		switch {
		case len(msg.Tools) > 0:
			for _, tc := range msg.Tools {
				if tc != nil {
					s.ToolCalls = append(s.ToolCalls, *tc)
				}
			}
		case msg.Tool != nil:
			s.ToolCalls = append(s.ToolCalls, *msg.Tool)
		}
	}

	switch msg.Type {
	case MessageText:
		s.TextBlocks = append(s.TextBlocks, msg.Content)
	case MessageThinking:
		s.ThinkingBlocks = append(s.ThinkingBlocks, msg.Content)
	case MessageError:
		s.Errors = append(s.Errors, msg.Content)
	case MessageResult:
		s.Usage = msg.Usage
		s.StopReason = msg.StopReason
		s.Denials = msg.Denials
		s.IsError = msg.IsError
		s.Result = true
	case MessageToolUse, MessageToolResult:
		// Tool field handled above; no additional fields to accumulate.
	}
}
