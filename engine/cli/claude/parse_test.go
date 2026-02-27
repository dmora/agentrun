package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/internal/errfmt"
)

// --- ParseLine tests ---

func TestParseLine_BlankLine(t *testing.T) {
	b := New()
	_, err := b.ParseLine("")
	if !errors.Is(err, cli.ErrSkipLine) {
		t.Errorf("blank line should return ErrSkipLine, got %v", err)
	}
}

func TestParseLine_WhitespaceLine(t *testing.T) {
	b := New()
	_, err := b.ParseLine("   \t  ")
	if !errors.Is(err, cli.ErrSkipLine) {
		t.Errorf("whitespace line should return ErrSkipLine, got %v", err)
	}
}

func TestParseLine_InvalidJSON(t *testing.T) {
	b := New()
	_, err := b.ParseLine("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseLine_MissingType(t *testing.T) {
	b := New()
	_, err := b.ParseLine(`{"data":"value"}`)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention missing type: %v", err)
	}
}

func TestParseLine_EmptyType(t *testing.T) {
	b := New()
	_, err := b.ParseLine(`{"type":""}`)
	if err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestParseLine_SystemInit(t *testing.T) {
	b := New()
	line := `{"type":"system","subtype":"init","session_id":"abc","model":"claude-sonnet-4-5-20250514"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != "abc" {
		t.Errorf("ResumeID = %q, want %q (session_id)", msg.ResumeID, "abc")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_SystemInit_NoSessionID(t *testing.T) {
	b := New()
	line := `{"type":"system","subtype":"init","model":"claude-sonnet-4-5-20250514"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != "" {
		t.Errorf("ResumeID = %q, want empty (no session_id)", msg.ResumeID)
	}
}

func TestParseLine_SystemMessage(t *testing.T) {
	b := New()
	line := `{"type":"system","subtype":"status","message":"Working..."}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageSystem)
	}
	if msg.Content != "Working..." {
		t.Errorf("content = %q, want %q", msg.Content, "Working...")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_StandaloneInit(t *testing.T) {
	b := New()
	line := `{"type":"init","session_id":"xyz","model":"claude-sonnet-4-5-20250514"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != "xyz" {
		t.Errorf("ResumeID = %q, want %q (session_id)", msg.ResumeID, "xyz")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_StandaloneInit_NoSessionID(t *testing.T) {
	b := New()
	line := `{"type":"init","model":"claude-sonnet-4-5-20250514"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != "" {
		t.Errorf("ResumeID = %q, want empty (no session_id)", msg.ResumeID)
	}
}

// --- Init.Model tests ---

func TestParseLine_StandaloneInit_WithModel(t *testing.T) {
	b := New()
	line := `{"type":"init","session_id":"xyz","model":"claude-sonnet-4-5-20250514"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Init == nil {
		t.Fatal("Init should be populated when model present")
	}
	if msg.Init.Model != "claude-sonnet-4-5-20250514" {
		t.Errorf("Init.Model = %q, want %q", msg.Init.Model, "claude-sonnet-4-5-20250514")
	}
}

func TestParseLine_SystemInit_WithModel(t *testing.T) {
	b := New()
	line := `{"type":"system","subtype":"init","session_id":"abc","model":"claude-sonnet-4-5-20250514"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Init == nil {
		t.Fatal("Init should be populated when model present")
	}
	if msg.Init.Model != "claude-sonnet-4-5-20250514" {
		t.Errorf("Init.Model = %q, want %q", msg.Init.Model, "claude-sonnet-4-5-20250514")
	}
}

func TestParseLine_StandaloneInit_NoModel(t *testing.T) {
	b := New()
	line := `{"type":"init","session_id":"xyz"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Init != nil {
		t.Errorf("Init should be nil when model absent, got %+v", msg.Init)
	}
}

func TestParseLine_StandaloneInit_EmptyModel(t *testing.T) {
	b := New()
	line := `{"type":"init","session_id":"xyz","model":""}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Init != nil {
		t.Errorf("Init should be nil when model is empty string, got %+v", msg.Init)
	}
}

func TestParseLine_StandaloneInit_ControlCharsModel(t *testing.T) {
	b := New()
	line := `{"type":"init","session_id":"xyz","model":"bad\u0000model"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Init != nil {
		t.Errorf("Init should be nil when model has control chars, got %+v", msg.Init)
	}
}

func TestParseLine_SystemInit_NoModel(t *testing.T) {
	b := New()
	line := `{"type":"system","subtype":"init","session_id":"abc"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.Init != nil {
		t.Errorf("Init should be nil when no model, got %+v", msg.Init)
	}
}

func TestParseLine_AssistantNestedContent(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello "},{"type":"text","text":"world"}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageText {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageText)
	}
	if msg.Content != "Hello world" {
		t.Errorf("content = %q, want %q", msg.Content, "Hello world")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_AssistantFlatText(t *testing.T) {
	b := New()
	line := `{"type":"assistant","text":"flat text"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "flat text" {
		t.Errorf("content = %q, want %q", msg.Content, "flat text")
	}
}

func TestParseLine_AssistantFlatContent(t *testing.T) {
	b := New()
	line := `{"type":"assistant","content":"flat content"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "flat content" {
		t.Errorf("content = %q, want %q", msg.Content, "flat content")
	}
}

func TestParseLine_AssistantWithToolUse(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me read that."},{"type":"tool_use","name":"Read","input":{"path":"/tmp/test.txt"}}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageText {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageText)
	}
	if msg.Content != "Let me read that." {
		t.Errorf("content = %q, want %q", msg.Content, "Let me read that.")
	}
	if msg.Tool == nil {
		t.Fatal("tool should be populated")
	}
	if msg.Tool.Name != testToolName {
		t.Errorf("tool name = %q, want %q", msg.Tool.Name, testToolName)
	}
	if msg.Tool.Input == nil {
		t.Fatal("tool input should be populated")
	}
	var inputMap map[string]any
	if err := json.Unmarshal(msg.Tool.Input, &inputMap); err != nil {
		t.Fatalf("tool input is not valid JSON: %v", err)
	}
	if inputMap["path"] != "/tmp/test.txt" {
		t.Errorf("tool input path = %v, want /tmp/test.txt", inputMap["path"])
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_AssistantThinkingOnly(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me reason about this."}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageThinking {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageThinking)
	}
	if msg.Content != "Let me reason about this." {
		t.Errorf("content = %q, want %q", msg.Content, "Let me reason about this.")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_AssistantThinkingMultipleBlocks(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"First "},{"type":"thinking","thinking":"then second."}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageThinking {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageThinking)
	}
	if msg.Content != "First then second." {
		t.Errorf("content = %q, want %q", msg.Content, "First then second.")
	}
}

func TestParseLine_AssistantThinkingAndText(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"reasoning..."},{"type":"text","text":"The answer is 42."}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageText {
		t.Errorf("type = %q, want %q (text takes priority)", msg.Type, agentrun.MessageText)
	}
	if msg.Content != "The answer is 42." {
		t.Errorf("content = %q, want %q", msg.Content, "The answer is 42.")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_AssistantThinkingEmpty(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty thinking with no text → stays MessageText (default from parseAssistantMessage).
	if msg.Type != agentrun.MessageText {
		t.Errorf("type = %q, want %q (empty thinking stays text)", msg.Type, agentrun.MessageText)
	}
}

func TestParseLine_AssistantThinkingAndToolUse(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me read the file."},{"type":"tool_use","name":"read_file","input":{"path":"foo.txt"}}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No text content → thinking takes priority for type.
	if msg.Type != agentrun.MessageThinking {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageThinking)
	}
	if msg.Content != "Let me read the file." {
		t.Errorf("content = %q, want %q", msg.Content, "Let me read the file.")
	}
	if msg.Tool == nil {
		t.Fatal("tool should be populated even with thinking")
	}
	if msg.Tool.Name != "read_file" {
		t.Errorf("tool name = %q, want %q", msg.Tool.Name, "read_file")
	}
}

func TestParseLine_AssistantMixedTextToolText(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"before "},{"type":"tool_use","name":"Read","input":{}},{"type":"text","text":"after"}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "before after" {
		t.Errorf("content = %q, want %q", msg.Content, "before after")
	}
	if msg.Tool == nil {
		t.Fatal("tool should be populated")
	}
}

func TestParseLine_AssistantMultipleToolUse(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"First","input":{}},{"type":"tool_use","name":"Last","input":{}}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Tool == nil {
		t.Fatal("tool should be populated")
	}
	if msg.Tool.Name != "Last" {
		t.Errorf("tool name = %q, want %q (last wins)", msg.Tool.Name, "Last")
	}
}

func TestParseLine_AssistantWithUsage(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":100,"output_tokens":50}}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be populated")
	}
	if msg.Usage.InputTokens != 100 {
		t.Errorf("input_tokens = %d, want 100", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != 50 {
		t.Errorf("output_tokens = %d, want 50", msg.Usage.OutputTokens)
	}
}

func TestParseLine_AssistantNoUsage(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage != nil {
		t.Errorf("usage should be nil when absent, got %+v", msg.Usage)
	}
}

func TestParseLine_AssistantZeroUsage(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":0,"output_tokens":0}}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage != nil {
		t.Errorf("zero usage should be nil (not &Usage{0,0}), got %+v", msg.Usage)
	}
}

func TestParseLine_ToolResult(t *testing.T) {
	b := New()
	line := `{"type":"tool","name":"Read","input":{"path":"/tmp"},"output":"file contents","status":"success"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil {
		t.Fatal("tool should be populated")
	}
	if msg.Tool.Name != testToolName {
		t.Errorf("tool name = %q, want %q", msg.Tool.Name, testToolName)
	}
	if msg.Tool.Input == nil {
		t.Fatal("tool input should be populated")
	}
	var inputMap map[string]any
	if err := json.Unmarshal(msg.Tool.Input, &inputMap); err != nil {
		t.Fatalf("tool input is not valid JSON: %v", err)
	}
	if inputMap["path"] != "/tmp" {
		t.Errorf("tool input path = %v, want /tmp", inputMap["path"])
	}
	if msg.Tool.Output == nil {
		t.Fatal("tool output should be populated")
	}
	var output string
	if err := json.Unmarshal(msg.Tool.Output, &output); err != nil {
		t.Fatalf("tool output is not valid JSON string: %v", err)
	}
	if output != "file contents" {
		t.Errorf("tool output = %q, want %q", output, "file contents")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_Result(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"Task completed successfully"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageResult {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
	if msg.Content != "Task completed successfully" {
		t.Errorf("content = %q, want %q", msg.Content, "Task completed successfully")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_ResultWithUsage(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","usage":{"input_tokens":500,"output_tokens":200}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageResult {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be populated")
	}
	if msg.Usage.InputTokens != 500 {
		t.Errorf("input_tokens = %d, want 500", msg.Usage.InputTokens)
	}
}

func TestParseLine_ResultNoUsage(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage != nil {
		t.Errorf("usage should be nil when absent, got %+v", msg.Usage)
	}
}

func TestParseLine_ResultTextOnly(t *testing.T) {
	b := New()
	line := `{"type":"result","text":"text-only result"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageResult {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
	if msg.Content != "text-only result" {
		t.Errorf("content = %q, want %q", msg.Content, "text-only result")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_ErrorWithCode(t *testing.T) {
	b := New()
	line := `{"type":"error","code":"rate_limit","message":"Too many requests"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if msg.ErrorCode != "rate_limit" {
		t.Errorf("ErrorCode = %q, want %q", msg.ErrorCode, "rate_limit")
	}
	if msg.Content != "Too many requests" {
		t.Errorf("content = %q, want %q", msg.Content, "Too many requests")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_ErrorStringFallback(t *testing.T) {
	b := New()
	line := `{"type":"error","error":"something went wrong"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "something went wrong" {
		t.Errorf("content = %q, want %q", msg.Content, "something went wrong")
	}
	if msg.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", msg.ErrorCode)
	}
}

func TestParseLine_ErrorLongMessage(t *testing.T) {
	b := New()
	longMsg := strings.Repeat("x", errfmt.MaxLen+500)
	line := `{"type":"error","code":"E","message":"` + longMsg + `"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.Content) > errfmt.MaxLen {
		t.Errorf("Content length = %d, want <= %d", len(msg.Content), errfmt.MaxLen)
	}
}

func TestParseLine_ErrorControlCharCode(t *testing.T) {
	b := New()
	line := `{"type":"error","code":"\u0000rate_limit","message":"Too many requests"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty (control char rejection)", msg.ErrorCode)
	}
}

func TestParseLine_UnknownType(t *testing.T) {
	b := New()
	line := `{"type":"custom_event","data":"value"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != "custom_event" {
		t.Errorf("type = %q, want %q", msg.Type, "custom_event")
	}
	assertRawPopulated(t, msg)
}

func TestParseLine_UnknownTypeTooLong(t *testing.T) {
	b := New()
	longType := strings.Repeat("x", 65)
	line := fmt.Sprintf(`{"type":"%s"}`, longType)
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("long unknown type should be sanitized to system, got %q", msg.Type)
	}
}

func TestParseLine_UnknownTypeControlChars(t *testing.T) {
	b := New()
	line := `{"type":"evil\ntype"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		// JSON might reject control chars — that's fine.
		return
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("control char type should be sanitized to system, got %q", msg.Type)
	}
}

func TestParseLine_NullToolInput(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Test","input":null}]}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Tool == nil {
		t.Fatal("tool should be populated")
	}
	// null input should marshal to JSON "null".
	if msg.Tool.Input == nil {
		t.Error("null input should still be marshaled")
	}
}

// --- Stream event ParseLine tests ---

func TestParseLine_StreamEventDeltas(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantTyp agentrun.MessageType
		wantCnt string
	}{
		{
			name:    "text_delta",
			line:    `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}}`,
			wantTyp: agentrun.MessageTextDelta,
			wantCnt: "hello",
		},
		{
			name:    "input_json_delta",
			line:    `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"key\":"}}}`,
			wantTyp: agentrun.MessageToolUseDelta,
			wantCnt: `{"key":`,
		},
		{
			name:    "thinking_delta",
			line:    `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"let me consider"}}}`,
			wantTyp: agentrun.MessageThinkingDelta,
			wantCnt: "let me consider",
		},
		{
			name:    "signature_delta",
			line:    `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"ErUBCkYIAxgCIkD"}}}`,
			wantTyp: agentrun.MessageSystem,
			wantCnt: "ErUBCkYIAxgCIkD",
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := b.ParseLine(tt.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.Type != tt.wantTyp {
				t.Errorf("type = %q, want %q", msg.Type, tt.wantTyp)
			}
			if msg.Content != tt.wantCnt {
				t.Errorf("content = %q, want %q", msg.Content, tt.wantCnt)
			}
			assertRawPopulated(t, msg)
		})
	}
}

func TestParseLine_StreamEventLifecycle(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantCnt string
	}{
		{"message_start", `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1"}}}`, "stream_event: message_start"},
		{"content_block_start", `{"type":"stream_event","event":{"type":"content_block_start","index":0}}`, "stream_event: content_block_start"},
		{"content_block_stop", `{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`, "stream_event: content_block_stop"},
		{"message_stop", `{"type":"stream_event","event":{"type":"message_stop"}}`, "stream_event: message_stop"},
		{"message_delta", `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"}}}`, "stream_event: message_delta"},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := b.ParseLine(tt.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.Type != agentrun.MessageSystem {
				t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageSystem)
			}
			if msg.Content != tt.wantCnt {
				t.Errorf("content = %q, want %q", msg.Content, tt.wantCnt)
			}
			assertRawPopulated(t, msg)
		})
	}
}

func TestParseLine_StreamEventInvalidEvent(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantCnt string
	}{
		{"no event field", `{"type":"stream_event"}`, "stream_event: missing or invalid event field"},
		{"event as string", `{"type":"stream_event","event":"not_an_object"}`, "stream_event: missing or invalid event field"},
		{"event as null", `{"type":"stream_event","event":null}`, "stream_event: missing or invalid event field"},
		{"empty stream_event object", `{"type":"stream_event","event":{}}`, "stream_event: "},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := b.ParseLine(tt.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.Type != agentrun.MessageSystem {
				t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageSystem)
			}
			if msg.Content != tt.wantCnt {
				t.Errorf("content = %q, want %q", msg.Content, tt.wantCnt)
			}
			assertRawPopulated(t, msg)
		})
	}
}

