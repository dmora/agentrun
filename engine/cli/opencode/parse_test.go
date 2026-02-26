package opencode

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/errfmt"
)

const testValidSessionID = "ses_abcdefghij1234567890"

// --- step_start ---

func TestParseLine_StepStart_First(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"step_start","timestamp":1700000000000,"sessionID":"ses_abcdefghij1234567890"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != testValidSessionID {
		t.Errorf("ResumeID = %q, want session ID", msg.ResumeID)
	}
	if b.SessionID() != testValidSessionID {
		t.Errorf("SessionID() = %q, want stored", b.SessionID())
	}
}

func TestParseLine_StepStart_Subsequent(t *testing.T) {
	b := New()
	// First — captures session ID.
	_, _ = b.ParseLine(`{"type":"step_start","timestamp":1700000000000,"sessionID":"ses_abcdefghij1234567890"}`)

	// Second — should be system message.
	msg, err := b.ParseLine(`{"type":"step_start","timestamp":1700000000000,"sessionID":"ses_abcdefghij1234567890"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageSystem)
	}
	if !strings.Contains(msg.Content, "step_start") {
		t.Errorf("Content = %q, want to contain 'step_start'", msg.Content)
	}
}

func TestParseLine_StepStart_InvalidSessionID(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"step_start","timestamp":1700000000000,"sessionID":"bad-id"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still emit MessageInit (don't block the engine).
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	// ResumeID should be empty — invalid ID not exposed to consumers.
	if msg.ResumeID != "" {
		t.Errorf("ResumeID = %q, want empty (invalid session ID)", msg.ResumeID)
	}
	// Session ID should NOT be stored.
	if b.SessionID() != "" {
		t.Errorf("SessionID() = %q, want empty (invalid ID should not be stored)", b.SessionID())
	}
}

func TestParseLine_StepStart_EmptySessionID(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"step_start","timestamp":1700000000000}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First step_start should always emit MessageInit, even without sessionID.
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if b.SessionID() != "" {
		t.Errorf("SessionID() = %q, want empty (no ID to store)", b.SessionID())
	}
}

func TestParseLine_StepStart_InvalidThenValid(t *testing.T) {
	b := New()
	// First with invalid ID — emits init but doesn't store.
	_, _ = b.ParseLine(`{"type":"step_start","timestamp":1700000000000,"sessionID":"bad-id"}`)

	// Second with valid ID — should store and emit init with ResumeID.
	msg, err := b.ParseLine(`{"type":"step_start","timestamp":1700000000000,"sessionID":"ses_abcdefghij1234567890"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID != testValidSessionID {
		t.Errorf("ResumeID = %q, want session ID", msg.ResumeID)
	}
	if b.SessionID() != testValidSessionID {
		t.Errorf("SessionID() = %q, want stored", b.SessionID())
	}
}

// --- text ---

func TestParseLine_Text(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"text","timestamp":1700000000000,"part":{"text":"Hello world"}}`)
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

func TestParseLine_Text_Empty(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"text","timestamp":1700000000000,"part":{"text":""}}`)
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

// --- tool_use ---

func TestParseLine_ToolUse(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"tool_use","timestamp":1700000000000,"part":{"tool":"bash","state":{"input":"ls -la","output":"total 0","status":"completed"}}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil {
		t.Fatal("Tool is nil")
	}
	if msg.Tool.Name != "bash" {
		t.Errorf("Tool.Name = %q, want %q", msg.Tool.Name, "bash")
	}
	// Input/Output are json.RawMessage — verify they unmarshal correctly.
	var input string
	if err := json.Unmarshal(msg.Tool.Input, &input); err != nil {
		t.Fatalf("unmarshal Input: %v", err)
	}
	if input != "ls -la" {
		t.Errorf("Input = %q, want %q", input, "ls -la")
	}
	var output string
	if err := json.Unmarshal(msg.Tool.Output, &output); err != nil {
		t.Fatalf("unmarshal Output: %v", err)
	}
	if output != "total 0" {
		t.Errorf("Output = %q, want %q", output, "total 0")
	}
}

func TestParseLine_ToolUse_NoState(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"tool_use","timestamp":1700000000000,"part":{"tool":"read"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil {
		t.Fatal("Tool is nil")
	}
	if msg.Tool.Name != "read" {
		t.Errorf("Tool.Name = %q, want %q", msg.Tool.Name, "read")
	}
	if msg.Tool.Input != nil {
		t.Errorf("Tool.Input = %v, want nil (no state)", msg.Tool.Input)
	}
	if msg.Tool.Output != nil {
		t.Errorf("Tool.Output = %v, want nil (no state)", msg.Tool.Output)
	}
}

func TestParseLine_ToolUse_NoPart(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"tool_use","timestamp":1700000000000}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageToolResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageToolResult)
	}
	if msg.Tool == nil {
		t.Fatal("Tool should not be nil when part is missing")
	}
	if msg.Tool.Name != "" {
		t.Errorf("Tool.Name = %q, want empty", msg.Tool.Name)
	}
}

// --- step_finish ---

func TestParseLine_StepFinish(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"step_finish","timestamp":1700000000000,"part":{"tokens":{"input":1500,"output":200}}}`)
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

func TestParseLine_StepFinish_NoTokens(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"step_finish","timestamp":1700000000000,"part":{}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
	if msg.Usage != nil {
		t.Errorf("Usage should be nil when no tokens, got %+v", msg.Usage)
	}
}

func TestParseLine_StepFinish_InputTokensContract(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"step_finish","timestamp":1700000000000,"part":{"tokens":{"input":5000,"output":300}}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage == nil || msg.Usage.InputTokens <= 0 {
		t.Error("InputTokens must be populated for context-window monitoring")
	}
}

// --- reasoning ---

func TestParseLine_Reasoning(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"reasoning","timestamp":1700000000000,"part":{"text":"Let me think..."}}`)
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

