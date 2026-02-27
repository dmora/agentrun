package acp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/internal/errfmt"
)

// assertMessage checks Type, Content, and Timestamp of a parsed update.
func assertMessage(t *testing.T, msg *agentrun.Message, wantType agentrun.MessageType, wantText string) {
	t.Helper()
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Type != wantType {
		t.Errorf("type = %q, want %q", msg.Type, wantType)
	}
	if msg.Content != wantText {
		t.Errorf("content = %q, want %q", msg.Content, wantText)
	}
	if msg.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

// assertToolCall checks Tool fields on a message.
func assertToolCall(t *testing.T, msg *agentrun.Message, wantName string, wantInput, wantOutput string) {
	t.Helper()
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Tool == nil {
		t.Fatal("expected Tool, got nil")
	}
	if msg.Tool.Name != wantName {
		t.Errorf("tool name = %q, want %q", msg.Tool.Name, wantName)
	}
	if wantInput != "" && string(msg.Tool.Input) != wantInput {
		t.Errorf("tool input = %s, want %s", msg.Tool.Input, wantInput)
	}
	if wantOutput != "" && string(msg.Tool.Output) != wantOutput {
		t.Errorf("tool output = %s, want %s", msg.Tool.Output, wantOutput)
	}
}

// assertUsageContextWindow checks Type, Usage.ContextSizeTokens, and Usage.ContextUsedTokens.
func assertUsageContextWindow(t *testing.T, msg *agentrun.Message, wantSize, wantUsed int) {
	t.Helper()
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Type != agentrun.MessageContextWindow {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageContextWindow)
	}
	if msg.Usage == nil {
		t.Fatal("expected Usage, got nil")
	}
	if msg.Usage.ContextSizeTokens != wantSize {
		t.Errorf("ContextSizeTokens = %d, want %d", msg.Usage.ContextSizeTokens, wantSize)
	}
	if msg.Usage.ContextUsedTokens != wantUsed {
		t.Errorf("ContextUsedTokens = %d, want %d", msg.Usage.ContextUsedTokens, wantUsed)
	}
}

func TestParseSessionUpdate_ContentChunks(t *testing.T) {
	tests := []struct {
		name     string
		update   string
		wantType agentrun.MessageType
		wantText string
	}{
		{
			"agent_message_chunk",
			`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}`,
			agentrun.MessageTextDelta, "Hello",
		},
		{
			"agent_thought_chunk",
			`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking"}}`,
			agentrun.MessageThinkingDelta, "thinking",
		},
		{
			"user_message_chunk",
			`{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"user says"}}`,
			agentrun.MessageSystem, "user says",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := parseSessionUpdate(json.RawMessage(tt.update))
			assertMessage(t, msg, tt.wantType, tt.wantText)
		})
	}
}

func TestParseSessionUpdate_ToolCall(t *testing.T) {
	update := `{"sessionUpdate":"tool_call","toolCallId":"call_001","title":"Read file","kind":"read","rawInput":{"path":"foo.txt"}}`
	msg := parseSessionUpdate(json.RawMessage(update))
	assertMessage(t, msg, agentrun.MessageToolUse, "")
	assertToolCall(t, msg, "Read file", `{"path":"foo.txt"}`, "")
}