func TestParseLine_StreamEventInvalidDelta(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantTyp agentrun.MessageType
		wantCnt string
	}{
		{"delta missing", `{"type":"stream_event","event":{"type":"content_block_delta"}}`, agentrun.MessageSystem, "content_block_delta: missing or invalid delta field"},
		{"delta as string", `{"type":"stream_event","event":{"type":"content_block_delta","delta":"not_an_object"}}`, agentrun.MessageSystem, "content_block_delta: missing or invalid delta field"},
		{"delta as null", `{"type":"stream_event","event":{"type":"content_block_delta","delta":null}}`, agentrun.MessageSystem, "content_block_delta: missing or invalid delta field"},
		{"unknown delta type", `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"unknown_delta"}}}`, agentrun.MessageSystem, "content_block_delta: unknown delta type: unknown_delta"},
		{"text_delta missing text", `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta"}}}`, agentrun.MessageTextDelta, ""},
		{"delta type key absent", `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"other":"field"}}}`, agentrun.MessageSystem, "content_block_delta: unknown delta type: "},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := b.ParseLine(tt.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.Type != tt.wantTyp {
				t.Errorf("type = %q, want %q", msg.Type, tt.wantTyp)
			}
			if msg.Content != tt.wantCnt {
				t.Errorf("content = %q, want %q", msg.Content, tt.wantCnt)
			}
			assertRawPopulated(t, msg)
		})
	}
}

