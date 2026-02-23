package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dmora/agentrun"
)

// --- SpawnArgs tests ---

func TestSpawnArgs_Base(t *testing.T) {
	tests := []struct {
		name     string
		session  agentrun.Session
		contains []string
		excludes []string
		last     string
	}{
		{
			name:     "minimal",
			session:  agentrun.Session{Prompt: testPrompt},
			contains: []string{"-p", "--verbose", "--output-format", "stream-json"},
			excludes: []string{"--include-partial-messages", "--input-format"},
			last:     testPrompt,
		},
		{
			name:     "with model",
			session:  agentrun.Session{Model: testModel, Prompt: testPrompt},
			contains: []string{"--model", testModel},
			excludes: []string{"--include-partial-messages", "--input-format"},
			last:     testPrompt,
		},
		{
			name: "with system prompt",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionSystemPrompt: testSystemPrompt},
			},
			contains: []string{"--system-prompt", testSystemPrompt},
			excludes: []string{"--include-partial-messages", "--input-format"},
			last:     testPrompt,
		},
		{
			name: "with max turns",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionMaxTurns: "5"},
			},
			contains: []string{"--max-turns", "5"},
			excludes: []string{"--include-partial-messages", "--input-format"},
			last:     testPrompt,
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary, args := b.SpawnArgs(tt.session)
			if binary != defaultBinary {
				t.Errorf("binary = %q, want %q", binary, defaultBinary)
			}
			assertArgs(t, args, tt.contains, tt.excludes, tt.last, false)
		})
	}
}

func TestSpawnArgs_Options(t *testing.T) {
	tests := []struct {
		name     string
		session  agentrun.Session
		contains []string
		last     string
	}{
		{
			name: "permission acceptEdits",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{OptionPermissionMode: string(PermissionAcceptEdits)},
			},
			contains: []string{"--permission-mode", "acceptEdits"},
			last:     testPrompt,
		},
		{
			name: "thinking budget",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionThinkingBudget: testThinkingBudget},
			},
			contains: []string{"--max-thinking-tokens", testThinkingBudget},
			last:     testPrompt,
		},
		{
			name: "thinking budget minimum",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionThinkingBudget: "1"},
			},
			contains: []string{"--max-thinking-tokens", "1"},
			last:     testPrompt,
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, args := b.SpawnArgs(tt.session)
			assertArgs(t, args, tt.contains, nil, tt.last, false)
		})
	}
}

func TestSpawnArgs_AllOptions(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Model:  testModel,
		Prompt: testPrompt,
		Options: map[string]string{
			agentrun.OptionSystemPrompt:   testSystemPrompt,
			OptionPermissionMode:          string(PermissionBypassAll),
			agentrun.OptionMaxTurns:       "10",
			agentrun.OptionThinkingBudget: testThinkingBudget,
		},
	}
	_, args := b.SpawnArgs(session)
	assertArgs(t, args, []string{
		"--model", testModel,
		"--system-prompt", testSystemPrompt,
		"--permission-mode", "bypassPermissions",
		"--max-turns", "10",
		"--max-thinking-tokens", testThinkingBudget,
	}, []string{"--include-partial-messages", "--input-format"}, testPrompt, false)
}

func TestSpawnArgs_SkipsInvalidValues(t *testing.T) {
	tests := []struct {
		name     string
		session  agentrun.Session
		excludes []string
		last     string
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
				Options: map[string]string{agentrun.OptionMaxTurns: "abc"},
			},
			excludes: []string{"--max-turns"},
			last:     testPrompt,
		},
		{
			name: "negative max turns skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionMaxTurns: "-1"},
			},
			excludes: []string{"--max-turns"},
			last:     testPrompt,
		},
		{
			name: "invalid thinking budget skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionThinkingBudget: "abc"},
			},
			excludes: []string{"--max-thinking-tokens"},
			last:     testPrompt,
		},
		{
			name: "negative thinking budget skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionThinkingBudget: "-1"},
			},
			excludes: []string{"--max-thinking-tokens"},
			last:     testPrompt,
		},
		{
			name: "zero thinking budget skipped",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionThinkingBudget: "0"},
			},
			excludes: []string{"--max-thinking-tokens"},
			last:     testPrompt,
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, args := b.SpawnArgs(tt.session)
			assertArgs(t, args, nil, tt.excludes, tt.last, false)
		})
	}
}

