package codex

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

const (
	testValidThreadID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	unknownError      = "unknown error"
)

// --- thread.started ---

func TestParseLine_ThreadStarted_First(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != testValidThreadID {
		t.Errorf("ResumeID = %q, want thread ID", msg.ResumeID)
	}
	if b.ThreadID() != testValidThreadID {
		t.Errorf("ThreadID() = %q, want stored", b.ThreadID())
	}
}

func TestParseLine_ThreadStarted_Subsequent(t *testing.T) {
	b := New()
	_, _ = b.ParseLine(`{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`)

	msg, err := b.ParseLine(`{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageSystem)
	}
	if !strings.Contains(msg.Content, "thread.started") {
		t.Errorf("Content = %q, want to contain 'thread.started'", msg.Content)
	}
}

func TestParseLine_ThreadStarted_NonUUID(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"thread.started","thread_id":"my-thread-name"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q (first thread.started)", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != "" {
		t.Errorf("ResumeID = %q, want empty (non-UUID not stored)", msg.ResumeID)
	}
	if b.ThreadID() != "" {
		t.Errorf("ThreadID() = %q, want empty (non-UUID)", b.ThreadID())
	}
}

func TestParseLine_ThreadStarted_NonUUIDThenValid(t *testing.T) {
	b := New()
	_, _ = b.ParseLine(`{"type":"thread.started","thread_id":"not-a-uuid"}`)

	msg, err := b.ParseLine(`{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != testValidThreadID {
		t.Errorf("ResumeID = %q, want thread ID", msg.ResumeID)
	}
	if b.ThreadID() != testValidThreadID {
		t.Errorf("ThreadID() = %q, want stored", b.ThreadID())
	}
}

func TestParseLine_ThreadStarted_NonUUIDThenNonUUID(t *testing.T) {
	b := New()
	_, _ = b.ParseLine(`{"type":"thread.started","thread_id":"not-a-uuid"}`)

	// Second non-UUID → MessageSystem (not a second MessageInit).
	msg, err := b.ParseLine(`{"type":"thread.started","thread_id":"also-not-uuid"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("Type = %q, want %q (second non-UUID should be system)", msg.Type, agentrun.MessageSystem)
	}
}

func TestParseLine_ThreadStarted_Empty(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"thread.started"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if b.ThreadID() != "" {
		t.Errorf("ThreadID() = %q, want empty", b.ThreadID())
	}
}

// --- turn.started / item.started ---

func TestParseLine_TurnStarted(t *testing.T) {
	b := New()
	_, err := b.ParseLine(`{"type":"turn.started"}`)
	if !errors.Is(err, cli.ErrSkipLine) {
		t.Errorf("err = %v, want ErrSkipLine", err)
	}
}

func TestParseLine_ItemStarted(t *testing.T) {
	b := New()
	_, err := b.ParseLine(`{"type":"item.started"}`)
	if !errors.Is(err, cli.ErrSkipLine) {
		t.Errorf("err = %v, want ErrSkipLine", err)
	}
}

// --- item.completed/agent_message ---

func TestParseLine_AgentMessage(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"agent_message","text":"Hello world"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageText {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageText)
	}
	if msg.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", msg.Content, "Hello world")
	}
}

func TestParseLine_AgentMessage_Empty(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"agent_message","text":""}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageText {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageText)
	}
	if msg.Content != "" {
		t.Errorf("Content = %q, want empty", msg.Content)
	}
}

// --- item.completed/reasoning ---

func TestParseLine_Reasoning(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"reasoning","text":"Let me think..."}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageThinking {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageThinking)
	}
	if msg.Content != "Let me think..." {
		t.Errorf("Content = %q, want %q", msg.Content, "Let me think...")
	}
}

// --- item.completed/command_execution ---

func TestParseLine_CommandExecution(t *testing.T) {
	b := New()
	line := `{"type":"item.completed","item":{"type":"command_execution","command":"ls -la","aggregated_output":"total 0"}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil {
		t.Fatal("Tool is nil")
	}
	if msg.Tool.Name != "command_execution" {
		t.Errorf("Tool.Name = %q, want %q", msg.Tool.Name, "command_execution")
	}
	// Input should be the command string.
	var input string
	if err := json.Unmarshal(msg.Tool.Input, &input); err != nil {
		t.Fatalf("unmarshal Input: %v", err)
	}
	if input != "ls -la" {
		t.Errorf("Input = %q, want %q", input, "ls -la")
	}
	// Output should be the full marshaled item.
	if msg.Tool.Output == nil {
		t.Fatal("Tool.Output is nil")
	}
	var output map[string]any
	if err := json.Unmarshal(msg.Tool.Output, &output); err != nil {
		t.Fatalf("unmarshal Output: %v", err)
	}
	if output["command"] != "ls -la" {
		t.Errorf("Output.command = %v, want 'ls -la'", output["command"])
	}
}

func TestParseLine_CommandExecution_NoCommand(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"command_execution"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Tool == nil {
		t.Fatal("Tool is nil")
	}
	if msg.Tool.Input != nil {
		t.Errorf("Tool.Input = %v, want nil (no command)", msg.Tool.Input)
	}
}

// --- item.completed/error ---

func TestParseLine_ItemError(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"error","message":"something broke","code":"ERR01"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if msg.Content != "ERR01: something broke" {
		t.Errorf("Content = %q, want %q", msg.Content, "ERR01: something broke")
	}
}

func TestParseLine_ItemError_Fallback(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"error","text":"fallback text"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "fallback text" {
		t.Errorf("Content = %q, want %q", msg.Content, "fallback text")
	}
}

func TestParseLine_ItemError_NoMessage(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"error"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != unknownError {
		t.Errorf("Content = %q, want %q", msg.Content, unknownError)
	}
}

// --- item.completed/file_changes ---

func TestParseLine_FileChanges(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"file_changes","files":["a.go","b.go"]}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil || msg.Tool.Name != "file_changes" {
		t.Errorf("Tool.Name = %v, want 'file_changes'", msg.Tool)
	}
	if msg.Tool.Output == nil {
		t.Error("Tool.Output should be marshaled item")
	}
}

// --- item.completed/web_search ---

func TestParseLine_WebSearch(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"web_search","query":"golang"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil || msg.Tool.Name != "web_search" {
		t.Errorf("Tool.Name = %v, want 'web_search'", msg.Tool)
	}
}

// --- item.completed/mcp_tool_call ---

func TestParseLine_MCPToolCall(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"mcp_tool_call","name":"my_mcp_tool","result":"ok"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil {
		t.Fatal("Tool is nil")
	}
	if msg.Tool.Name != "my_mcp_tool" {
		t.Errorf("Tool.Name = %q, want %q", msg.Tool.Name, "my_mcp_tool")
	}
}

func TestParseLine_MCPToolCall_NoName(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"mcp_tool_call"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Tool == nil || msg.Tool.Name != "mcp_tool_call" {
		t.Errorf("Tool.Name = %v, want 'mcp_tool_call' fallback", msg.Tool)
	}
}

// --- item.completed unknown type ---

func TestParseLine_ItemCompleted_UnknownType(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"future_type"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageSystem)
	}
	if msg.Content != "item.completed/future_type" {
		t.Errorf("Content = %q, want %q", msg.Content, "item.completed/future_type")
	}
}

func TestParseLine_ItemCompleted_MissingItem(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageSystem)
	}
	if !strings.Contains(msg.Content, "missing item") {
		t.Errorf("Content = %q, want to contain 'missing item'", msg.Content)
	}
}