func TestParseSessionUpdate_ToolCallUpdate(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		update := `{"sessionUpdate":"tool_call_update","toolCallId":"call_001","title":"Read file","status":"completed","content":[{"type":"content","content":{"type":"text","text":"file contents"}}]}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertMessage(t, msg, agentrun.MessageToolResult, "")
		assertToolCall(t, msg, "Read file", "", `"file contents"`)
	})

	t.Run("failed", func(t *testing.T) {
		update := `{"sessionUpdate":"tool_call_update","toolCallId":"call_001","title":"Write file","status":"failed"}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertMessage(t, msg, agentrun.MessageError, "tool_call failed: Write file")
		if msg.ErrorCode != "tool_call_failed" {
			t.Errorf("ErrorCode = %q, want %q", msg.ErrorCode, "tool_call_failed")
		}
	})

	t.Run("in_progress", func(t *testing.T) {
		update := `{"sessionUpdate":"tool_call_update","toolCallId":"call_001","title":"Read file","status":"in_progress"}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertMessage(t, msg, agentrun.MessageSystem, "tool_call_update: Read file (in_progress)")
	})

	t.Run("pending", func(t *testing.T) {
		update := `{"sessionUpdate":"tool_call_update","toolCallId":"call_001","title":"Read file","status":"pending"}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertMessage(t, msg, agentrun.MessageSystem, "tool_call_update: Read file (pending)")
	})

	t.Run("completed_rawOutput_fallback", func(t *testing.T) {
		update := `{"sessionUpdate":"tool_call_update","toolCallId":"call_002","title":"Run cmd","status":"completed","rawOutput":{"exitCode":0,"stdout":"ok"}}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertMessage(t, msg, agentrun.MessageToolResult, "")
		assertToolCall(t, msg, "Run cmd", "", `{"exitCode":0,"stdout":"ok"}`)
	})
}

func TestParseSessionUpdate_Plan(t *testing.T) {
	update := `{"sessionUpdate":"plan","entries":[{"content":"step 1","priority":"high","status":"pending"},{"content":"step 2","priority":"medium","status":"pending"}]}`
	msg := parseSessionUpdate(json.RawMessage(update))
	assertMessage(t, msg, agentrun.MessageText, "step 1\nstep 2")
}

func TestParseSessionUpdate_MetadataUpdates(t *testing.T) {
	tests := []struct {
		name     string
		update   string
		wantType agentrun.MessageType
		wantText string
	}{
		{
			"current_mode_update",
			`{"sessionUpdate":"current_mode_update","currentModeId":"code"}`,
			agentrun.MessageSystem, "mode:code",
		},
		{
			"config_option_update",
			`{"sessionUpdate":"config_option_update","configOptions":[]}`,
			agentrun.MessageSystem, "config_option_update",
		},
		{
			"session_info_update",
			`{"sessionUpdate":"session_info_update","title":"Analyzing code"}`,
			agentrun.MessageSystem, "session_info:Analyzing code",
		},
		{
			"available_commands_update",
			`{"sessionUpdate":"available_commands_update","availableCommands":[]}`,
			agentrun.MessageSystem, "available_commands_update",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := parseSessionUpdate(json.RawMessage(tt.update))
			assertMessage(t, msg, tt.wantType, tt.wantText)
		})
	}
}

func TestParseSessionUpdate_UsageUpdate(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		update := `{"sessionUpdate":"usage_update","size":200000,"used":45000}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertUsageContextWindow(t, msg, 200000, 45000)
		if msg.Timestamp.IsZero() {
			t.Error("timestamp should be set")
		}
	})

	t.Run("fresh_session", func(t *testing.T) {
		update := `{"sessionUpdate":"usage_update","size":200000,"used":0}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertUsageContextWindow(t, msg, 200000, 0)
	})

	t.Run("no_cost", func(t *testing.T) {
		// Wire format may include cost, but we intentionally drop it.
		update := `{"sessionUpdate":"usage_update","size":200000,"used":45000,"cost":{"amount":0.05,"currency":"USD"}}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertUsageContextWindow(t, msg, 200000, 45000)
		if msg.Usage.CostUSD != 0 {
			t.Errorf("CostUSD = %f, want 0 (cost dropped from mid-turn)", msg.Usage.CostUSD)
		}
	})
}