func TestSpawnArgs_SkipsNullBytes(t *testing.T) {
	tests := []struct {
		name    string
		session agentrun.Session
		last    string
	}{
		{
			name: "in model",
			session: agentrun.Session{
				Model:  "model\x00evil",
				Prompt: testPrompt,
			},
			last: testPrompt,
		},
		{
			name: "in option",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionSystemPrompt: "prompt\x00evil"},
			},
			last: testPrompt,
		},
		{
			name:    "in prompt",
			session: agentrun.Session{Prompt: "prompt\x00evil"},
		},
		{
			name: "in thinking budget",
			session: agentrun.Session{
				Prompt:  testPrompt,
				Options: map[string]string{agentrun.OptionThinkingBudget: "100\x00evil"},
			},
			last: testPrompt,
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, args := b.SpawnArgs(tt.session)
			assertArgs(t, args, nil, nil, tt.last, true)
		})
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
			agentrun.OptionSystemPrompt: testSystemPrompt,
			OptionPermissionMode:        string(PermissionAcceptEdits),
			agentrun.OptionMaxTurns:     "5",
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

func TestStreamArgs_WithSession(t *testing.T) {
	tests := []struct {
		name     string
		session  agentrun.Session
		contains []string
		excludes []string
	}{
		{
			name:     "minimal",
			session:  agentrun.Session{},
			contains: []string{"--input-format", "stream-json", "--include-partial-messages"},
		},
		{
			name:     "with model",
			session:  agentrun.Session{Model: testModel},
			contains: []string{"--model", testModel},
		},
		{
			name: "with system prompt",
			session: agentrun.Session{
				Options: map[string]string{agentrun.OptionSystemPrompt: testSystemPrompt},
			},
			contains: []string{"--system-prompt", testSystemPrompt},
		},
		{
			name: "with max turns",
			session: agentrun.Session{
				Options: map[string]string{agentrun.OptionMaxTurns: "5"},
			},
			contains: []string{"--max-turns", "5"},
		},
		{
			name: "with permission",
			session: agentrun.Session{
				Options: map[string]string{OptionPermissionMode: string(PermissionAcceptEdits)},
			},
			contains: []string{"--permission-mode", "acceptEdits"},
		},
		{
			name: "with thinking budget",
			session: agentrun.Session{
				Options: map[string]string{agentrun.OptionThinkingBudget: "8000"},
			},
			contains: []string{"--max-thinking-tokens"},
		},
		{
			name: "all options",
			session: agentrun.Session{
				Model: testModel,
				Options: map[string]string{
					agentrun.OptionSystemPrompt:   testSystemPrompt,
					OptionPermissionMode:          string(PermissionBypassAll),
					agentrun.OptionMaxTurns:       "10",
					agentrun.OptionThinkingBudget: testThinkingBudget,
				},
			},
			contains: []string{
				"--model", testModel,
				"--system-prompt", testSystemPrompt,
				"--permission-mode", "bypassPermissions",
				"--max-turns", "10",
				"--max-thinking-tokens", testThinkingBudget,
				"--include-partial-messages",
			},
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binary, args := b.StreamArgs(tt.session)
			if binary != defaultBinary {
				t.Errorf("binary = %q, want %q", binary, defaultBinary)
			}
			// StreamArgs must not have a trailing prompt.
			last := args[len(args)-1]
			if last == testPrompt {
				t.Errorf("StreamArgs should not have trailing prompt")
			}
			assertArgs(t, args, tt.contains, tt.excludes, "", false)
		})
	}
}

