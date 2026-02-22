package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// Test fixture constants to satisfy goconst (3+ occurrences).
const (
	testModel        = "claude-sonnet-4-5-20250514"
	testPrompt       = "hello world"
	testSystemPrompt = "be helpful"
	testResumeID     = "conv-abc123"
	testBinary       = "/usr/local/bin/claude"
	testToolName     = "Read"
)

// --- Constructor tests ---

func TestNew_Default(t *testing.T) {
	b := New()
	if b.binary != defaultBinary {
		t.Errorf("binary = %q, want %q", b.binary, defaultBinary)
	}
}

func TestNew_WithBinary(t *testing.T) {
	b := New(WithBinary(testBinary))
	if b.binary != testBinary {
		t.Errorf("binary = %q, want %q", b.binary, testBinary)
	}
}

func TestNew_WithBinaryEmpty(t *testing.T) {
	b := New(WithBinary(""))
	if b.binary != defaultBinary {
		t.Errorf("empty WithBinary should keep default, got %q", b.binary)
	}
}

// --- SpawnArgs tests ---

func TestSpawnArgs(t *testing.T) {
	tests := []struct {
		name     string
		session  agentrun.Session
		contains []string
		last     string
	}{
		{
			name:     "minimal",
			session:  agentrun.Session{Prompt: testPrompt},
			contains: []string{"-p", "--verbose", "--output-format", "stream-json"},
			last:     testPrompt,
		},
		{
			name:     "with model",
			session:  agentrun.Session{Model: testModel, Prompt: testPrompt},
			contains: []string{"--model", testModel},
			last:     testPrompt,
		},
		{
			name: "with system prompt",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionSystemPrompt: testSystemPrompt},
			},
			contains: []string{"--system-prompt", testSystemPrompt},
			last:     testPrompt,
		},
		{
			name: "with max turns",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionMaxTurns: "5"},
			},
			contains: []string{"--max-turns", "5"},
			last:     testPrompt,
		},
		{
			name: "with permission acceptEdits",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionPermissionMode: string(PermissionAcceptEdits)},
			},
			contains: []string{"--permission-mode", "acceptEdits"},
			last:     testPrompt,
		},
		{
			name: "all options",
			session: agentrun.Session{
				Model:  testModel,
				Prompt: testPrompt,
				Options: map[string]string{
					OptionSystemPrompt:   testSystemPrompt,
					OptionPermissionMode: string(PermissionBypassAll),
					OptionMaxTurns:       "10",
				},
			},
			contains: []string{
				"--model", testModel,
				"--system-prompt", testSystemPrompt,
				"--permission-mode", "bypassPermissions",
				"--max-turns", "10",
			},
			last: testPrompt,
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary, args := b.SpawnArgs(tt.session)
			if binary != defaultBinary {
				t.Errorf("binary = %q, want %q", binary, defaultBinary)
			}
			assertArgs(t, args, tt.contains, nil, tt.last, false)
		})
	}
}

func TestSpawnArgs_SkipsInvalid(t *testing.T) {
	tests := []struct {
		name       string
		session    agentrun.Session
		excludes   []string
		last       string
		noNullByte bool
	}{
		{
			name: "permission default omitted",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionPermissionMode: string(PermissionDefault)},
			},
			excludes: []string{"--permission-mode"},
			last:     testPrompt,
		},
		{
			name: "invalid permission silently skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionPermissionMode: "invalid"},
			},
			excludes: []string{"--permission-mode"},
			last:     testPrompt,
		},
		{
			name: "invalid max turns skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionMaxTurns: "abc"},
			},
			excludes: []string{"--max-turns"},
			last:     testPrompt,
		},
		{
			name: "negative max turns skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionMaxTurns: "-1"},
			},
			excludes: []string{"--max-turns"},
			last:     testPrompt,
		},
		{
			name: "null byte in model skipped",
			session: agentrun.Session{
				Model:  "model\x00evil",
				Prompt: testPrompt,
			},
			noNullByte: true,
			last:       testPrompt,
		},
		{
			name: "null byte in option skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionSystemPrompt: "prompt\x00evil"},
			},
			noNullByte: true,
			last:       testPrompt,
		},
		{
			name:       "null byte in prompt omitted",
			session:    agentrun.Session{Prompt: "prompt\x00evil"},
			noNullByte: true,
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, args := b.SpawnArgs(tt.session)
			assertArgs(t, args, nil, tt.excludes, tt.last, tt.noNullByte)
		})
	}
}