// --- turn.completed ---

func TestParseLine_TurnCompleted(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"turn.completed","usage":{"input_tokens":1500,"output_tokens":200}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
	if msg.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if msg.Usage.InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500", msg.Usage.InputTokens)
	}
	if msg.Usage.OutputTokens != 200 {
		t.Errorf("OutputTokens = %d, want 200", msg.Usage.OutputTokens)
	}
}

func TestParseLine_TurnCompleted_NoUsage(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"turn.completed"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
	if msg.Usage != nil {
		t.Errorf("Usage should be nil, got %+v", msg.Usage)
	}
}

func TestParseLine_TurnCompleted_CachedTokens(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":500,"output_tokens":100}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if msg.Usage.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", msg.Usage.InputTokens)
	}
	if msg.Usage.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500", msg.Usage.CacheReadTokens)
	}
}

// --- turn.failed ---

func TestParseLine_TurnFailed(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"turn.failed","error":{"code":"TIMEOUT","message":"request timed out"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if msg.Content != "TIMEOUT: request timed out" {
		t.Errorf("Content = %q, want %q", msg.Content, "TIMEOUT: request timed out")
	}
}

func TestParseLine_TurnFailed_NoError(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"turn.failed"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if msg.Content != "turn failed" {
		t.Errorf("Content = %q, want %q", msg.Content, "turn failed")
	}
}

// --- top-level error ---

func TestParseLine_TopLevelError(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"error","message":"rate limited"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if msg.Content != "rate limited" {
		t.Errorf("Content = %q, want %q", msg.Content, "rate limited")
	}
}

func TestParseLine_TopLevelError_NoMessage(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"error"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != unknownError {
		t.Errorf("Content = %q, want %q", msg.Content, unknownError)
	}
}

// --- Edge cases ---

func TestParseLine_BlankLine(t *testing.T) {
	b := New()
	_, err := b.ParseLine("   ")
	if !errors.Is(err, cli.ErrSkipLine) {
		t.Errorf("err = %v, want ErrSkipLine", err)
	}
}

func TestParseLine_EmptyLine(t *testing.T) {
	b := New()
	_, err := b.ParseLine("")
	if !errors.Is(err, cli.ErrSkipLine) {
		t.Errorf("err = %v, want ErrSkipLine", err)
	}
}

func TestParseLine_InvalidJSON(t *testing.T) {
	b := New()
	_, err := b.ParseLine("not json at all")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error = %q, want to contain 'invalid JSON'", err.Error())
	}
}