func TestStreamArgs_IncludesPartialMessages(t *testing.T) {
	b := New()
	_, args := b.StreamArgs(agentrun.Session{})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--include-partial-messages") {
		t.Errorf("default StreamArgs should include --include-partial-messages: %v", args)
	}
}

func TestStreamArgs_DisablePartialMessages(t *testing.T) {
	b := New(WithPartialMessages(false))
	_, args := b.StreamArgs(agentrun.Session{})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--include-partial-messages") {
		t.Errorf("StreamArgs should not include --include-partial-messages when disabled: %v", args)
	}
}

// --- ResumeArgs tests ---

func TestResumeArgs(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Prompt:  testPrompt,
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
		t.Errorf("last arg = %q, want %q (prompt must be last)", args[len(args)-1], testPrompt)
	}
}

func TestResumeArgs_NoResumeID(t *testing.T) {
	b := New()
	session := agentrun.Session{Prompt: testPrompt}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for missing resume_id")
	}
	if !strings.Contains(err.Error(), "missing resume_id") {
		t.Errorf("error should mention missing resume_id: %v", err)
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
			OptionPermissionMode: string(PermissionAcceptEdits),
		},
	}
	_, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--permission-mode acceptEdits") {
		t.Errorf("args missing --permission-mode: %v", args)
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
		Options: map[string]string{OptionResumeID: "conv-abc\x00evil"},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for null byte in resume_id")
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
}

func TestResumeArgs_WithSystemPrompt(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:              testResumeID,
			agentrun.OptionSystemPrompt: testSystemPrompt,
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

func TestResumeArgs_WithThinkingBudget(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:                testResumeID,
			agentrun.OptionThinkingBudget: testThinkingBudget,
		},
	}
	_, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--max-thinking-tokens "+testThinkingBudget) {
		t.Errorf("args missing --max-thinking-tokens: %v", args)
	}
	if !strings.Contains(joined, "--resume "+testResumeID) {
		t.Errorf("args missing --resume: %v", args)
	}
	if args[len(args)-1] != testPrompt {
		t.Errorf("last arg = %q, want %q (prompt must be last)", args[len(args)-1], testPrompt)
	}
}

func TestResumeArgs_InvalidThinkingBudget(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:                testResumeID,
			agentrun.OptionThinkingBudget: "abc",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for invalid thinking budget")
	}
	if !strings.Contains(err.Error(), "thinking budget") {
		t.Errorf("error should mention thinking budget: %v", err)
	}
}

func TestResumeArgs_NullByteThinkingBudget(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:                testResumeID,
			agentrun.OptionThinkingBudget: "100\x00evil",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for null byte in thinking budget")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention null bytes: %v", err)
	}
}

func TestResumeArgs_ZeroThinkingBudget(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:                testResumeID,
			agentrun.OptionThinkingBudget: "0",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for zero thinking budget")
	}
	if !strings.Contains(err.Error(), "thinking budget") {
		t.Errorf("error should mention thinking budget: %v", err)
	}
}

func TestResumeArgs_InvalidMaxTurns(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:          testResumeID,
			agentrun.OptionMaxTurns: "abc",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for invalid max turns")
	}
	if !strings.Contains(err.Error(), "max turns") {
		t.Errorf("error should mention max turns: %v", err)
	}
}

func TestResumeArgs_ZeroMaxTurns(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:          testResumeID,
			agentrun.OptionMaxTurns: "0",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for zero max turns")
	}
	if !strings.Contains(err.Error(), "max turns") {
		t.Errorf("error should mention max turns: %v", err)
	}
}

func TestResumeArgs_NullByteMaxTurns(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			OptionResumeID:          testResumeID,
			agentrun.OptionMaxTurns: "5\x00evil",
		},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for null byte in max turns")
	}
	if !strings.Contains(err.Error(), "null bytes") {
		t.Errorf("error should mention null bytes: %v", err)
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

// --- Helpers ---

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