func assertArgs(t *testing.T, args, contains, excludes []string, last string, noNullByte bool) {
	t.Helper()
	joined := strings.Join(args, " ")
	for _, c := range contains {
		if !strings.Contains(joined, c) {
			t.Errorf("args missing %q in: %v", c, args)
		}
	}
	for _, e := range excludes {
		if strings.Contains(joined, e) {
			t.Errorf("args should not contain %q: %v", e, args)
		}
	}
	if last != "" && args[len(args)-1] != last {
		t.Errorf("last arg = %q, want %q", args[len(args)-1], last)
	}
	if noNullByte && strings.Contains(joined, "\x00") {
		t.Errorf("null byte should be skipped: %v", args)
	}
}

func TestSpawnArgs_IgnoresResumeID(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Prompt:  testPrompt,
		Options: map[string]string{OptionResumeID: testResumeID},
	}
	_, args := b.SpawnArgs(session)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--resume") {
		t.Errorf("SpawnArgs must not use OptionResumeID: %v", args)
	}
}

// --- StreamArgs tests ---

func TestStreamArgs(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Model: testModel,
		Options: map[string]string{
			OptionSystemPrompt:   testSystemPrompt,
			OptionPermissionMode: string(PermissionAcceptEdits),
			OptionMaxTurns:       "5",
		},
	}
	binary, args := b.StreamArgs(session)
	if binary != defaultBinary {
		t.Errorf("binary = %q, want %q", binary, defaultBinary)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--input-format", "stream-json", "--model", testModel} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q in: %v", want, args)
		}
	}
	// StreamArgs must not have a trailing prompt.
	last := args[len(args)-1]
	if last == testPrompt {
		t.Errorf("StreamArgs should not have trailing prompt")
	}
}

// --- ResumeArgs tests ---

func TestResumeArgs(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{OptionResumeID: testResumeID},
	}
	binary, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if binary != defaultBinary {
		t.Errorf("binary = %q, want %q", binary, defaultBinary)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--resume "+testResumeID) {
		t.Errorf("args missing --resume: %v", args)
	}
	if args[len(args)-1] != testPrompt {
		t.Errorf("last arg = %q, want %q", args[len(args)-1], testPrompt)
	}
}

func TestResumeArgs_NoResumeID(t *testing.T) {
	b := New()
	_, _, err := b.ResumeArgs(agentrun.Session{}, testPrompt)
	if err == nil {
		t.Fatal("expected error for missing resume ID")
	}
	if !strings.Contains(err.Error(), "resume_id") {
		t.Errorf("error should mention resume_id: %v", err)
	}
}

func TestResumeArgs_InvalidPermission(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:       testResumeID,
			OptionPermissionMode: "invalid",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for invalid permission")
	}
	if !strings.Contains(err.Error(), "unknown permission mode") {
		t.Errorf("error should mention unknown permission mode: %v", err)
	}
}

func TestResumeArgs_WithPermission(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:       testResumeID,
			OptionPermissionMode: string(PermissionBypassAll),
		},
	}
	_, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--permission-mode bypassPermissions") {
		t.Errorf("args missing permission-mode: %v", args)
	}
}

func TestResumeArgs_DefaultPermissionOmitted(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:       testResumeID,
			OptionPermissionMode: string(PermissionDefault),
		},
	}
	_, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--permission-mode") {
		t.Errorf("default permission should be omitted: %v", args)
	}
}

func TestResumeArgs_NullByteResumeID(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{OptionResumeID: "conv\x00evil"},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for null byte in resume ID")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention null bytes: %v", err)
	}
}

func TestResumeArgs_NullByteInitialPrompt(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{OptionResumeID: testResumeID},
	}
	_, _, err := b.ResumeArgs(session, "prompt\x00evil")
	if err == nil {
		t.Fatal("expected error for null byte in initial prompt")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention null bytes: %v", err)
	}
}

func TestResumeArgs_NullBytePermission(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:       testResumeID,
			OptionPermissionMode: "bypassAll\x00evil",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for null byte in permission")
	}
}

func TestResumeArgs_WithModel(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Model:   testModel,
		Options: map[string]string{OptionResumeID: testResumeID},
	}
	_, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--model "+testModel) {
		t.Errorf("args missing --model: %v", args)
	}
	if !strings.Contains(joined, "--resume "+testResumeID) {
		t.Errorf("args missing --resume: %v", args)
	}
}

func TestResumeArgs_WithSystemPrompt(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:     testResumeID,
			OptionSystemPrompt: testSystemPrompt,
		},
	}
	_, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--system-prompt "+testSystemPrompt) {
		t.Errorf("args missing --system-prompt: %v", args)
	}
}

// --- FormatInput tests ---