// --- Cache tokens + cost + StopReason tests ---

func TestParseLine_ResultWithCacheTokens(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","usage":{"input_tokens":500,"output_tokens":200,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be populated")
	}
	if msg.Usage.CacheReadTokens != 100 {
		t.Errorf("CacheReadTokens = %d, want 100", msg.Usage.CacheReadTokens)
	}
	if msg.Usage.CacheWriteTokens != 50 {
		t.Errorf("CacheWriteTokens = %d, want 50", msg.Usage.CacheWriteTokens)
	}
}

func TestParseLine_ResultWithCostUSD(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","total_cost_usd":0.1181,"usage":{"input_tokens":500,"output_tokens":200}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be populated")
	}
	if msg.Usage.CostUSD != 0.1181 {
		t.Errorf("CostUSD = %f, want 0.1181", msg.Usage.CostUSD)
	}
}

func TestParseLine_ResultCacheOnlyNoInputOutput(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":5}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be non-nil when cache tokens present")
	}
	if msg.Usage.CacheReadTokens != 5 {
		t.Errorf("CacheReadTokens = %d, want 5", msg.Usage.CacheReadTokens)
	}
}

func TestParseLine_ResultWithThinkingTokens(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","usage":{"input_tokens":500,"output_tokens":200,"thinking_tokens":150}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be populated")
	}
	if msg.Usage.ThinkingTokens != 150 {
		t.Errorf("ThinkingTokens = %d, want 150", msg.Usage.ThinkingTokens)
	}
}

