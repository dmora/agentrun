package agentrun

import (
	"encoding/json"
	"testing"
)

const testToolRead = "Read"

func TestTurnSummaryAdd(t *testing.T) {
	tests := []struct {
		name  string
		msgs  []Message
		check func(t *testing.T, s *TurnSummary)
	}{
		{
			name: "normal turn text and result",
			msgs: []Message{
				{Type: MessageText, Content: "hello"},
				{Type: MessageResult, StopReason: StopEndTurn, Usage: &Usage{InputTokens: 10, OutputTokens: 5}},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.TextBlocks) != 1 || s.TextBlocks[0] != "hello" {
					t.Errorf("TextBlocks = %v, want [hello]", s.TextBlocks)
				}
				if s.Usage == nil || s.Usage.InputTokens != 10 {
					t.Error("Usage not set from result")
				}
				if s.StopReason != StopEndTurn {
					t.Errorf("StopReason = %q, want %q", s.StopReason, StopEndTurn)
				}
				if !s.Result {
					t.Error("Result should be true")
				}
				if len(s.Messages) != 2 {
					t.Errorf("Messages count = %d, want 2", len(s.Messages))
				}
			},
		},
		{
			name: "thinking turn",
			msgs: []Message{
				{Type: MessageThinking, Content: "let me think"},
				{Type: MessageText, Content: "answer"},
				{Type: MessageResult, StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.ThinkingBlocks) != 1 || s.ThinkingBlocks[0] != "let me think" {
					t.Errorf("ThinkingBlocks = %v, want [let me think]", s.ThinkingBlocks)
				}
				if len(s.TextBlocks) != 1 || s.TextBlocks[0] != "answer" {
					t.Errorf("TextBlocks = %v, want [answer]", s.TextBlocks)
				}
			},
		},
		{
			name: "tool use turn",
			msgs: []Message{
				{Type: MessageToolUse, Tool: &ToolCall{Name: "read", Input: json.RawMessage(`{"path":"foo"}`)}},
				{Type: MessageResult, StopReason: StopToolUse},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.ToolCalls) != 1 {
					t.Fatalf("ToolCalls count = %d, want 1", len(s.ToolCalls))
				}
				if s.ToolCalls[0].Name != "read" {
					t.Errorf("ToolCalls[0].Name = %q, want %q", s.ToolCalls[0].Name, "read")
				}
				if s.StopReason != StopToolUse {
					t.Errorf("StopReason = %q, want %q", s.StopReason, StopToolUse)
				}
			},
		},
		{
			name: "multiple tool calls",
			msgs: []Message{
				{Type: MessageToolUse, Tool: &ToolCall{Name: "read"}},
				{Type: MessageToolUse, Tool: &ToolCall{Name: "write"}},
				{Type: MessageResult, StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.ToolCalls) != 2 {
					t.Fatalf("ToolCalls count = %d, want 2", len(s.ToolCalls))
				}
				if s.ToolCalls[0].Name != "read" || s.ToolCalls[1].Name != "write" {
					t.Errorf("ToolCalls = %v", s.ToolCalls)
				}
			},
		},
		{
			name: "error turn",
			msgs: []Message{
				{Type: MessageError, Content: "rate limit exceeded"},
				{Type: MessageResult, StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.Errors) != 1 || s.Errors[0] != "rate limit exceeded" {
					t.Errorf("Errors = %v, want [rate limit exceeded]", s.Errors)
				}
			},
		},
		{
			name: "denial turn",
			msgs: []Message{
				{Type: MessageResult, StopReason: StopEndTurn, Denials: []PermissionDenial{
					{Tool: "bash", Reason: "not allowed"},
				}},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.Denials) != 1 {
					t.Fatalf("Denials count = %d, want 1", len(s.Denials))
				}
				if s.Denials[0].Tool != "bash" {
					t.Errorf("Denials[0].Tool = %q, want %q", s.Denials[0].Tool, "bash")
				}
			},
		},
		{
			name: "delta messages skip field accumulation",
			msgs: []Message{
				{Type: MessageTextDelta, Content: "hel"},
				{Type: MessageTextDelta, Content: "lo"},
				{Type: MessageThinkingDelta, Content: "think"},
				{Type: MessageText, Content: "hello"},
				{Type: MessageResult, StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.TextBlocks) != 1 {
					t.Errorf("TextBlocks count = %d, want 1 (deltas should not accumulate)", len(s.TextBlocks))
				}
				if len(s.ThinkingBlocks) != 0 {
					t.Errorf("ThinkingBlocks count = %d, want 0 (deltas should not accumulate)", len(s.ThinkingBlocks))
				}
				// All messages including deltas should be in Messages.
				if len(s.Messages) != 5 {
					t.Errorf("Messages count = %d, want 5", len(s.Messages))
				}
			},
		},
		{
			name: "no result",
			msgs: []Message{
				{Type: MessageText, Content: "partial"},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if s.Result {
					t.Error("Result should be false")
				}
				if s.Usage != nil {
					t.Error("Usage should be nil")
				}
				if s.StopReason != "" {
					t.Errorf("StopReason = %q, want empty", s.StopReason)
				}
			},
		},
		{
			name: "is error result",
			msgs: []Message{
				{Type: MessageResult, IsError: true, Content: "Prompt is too long", StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if !s.IsError {
					t.Error("IsError should be true")
				}
				if !s.Result {
					t.Error("Result should be true")
				}
			},
		},
		{
			name: "multi text turn preserves block boundaries",
			msgs: []Message{
				{Type: MessageText, Content: "first"},
				{Type: MessageText, Content: "second"},
				{Type: MessageText, Content: "third"},
				{Type: MessageResult, StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.TextBlocks) != 3 {
					t.Fatalf("TextBlocks count = %d, want 3", len(s.TextBlocks))
				}
				want := []string{"first", "second", "third"}
				for i, w := range want {
					if s.TextBlocks[i] != w {
						t.Errorf("TextBlocks[%d] = %q, want %q", i, s.TextBlocks[i], w)
					}
				}
			},
		},
		{
			name: "empty summary zero value",
			msgs: nil,
			check: func(t *testing.T, s *TurnSummary) {
				if s.TextBlocks != nil {
					t.Errorf("TextBlocks = %v, want nil", s.TextBlocks)
				}
				if s.ThinkingBlocks != nil {
					t.Errorf("ThinkingBlocks = %v, want nil", s.ThinkingBlocks)
				}
				if s.ToolCalls != nil {
					t.Errorf("ToolCalls = %v, want nil", s.ToolCalls)
				}
				if s.Errors != nil {
					t.Errorf("Errors = %v, want nil", s.Errors)
				}
				if s.Messages != nil {
					t.Errorf("Messages = %v, want nil", s.Messages)
				}
				if s.Result {
					t.Error("Result should be false")
				}
				if s.IsError {
					t.Error("IsError should be false")
				}
			},
		},
		{
			name: "nil tool guard",
			msgs: []Message{
				{Type: MessageToolUse, Tool: nil},
				{Type: MessageResult, StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.ToolCalls) != 0 {
					t.Errorf("ToolCalls count = %d, want 0 (nil Tool should be skipped)", len(s.ToolCalls))
				}
			},
		},
		{
			name: "nil usage on result",
			msgs: []Message{
				{Type: MessageResult, StopReason: StopEndTurn, Usage: nil},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if s.Usage != nil {
					t.Error("Usage should be nil when result has nil Usage")
				}
				if !s.Result {
					t.Error("Result should be true")
				}
			},
		},
		{
			name: "tool on MessageText (Claude CLI pattern)",
			msgs: []Message{
				{Type: MessageText, Content: "Let me read that.", Tool: &ToolCall{Name: testToolRead, Input: json.RawMessage(`{"path":"foo.go"}`)}},
				{Type: MessageResult, StopReason: StopToolUse},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.ToolCalls) != 1 {
					t.Fatalf("ToolCalls count = %d, want 1", len(s.ToolCalls))
				}
				if s.ToolCalls[0].Name != testToolRead {
					t.Errorf("ToolCalls[0].Name = %q, want Read", s.ToolCalls[0].Name)
				}
				if len(s.TextBlocks) != 1 || s.TextBlocks[0] != "Let me read that." {
					t.Errorf("TextBlocks = %v, want [Let me read that.]", s.TextBlocks)
				}
			},
		},
		{
			name: "tool on MessageThinking (Claude CLI thinking+tool)",
			msgs: []Message{
				{Type: MessageThinking, Content: "I should read", Tool: &ToolCall{Name: testToolRead}},
				{Type: MessageResult, StopReason: StopToolUse},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.ToolCalls) != 1 {
					t.Fatalf("ToolCalls count = %d, want 1", len(s.ToolCalls))
				}
				if s.ToolCalls[0].Name != testToolRead {
					t.Errorf("ToolCalls[0].Name = %q, want Read", s.ToolCalls[0].Name)
				}
				if len(s.ThinkingBlocks) != 1 {
					t.Errorf("ThinkingBlocks count = %d, want 1", len(s.ThinkingBlocks))
				}
			},
		},
		{
			name: "mixed tool sources no duplicates",
			msgs: []Message{
				{Type: MessageText, Content: "text", Tool: &ToolCall{Name: testToolRead}},
				{Type: MessageToolUse, Tool: &ToolCall{Name: "Write"}},
				{Type: MessageResult, StopReason: StopEndTurn},
			},
			check: func(t *testing.T, s *TurnSummary) {
				if len(s.ToolCalls) != 2 {
					t.Fatalf("ToolCalls count = %d, want 2", len(s.ToolCalls))
				}
				if s.ToolCalls[0].Name != testToolRead || s.ToolCalls[1].Name != "Write" {
					t.Errorf("ToolCalls = [%s, %s], want [Read, Write]", s.ToolCalls[0].Name, s.ToolCalls[1].Name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s TurnSummary
			for _, msg := range tt.msgs {
				s.Add(msg)
			}
			tt.check(t, &s)
		})
	}
}