func TestParseLine_MissingType(t *testing.T) {
	b := New()
	_, err := b.ParseLine(`{"data":"something"}`)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error = %q, want to contain 'missing'", err.Error())
	}
}

func TestParseLine_UnknownType(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"unknown_event"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageSystem)
	}
	if msg.Content != "unknown_event" {
		t.Errorf("Content = %q, want %q", msg.Content, "unknown_event")
	}
}

func TestParseLine_RawPreserved(t *testing.T) {
	b := New()
	line := `{"type":"item.completed","item":{"type":"agent_message","text":"hi"}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Raw == nil {
		t.Fatal("Raw is nil")
	}
	var raw map[string]any
	if err := json.Unmarshal(msg.Raw, &raw); err != nil {
		t.Fatalf("unmarshal Raw: %v", err)
	}
	if raw["type"] != "item.completed" {
		t.Errorf("Raw type = %v, want 'item.completed'", raw["type"])
	}
}

func TestParseLine_TimestampIsNow(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"item.completed","item":{"type":"agent_message","text":"hi"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Timestamp.IsZero() {
		t.Error("Timestamp should be set to time.Now(), got zero")
	}
}

// --- Golden JSONL fixture: simple text turn ---

func TestParseLine_GoldenFixture_TextTurn(t *testing.T) {
	b := New()
	lines := []string{
		`{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`,
		`{"type":"turn.started","turn_id":"turn_1"}`,
		`{"type":"item.started","item_id":"item_1"}`,
		`{"type":"item.completed","item":{"type":"reasoning","text":"thinking about it"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"hello world"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":50}}`,
	}
	wantTypes := []agentrun.MessageType{
		agentrun.MessageInit,
		"", // ErrSkipLine
		"", // ErrSkipLine
		agentrun.MessageThinking,
		agentrun.MessageText,
		agentrun.MessageResult,
	}

	for i, line := range lines {
		msg, err := b.ParseLine(line)
		if wantTypes[i] == "" {
			if !errors.Is(err, cli.ErrSkipLine) {
				t.Errorf("line %d: err = %v, want ErrSkipLine", i, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("line %d: unexpected error: %v", i, err)
			continue
		}
		if msg.Type != wantTypes[i] {
			t.Errorf("line %d: Type = %q, want %q", i, msg.Type, wantTypes[i])
		}
	}
}

// --- Golden JSONL fixture: command execution turn ---

func TestParseLine_GoldenFixture_CommandTurn(t *testing.T) {
	b := New()
	lines := []string{
		`{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started"}`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"ls","aggregated_output":"file1\nfile2"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"Found 2 files"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":200,"cached_input_tokens":50,"output_tokens":30}}`,
	}
	wantTypes := []agentrun.MessageType{
		agentrun.MessageInit,
		"",
		"",
		agentrun.MessageToolResult,
		agentrun.MessageText,
		agentrun.MessageResult,
	}

	for i, line := range lines {
		msg, err := b.ParseLine(line)
		if wantTypes[i] == "" {
			if !errors.Is(err, cli.ErrSkipLine) {
				t.Errorf("line %d: err = %v, want ErrSkipLine", i, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("line %d: unexpected error: %v", i, err)
			continue
		}
		if msg.Type != wantTypes[i] {
			t.Errorf("line %d: Type = %q, want %q", i, msg.Type, wantTypes[i])
		}
	}

	// Verify usage: input=200, cached=50 (separated, not folded).
	lastMsg, _ := b.ParseLine(lines[5])
	if lastMsg.Usage == nil {
		t.Fatal("expected non-nil Usage on turn.completed")
	}
	if lastMsg.Usage.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200", lastMsg.Usage.InputTokens)
	}
	if lastMsg.Usage.CacheReadTokens != 50 {
		t.Errorf("CacheReadTokens = %d, want 50", lastMsg.Usage.CacheReadTokens)
	}
}

// --- Concurrency ---

func TestParseLine_ThreadStarted_ConcurrentWriteOnce(t *testing.T) {
	b := New()
	const n = 50
	var wg sync.WaitGroup
	initCount := make(chan int, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each goroutine uses a unique valid UUID.
			ids := []string{
				"11111111-1111-1111-1111-111111111111",
				"22222222-2222-2222-2222-222222222222",
				"33333333-3333-3333-3333-333333333333",
			}
			tid := ids[idx%len(ids)]
			line := `{"type":"thread.started","thread_id":"` + tid + `"}`
			msg, err := b.ParseLine(line)
			if err != nil {
				return
			}
			if msg.Type == agentrun.MessageInit {
				initCount <- 1
			}
		}(i)
	}
	wg.Wait()
	close(initCount)

	count := 0
	for range initCount {
		count++
	}
	if count == 0 {
		t.Error("expected at least one MessageInit")
	}

	tid := b.ThreadID()
	if tid == "" {
		t.Error("ThreadID() should be non-empty after concurrent thread.started")
	}
	if b.ThreadID() != tid {
		t.Error("ThreadID() not consistent across reads")
	}
}

// --- Concurrent sentinel upgrade ---

func TestParseLine_ThreadStarted_ConcurrentSentinelUpgrade(t *testing.T) {
	b := New()
	const n = 50
	var wg sync.WaitGroup
	initCount := make(chan int, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var line string
			if idx%2 == 0 {
				line = `{"type":"thread.started","thread_id":"not-a-uuid"}`
			} else {
				line = `{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`
			}
			msg, err := b.ParseLine(line)
			if err != nil {
				return
			}
			if msg.Type == agentrun.MessageInit {
				initCount <- 1
			}
		}(i)
	}
	wg.Wait()
	close(initCount)

	count := 0
	for range initCount {
		count++
	}
	if count == 0 {
		t.Error("expected at least one MessageInit")
	}
	// After concurrent mix, ThreadID may be empty (if non-UUID won the race)
	// or set (if UUID won). Either is valid — no panic, no data race.
}

// --- Cached-only tokens ---

func TestParseLine_TurnCompleted_CachedOnly(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"turn.completed","usage":{"cached_input_tokens":500,"output_tokens":0}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil {
		t.Fatal("Usage is nil, want non-nil for cached_input_tokens=500")
	}
	if msg.Usage.InputTokens != 0 {
		t.Errorf("InputTokens = %d, want 0", msg.Usage.InputTokens)
	}
	if msg.Usage.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500", msg.Usage.CacheReadTokens)
	}
}

// --- ResumeArgs after non-UUID thread ---

func TestResumeArgs_AfterNonUUIDThread(t *testing.T) {
	b := New()
	_, _ = b.ParseLine(`{"type":"thread.started","thread_id":"my-thread-name"}`)

	_, _, err := b.ResumeArgs(agentrun.Session{}, "msg")
	if err == nil {
		t.Fatal("expected error: no thread ID after non-UUID thread.started")
	}
	assertStringContains(t, err.Error(), "no thread ID")
}

// --- Fuzz ---

func FuzzParseLine(f *testing.F) {
	seeds := []string{
		`{"type":"thread.started","thread_id":"a1b2c3d4-e5f6-7890-abcd-ef1234567890"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"hello"}}`,
		`{"type":"item.completed","item":{"type":"reasoning","text":"thinking"}}`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"ls"}}`,
		`{"type":"item.completed","item":{"type":"error","message":"oops"}}`,
		`{"type":"item.completed","item":{"type":"file_changes"}}`,
		`{"type":"item.completed","item":{"type":"web_search"}}`,
		`{"type":"item.completed","item":{"type":"mcp_tool_call","name":"tool"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":50}}`,
		`{"type":"turn.failed","error":{"message":"fail"}}`,
		`{"type":"error","message":"top-level"}`,
		`{}`,
		`not json`,
		`{"type":""}`,
		`{"type":"item.completed"}`,
		`{"type":"item.completed","item":{"type":"unknown"}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(_ *testing.T, line string) {
		b := New()
		_, _ = b.ParseLine(line)
	})
}