func TestParseLine_ResultThinkingOnlyNoInputOutput(t *testing.T) {
	b := New()
	// Nil-guard boundary: only thinking tokens present — Usage should be non-nil.
	line := `{"type":"result","result":"done","usage":{"input_tokens":0,"output_tokens":0,"thinking_tokens":42}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be non-nil when thinking tokens present")
	}
	if msg.Usage.ThinkingTokens != 42 {
		t.Errorf("ThinkingTokens = %d, want 42", msg.Usage.ThinkingTokens)
	}
}

func TestParseLine_ResultCostOnlyNoTokens(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","total_cost_usd":0.05,"usage":{"input_tokens":0,"output_tokens":0}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be non-nil when cost present")
	}
	if msg.Usage.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", msg.Usage.CostUSD)
	}
}

func TestParseLine_ResultCostWithoutUsageObject(t *testing.T) {
	b := New()
	// total_cost_usd present at root but no "usage" sub-object — cost must
	// still be captured (not silently dropped by early return).
	line := `{"type":"result","result":"done","total_cost_usd":0.11}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("usage should be non-nil when cost present without usage object")
	}
	if msg.Usage.CostUSD != 0.11 {
		t.Errorf("CostUSD = %f, want 0.11", msg.Usage.CostUSD)
	}
	if msg.Usage.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", msg.Usage.InputTokens)
	}
}