// --- error ---

func TestParseLine_Error(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"error","timestamp":1700000000000,"error":{"name":"APIError","data":{"message":"rate limited"}}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if msg.Content != "APIError: rate limited" {
		t.Errorf("Content = %q, want %q", msg.Content, "APIError: rate limited")
	}
}

func TestParseLine_Error_NilErrorObject(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"error","timestamp":1700000000000}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageError {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageError)
	}
	if msg.Content != "unknown error" {
		t.Errorf("Content = %q, want %q", msg.Content, "unknown error")
	}
}

func TestParseLine_Error_FallbackMessage(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"error","timestamp":1700000000000,"error":{"name":"InternalError","message":"something broke"}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Content != "InternalError: something broke" {
		t.Errorf("Content = %q, want %q", msg.Content, "InternalError: something broke")
	}
}

func TestParseLine_Error_LongMessage(t *testing.T) {
	b := New()
	longMsg := strings.Repeat("x", errfmt.MaxLen+500)
	line := `{"type":"error","timestamp":1700000000000,"error":{"name":"E","message":"` + longMsg + `"}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.Content) > errfmt.MaxLen {
		t.Errorf("Content length = %d, want <= %d", len(msg.Content), errfmt.MaxLen)
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
	_, err := b.ParseLine(`{"timestamp":1700000000000,"data":"something"}`)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error = %q, want to contain 'missing'", err.Error())
	}
}

func TestParseLine_UnknownType(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"unknown_event","timestamp":1700000000000}`)
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

func TestParseLine_Timestamp(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"step_start","timestamp":1700000000000}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.UnixMilli(1700000000000)
	if !msg.Timestamp.Equal(expected) {
		t.Errorf("Timestamp = %v, want %v", msg.Timestamp, expected)
	}
}

func TestParseLine_MissingTimestamp(t *testing.T) {
	b := New()
	before := time.Now()
	msg, err := b.ParseLine(`{"type":"step_start"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Timestamp.Before(before) {
		t.Error("missing timestamp should fall back to time.Now()")
	}
}

func TestParseLine_RawPreserved(t *testing.T) {
	b := New()
	line := `{"type":"text","timestamp":1700000000000,"part":{"text":"hi"}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Raw == nil {
		t.Fatal("Raw is nil")
	}
	// Round-trip: unmarshal Raw and check type field.
	var raw map[string]any
	if err := json.Unmarshal(msg.Raw, &raw); err != nil {
		t.Fatalf("unmarshal Raw: %v", err)
	}
	if raw["type"] != "text" {
		t.Errorf("Raw type = %v, want 'text'", raw["type"])
	}
}

// --- Concurrency ---

func TestParseLine_StepStart_ConcurrentWriteOnce(t *testing.T) {
	b := New()
	const n = 50
	var wg sync.WaitGroup
	initCount := make(chan int, n)

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each goroutine sends a step_start with a unique valid session ID.
			sid := "ses_concurrent" + strings.Repeat("x", 20) + string(rune('a'+idx%26))
			line := `{"type":"step_start","timestamp":1700000000000,"sessionID":"` + sid + `"}`
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

	// Exactly one or two inits: one for the CAS winner, possibly one more
	// if an invalid-then-valid race occurs. But stored ID must be consistent.
	count := 0
	for range initCount {
		count++
	}
	if count == 0 {
		t.Error("expected at least one MessageInit")
	}

	// Stored session ID must be non-empty and consistent across reads.
	sid := b.SessionID()
	if sid == "" {
		t.Error("SessionID() should be non-empty after concurrent step_starts")
	}
	if b.SessionID() != sid {
		t.Error("SessionID() not consistent across reads")
	}
}

// --- ResumeArgs chained with invalid session ID ---

func TestResumeArgs_AfterInvalidSessionID(t *testing.T) {
	b := New()
	// ParseLine with invalid session ID — not stored.
	_, _ = b.ParseLine(`{"type":"step_start","timestamp":1700000000000,"sessionID":"bad"}`)

	// ResumeArgs with no OptionSessionID should fail.
	_, _, err := b.ResumeArgs(agentrun.Session{}, "msg")
	if err == nil {
		t.Fatal("expected error: no session ID after invalid step_start")
	}
	assertStringContains(t, err.Error(), "no session ID")
}

// --- Fuzz ---

func FuzzParseLine(f *testing.F) {
	// Seed with all 6 event types.
	seeds := []string{
		`{"type":"step_start","timestamp":1700000000000,"sessionID":"ses_abcdefghij1234567890"}`,
		`{"type":"text","timestamp":1700000000000,"part":{"text":"hello"}}`,
		`{"type":"tool_use","timestamp":1700000000000,"part":{"tool":"bash","state":{"input":"ls","output":"ok","status":"completed"}}}`,
		`{"type":"step_finish","timestamp":1700000000000,"part":{"tokens":{"input":100,"output":50}}}`,
		`{"type":"reasoning","timestamp":1700000000000,"part":{"text":"thinking..."}}`,
		`{"type":"error","timestamp":1700000000000,"error":{"name":"E","data":{"message":"oops"}}}`,
		`{}`,
		`not json`,
		`{"type":""}`,
		`{"type":"step_start","timestamp":1700000000000,"sessionID":"ses_abcdefghij1234567890"}`,
		`{"type":"step_start","timestamp":1700000000000}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(_ *testing.T, line string) {
		b := New()
		// Must not panic.
		_, _ = b.ParseLine(line)
	})
}