// TestParseSessionUpdate_UsageUpdateNilCases tests inputs that should return nil
// (sanitized to no capacity).
func TestParseSessionUpdate_UsageUpdateNilCases(t *testing.T) {
	tests := []struct {
		name   string
		update string
	}{
		{"zero_fields", `{"sessionUpdate":"usage_update","size":0,"used":0}`},
		{"negative_values", `{"sessionUpdate":"usage_update","size":-1,"used":-5}`},
		{"negative_size_positive_used", `{"sessionUpdate":"usage_update","size":-1,"used":5000}`},
		{"size_zero_used_nonzero", `{"sessionUpdate":"usage_update","size":0,"used":45000}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := parseSessionUpdate(json.RawMessage(tt.update))
			if msg != nil {
				t.Errorf("expected nil, got %+v", msg)
			}
		})
	}
}

// TestParseSessionUpdate_UsageUpdateEdgeCases tests clamping and error paths.
func TestParseSessionUpdate_UsageUpdateEdgeCases(t *testing.T) {
	t.Run("used_exceeds_size", func(t *testing.T) {
		// used > size should be clamped to size.
		update := `{"sessionUpdate":"usage_update","size":200000,"used":500000}`
		msg := parseSessionUpdate(json.RawMessage(update))
		assertUsageContextWindow(t, msg, 200000, 200000)
	})

	t.Run("malformed_json", func(t *testing.T) {
		update := `{"sessionUpdate":"usage_update","size":"not_a_number"}`
		msg := parseSessionUpdate(json.RawMessage(update))
		if msg == nil {
			t.Fatal("expected non-nil error message for malformed JSON")
		}
		if msg.Type != agentrun.MessageError {
			t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageError)
		}
	})
}

func TestParseSessionUpdate_Unknown(t *testing.T) {
	tests := []struct {
		name     string
		update   string
		wantType agentrun.MessageType
		wantText string
	}{
		{
			"unknown type",
			`{"sessionUpdate":"new_event_type","data":"something"}`,
			agentrun.MessageSystem, "new_event_type",
		},
		{
			"empty discriminator",
			`{"sessionUpdate":""}`,
			agentrun.MessageSystem, "unknown",
		},
		{
			"missing discriminator",
			`{"foo":"bar"}`,
			agentrun.MessageSystem, "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := parseSessionUpdate(json.RawMessage(tt.update))
			assertMessage(t, msg, tt.wantType, tt.wantText)
		})
	}
}

func TestParseSessionUpdate_MalformedData(t *testing.T) {
	tests := []struct {
		name     string
		update   string
		wantType agentrun.MessageType
	}{
		{"bad JSON", `not json`, agentrun.MessageError},
		{"nil data", "", agentrun.MessageSystem},
		{"empty content chunk", `{"sessionUpdate":"agent_message_chunk","content":{}}`, agentrun.MessageTextDelta},
		{"tool_call with empty object", `{"sessionUpdate":"tool_call"}`, agentrun.MessageToolUse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data json.RawMessage
			if tt.update != "" {
				data = json.RawMessage(tt.update)
			}
			msg := parseSessionUpdate(data)
			if msg == nil {
				t.Fatal("expected non-nil message")
			}
			if msg.Type != tt.wantType {
				t.Errorf("type = %q, want %q", msg.Type, tt.wantType)
			}
		})
	}
}

func TestUnmarshalError_Truncation(t *testing.T) {
	// Call unmarshalError directly with an error whose message exceeds MaxLen.
	longErr := fmt.Errorf("fail: %s", strings.Repeat("x", errfmt.MaxLen+500))
	msg := unmarshalError("test_type", longErr)
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if len(msg.Content) > errfmt.MaxLen {
		t.Errorf("Content length = %d, want <= %d", len(msg.Content), errfmt.MaxLen)
	}
	if msg.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

// FuzzParseSessionUpdate verifies that arbitrary inputs never panic.
func FuzzParseSessionUpdate(f *testing.F) {
	f.Add([]byte(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}`))
	f.Add([]byte(`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"think"}}`))
	f.Add([]byte(`{"sessionUpdate":"tool_call","toolCallId":"x","title":"y","rawInput":{}}`))
	f.Add([]byte(`{"sessionUpdate":"tool_call_update","toolCallId":"x","status":"completed"}`))
	f.Add([]byte(`{"sessionUpdate":"usage_update","size":1000,"used":500}`))
	f.Add([]byte(`{"sessionUpdate":"usage_update","size":200000,"used":45000,"cost":{"amount":0.05,"currency":"USD"}}`))
	f.Add([]byte(`{"sessionUpdate":"unknown_type"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`not json`))

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Should never panic — nil return is valid (e.g. zero size+used).
		parseSessionUpdate(json.RawMessage(data))
	})
}