func TestParseLine_ResultCostNaN(t *testing.T) {
	b := New()
	// NaN is not valid JSON, but test the extraction path via a zero result.
	// Since json.Unmarshal can't produce NaN from valid JSON, this tests
	// that zero cost + zero tokens → nil usage (no false positive).
	line := `{"type":"result","result":"done","usage":{"input_tokens":0,"output_tokens":0}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage != nil {
		t.Errorf("usage should be nil when all fields zero, got %+v", msg.Usage)
	}
}

func TestParseLine_ResultCostNegative(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","total_cost_usd":-1.5,"usage":{"input_tokens":0,"output_tokens":0}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Negative cost sanitized to 0, all tokens zero → nil usage.
	if msg.Usage != nil {
		t.Errorf("usage should be nil when cost is negative (sanitized), got %+v", msg.Usage)
	}
}

func TestParseLine_MessageDeltaStopReason(t *testing.T) {
	b := New()
	line := `{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageSystem)
	}
	if msg.StopReason != agentrun.StopEndTurn {
		t.Errorf("StopReason = %q, want %q", msg.StopReason, agentrun.StopEndTurn)
	}
}

func TestParseLine_ResultWithStopReason(t *testing.T) {
	b := New()
	line := `{"type":"result","result":"done","stop_reason":"max_tokens","usage":{"input_tokens":100,"output_tokens":50}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.StopReason != agentrun.StopMaxTokens {
		t.Errorf("StopReason = %q, want %q", msg.StopReason, agentrun.StopMaxTokens)
	}
}

// --- Fuzz test ---

func FuzzParseLine(f *testing.F) {
	// Seed corpus with representative JSON fixtures.
	seeds := []string{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"init","model":"claude-sonnet-4-5-20250514"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"assistant","text":"flat"}`,
		`{"type":"tool","name":"Read","output":"data"}`,
		`{"type":"result","result":"done","usage":{"input_tokens":10,"output_tokens":5}}`,
		`{"type":"error","code":"err","message":"msg"}`,
		`{"type":"unknown"}`,
		`{}`,
		`{"type":""}`,
		`not json`,
		``,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"T","input":null}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{\"key\":"}}}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop"}}`,
		`{"type":"stream_event"}`,
		`{"type":"stream_event","event":"not_an_object"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"let me"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"signature_delta","signature":"ErUBCkYIAxgCIkD"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me reason."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"reasoning"},{"type":"text","text":"answer"}]}}`,
		`{"type":"init","session_id":"conv-abc123","model":"claude"}`,
		`{"type":"system","subtype":"init","session_id":"sess_xyz789"}`,
		`{"type":"init"}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	b := New()
	f.Fuzz(func(t *testing.T, line string) {
		// Must not panic.
		msg, err := b.ParseLine(line)
		if err != nil {
			return
		}
		// Raw should always be populated on successful parse.
		if msg.Raw == nil {
			t.Error("Raw should be populated on successful parse")
		}
	})
}

// --- Helpers ---

func assertRawPopulated(t *testing.T, msg agentrun.Message) {
	t.Helper()
	if len(msg.Raw) == 0 {
		t.Error("msg.Raw should be populated")
	}
	// Verify Raw is valid JSON.
	if !json.Valid(msg.Raw) {
		t.Error("msg.Raw should be valid JSON")
	}
}