func TestFormatInput(t *testing.T) {
	b := New()
	data, err := b.FormatInput(testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data[len(data)-1] != '\n' {
		t.Error("output should end with newline")
	}
	var parsed map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["type"] != "user" {
		t.Errorf("type = %v, want user", parsed["type"])
	}
	msg, ok := parsed["message"].(map[string]any)
	if !ok {
		t.Fatal("missing message field")
	}
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
	if msg["content"] != testPrompt {
		t.Errorf("content = %v, want %q", msg["content"], testPrompt)
	}
}

func TestFormatInput_SpecialChars(t *testing.T) {
	b := New()
	input := `line1\nline2 "quotes" <html> 日本語`
	data, err := b.FormatInput(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify round-trip: parse JSON and check content is preserved.
	var parsed map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg, ok := parsed["message"].(map[string]any)
	if !ok {
		t.Fatal("missing message field")
	}
	if msg["content"] != input {
		t.Errorf("content = %q, want %q", msg["content"], input)
	}
}

func TestFormatInput_NullBytes(t *testing.T) {
	b := New()
	_, err := b.FormatInput("hello\x00world")
	if err == nil {
		t.Fatal("expected error for null bytes")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention null bytes: %v", err)
	}
}

func TestFormatInput_Empty(t *testing.T) {
	b := New()
	data, err := b.FormatInput("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty message should still produce output")
	}
}

// --- Permission mapping tests ---

func TestMapPermission(t *testing.T) {
	tests := []struct {
		input   PermissionMode
		want    string
		wantErr bool
	}{
		{PermissionDefault, "default", false},
		{PermissionAcceptEdits, "acceptEdits", false},
		{PermissionBypassAll, "bypassPermissions", false},
		{PermissionPlan, "plan", false},
		{"invalid", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got, err := mapPermission(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if tt.wantErr && err != nil {
				if !strings.Contains(err.Error(), "valid:") {
					t.Errorf("error should list valid values: %v", err)
				}
			}
		})
	}
}

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
	assertRawPopulated(t, msg)
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
	assertRawPopulated(t, msg)
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
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me read that."},{"type":"tool_use","name":"Read","input":{"path":"/tmp/file"}}]}}`
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
	if inputMap["path"] != "/tmp/file" {
		t.Errorf("tool input path = %v, want /tmp/file", inputMap["path"])
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
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":100,"output_tokens":50}}}`
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
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`
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
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":0,"output_tokens":0}}}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Usage != nil {
		t.Errorf("zero usage should be nil, got %+v", msg.Usage)
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
	if msg.Content != "rate_limit: Too many requests" {
		t.Errorf("content = %q, want %q", msg.Content, "rate_limit: Too many requests")
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

// --- Version helper tests ---
// These test unexported helpers that will move to production code when
// the Validator interface is added (#68).

const minVersion = "2.1.25"

func parseVersionString(s string) (string, error) {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty version string")
	}
	v := parts[0]
	segments := strings.Split(v, ".")
	if len(segments) != 3 {
		return "", fmt.Errorf("invalid version format: %s", s)
	}
	for _, seg := range segments {
		if _, err := strconv.Atoi(seg); err != nil {
			return "", fmt.Errorf("invalid version format: %s", s)
		}
	}
	return v, nil
}

func semverLessThan(a, b string) bool {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	if len(aParts) != 3 || len(bParts) != 3 {
		return false
	}
	for i := range 3 {
		aNum, errA := strconv.Atoi(aParts[i])
		bNum, errB := strconv.Atoi(bParts[i])
		if errA != nil || errB != nil {
			return false
		}
		if aNum < bNum {
			return true
		}
		if aNum > bNum {
			return false
		}
	}
	return false
}

func TestParseVersionString(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"2.1.25", "2.1.25", false},
		{"2.1.25 (Claude Code)", "2.1.25", false},
		{"10.20.30", "10.20.30", false},
		{"", "", true},
		{"abc", "", true},
		{"1.2", "", true},
		{"1.2.3.4", "", true},
		{"a.b.c", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseVersionString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSemverLessThan(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"2.1.24", "2.1.25", true},
		{"2.1.25", "2.1.25", false},
		{"2.1.26", "2.1.25", false},
		{"1.0.0", "2.0.0", true},
		{"2.0.0", "1.0.0", false},
		{"2.0.0", "2.1.0", true},
		{"invalid", "2.1.25", false},
		{"2.1.25", "invalid", false},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := semverLessThan(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("semverLessThan(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMinVersion(t *testing.T) {
	if minVersion != "2.1.25" {
		t.Errorf("minVersion = %q, want %q", minVersion, "2.1.25")
	}
}

// --- Integration test ---

func TestEngineWiring(t *testing.T) {
	b := New()
	engine := cli.NewEngine(b)
	// Validate should fail because "claude" binary is likely not on PATH in CI.
	err := engine.Validate()
	if err == nil {
		// If claude IS available, that's fine too.
		return
	}
	if !errors.Is(err, agentrun.ErrUnavailable) {
		t.Errorf("expected ErrUnavailable, got %v", err)
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
