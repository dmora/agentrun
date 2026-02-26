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

func TestSpawnArgs_ModelLeadingDash(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{Prompt: testPrompt, Model: "-evil"})
	for _, a := range args {
		if a == "--model" {
			t.Error("--model should be omitted for leading-dash model")
		}
		if a == "-evil" {
			t.Error("leading-dash model should not appear in args")
		}
	}
}

func TestSpawnArgs_IgnoresResumeID(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Prompt:  testPrompt,
		Options: map[string]string{agentrun.OptionResumeID: testResumeID},
	}
	_, args := b.SpawnArgs(session)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--resume") {
		t.Errorf("SpawnArgs must not use agentrun.OptionResumeID: %v", args)
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

func TestStreamArgs_ResumeID(t *testing.T) {
	tests := []struct {
		name       string
		resumeID   string
		wantResume bool
	}{
		{"valid", testResumeID, true},
		{"invalid_skipped", "has spaces!", false},
		{"null_byte_skipped", "conv-abc\x00evil", false},
		{"empty_skipped", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			_, args := b.StreamArgs(agentrun.Session{
				Options: map[string]string{agentrun.OptionResumeID: tt.resumeID},
			})
			joined := strings.Join(args, " ")
			hasResume := strings.Contains(joined, "--resume")
			if hasResume != tt.wantResume {
				t.Errorf("StreamArgs --resume present = %v, want %v; args: %v", hasResume, tt.wantResume, args)
			}
			if tt.wantResume && !strings.Contains(joined, "--resume "+tt.resumeID) {
				t.Errorf("StreamArgs should include --resume %q, got: %v", tt.resumeID, args)
			}
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
		Options: map[string]string{agentrun.OptionResumeID: testResumeID},
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

func TestResumeArgs_InvalidResumeIDFormat(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: "has spaces!"},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for invalid resume_id format")
	}
	if !strings.Contains(err.Error(), "invalid resume_id format") {
		t.Errorf("error should mention invalid format: %v", err)
	}
}

func TestResumeArgs_InvalidPermission(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			agentrun.OptionResumeID: testResumeID,
			OptionPermissionMode:    "invalid",
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
			agentrun.OptionResumeID: testResumeID,
			OptionPermissionMode:    string(PermissionAcceptEdits),
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
			agentrun.OptionResumeID: testResumeID,
			OptionPermissionMode:    string(PermissionDefault),
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
		Options: map[string]string{agentrun.OptionResumeID: "conv-abc\x00evil"},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for null byte in resume_id")
	}
}

func TestResumeArgs_NullByteInitialPrompt(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: testResumeID},
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
			agentrun.OptionResumeID: testResumeID,
			OptionPermissionMode:    "bypassAll\x00evil",
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
		Options: map[string]string{agentrun.OptionResumeID: testResumeID},
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
			agentrun.OptionResumeID:     testResumeID,
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
			agentrun.OptionResumeID:       testResumeID,
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
			agentrun.OptionResumeID:       testResumeID,
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
			agentrun.OptionResumeID:       testResumeID,
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
			agentrun.OptionResumeID:       testResumeID,
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
			agentrun.OptionResumeID: testResumeID,
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
			agentrun.OptionResumeID: testResumeID,
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
			agentrun.OptionResumeID: testResumeID,
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

// --- Mode and HITL tests (SpawnArgs) ---

func TestSpawnArgs_ModeAndHITL(t *testing.T) {
	tests := []struct {
		name     string
		opts     map[string]string
		contains []string
		excludes []string
	}{
		{
			name:     "mode plan",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModePlan)},
			contains: []string{"--permission-mode", "plan"},
		},
		{
			name:     "mode act",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModeAct)},
			excludes: []string{"--permission-mode"},
		},
		{
			name:     "hitl off",
			opts:     map[string]string{agentrun.OptionHITL: string(agentrun.HITLOff)},
			contains: []string{"--permission-mode", "bypassPermissions"},
		},
		{
			name:     "plan overrides backend permission",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModePlan), OptionPermissionMode: string(PermissionBypassAll)},
			contains: []string{"--permission-mode", "plan"},
		},
		{
			name:     "hitl off overrides backend permission",
			opts:     map[string]string{agentrun.OptionHITL: string(agentrun.HITLOff), OptionPermissionMode: string(PermissionDefault)},
			contains: []string{"--permission-mode", "bypassPermissions"},
		},
		{
			name:     "act plus hitl on no bypass",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModeAct), agentrun.OptionHITL: string(agentrun.HITLOn), OptionPermissionMode: string(PermissionBypassAll)},
			excludes: []string{"--permission-mode"},
		},
		{
			name:     "no root uses backend acceptEdits",
			opts:     map[string]string{OptionPermissionMode: string(PermissionAcceptEdits)},
			contains: []string{"--permission-mode", "acceptEdits"},
		},
		{
			name:     "plan plus hitl off plan wins",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModePlan), agentrun.OptionHITL: string(agentrun.HITLOff)},
			contains: []string{"--permission-mode", "plan"},
		},
		{
			name:     "hitl on alone no flag",
			opts:     map[string]string{agentrun.OptionHITL: string(agentrun.HITLOn)},
			excludes: []string{"--permission-mode"},
		},
		{
			name:     "act alone suppresses backend permission",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModeAct), OptionPermissionMode: string(PermissionAcceptEdits)},
			excludes: []string{"--permission-mode"},
		},
		{
			name:     "invalid mode silently skipped",
			opts:     map[string]string{agentrun.OptionMode: "invalid"},
			excludes: []string{"--permission-mode"},
		},
		{
			name:     "invalid hitl silently skipped",
			opts:     map[string]string{agentrun.OptionHITL: "invalid"},
			excludes: []string{"--permission-mode"},
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := agentrun.Session{Prompt: testPrompt, Options: tt.opts}
			_, args := b.SpawnArgs(session)
			assertArgs(t, args, tt.contains, tt.excludes, testPrompt, false)
		})
	}
}

// --- Mode and HITL tests (StreamArgs) ---

func TestStreamArgs_ModeAndHITL(t *testing.T) {
	tests := []struct {
		name     string
		opts     map[string]string
		contains []string
		excludes []string
	}{
		{
			name:     "mode plan",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModePlan)},
			contains: []string{"--permission-mode", "plan"},
		},
		{
			name:     "hitl off",
			opts:     map[string]string{agentrun.OptionHITL: string(agentrun.HITLOff)},
			contains: []string{"--permission-mode", "bypassPermissions"},
		},
		{
			name:     "act plus hitl on",
			opts:     map[string]string{agentrun.OptionMode: string(agentrun.ModeAct), agentrun.OptionHITL: string(agentrun.HITLOn)},
			excludes: []string{"--permission-mode"},
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := agentrun.Session{Options: tt.opts}
			_, args := b.StreamArgs(session)
			assertArgs(t, args, tt.contains, tt.excludes, "", false)
		})
	}
}

// --- Mode and HITL tests (ResumeArgs) ---

func TestResumeArgs_ModeAndHITL(t *testing.T) {
	tests := []struct {
		name     string
		opts     map[string]string
		contains []string
		excludes []string
	}{
		{
			name:     "mode plan",
			opts:     map[string]string{agentrun.OptionResumeID: testResumeID, agentrun.OptionMode: string(agentrun.ModePlan)},
			contains: []string{"--permission-mode", "plan"},
		},
		{
			name:     "hitl off",
			opts:     map[string]string{agentrun.OptionResumeID: testResumeID, agentrun.OptionHITL: string(agentrun.HITLOff)},
			contains: []string{"--permission-mode", "bypassPermissions"},
		},
		{
			name:     "act plus hitl on",
			opts:     map[string]string{agentrun.OptionResumeID: testResumeID, agentrun.OptionMode: string(agentrun.ModeAct), agentrun.OptionHITL: string(agentrun.HITLOn)},
			excludes: []string{"--permission-mode"},
		},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := agentrun.Session{Options: tt.opts}
			_, args, err := b.ResumeArgs(session, testPrompt)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertArgs(t, args, tt.contains, tt.excludes, testPrompt, false)
		})
	}
}

func TestResumeArgs_PlanIgnoresInvalidPermission(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{
			agentrun.OptionResumeID: testResumeID,
			agentrun.OptionMode:     string(agentrun.ModePlan),
			OptionPermissionMode:    "invalid",
		},
	}
	_, args, err := b.ResumeArgs(session, testPrompt)
	if err != nil {
		t.Fatalf("root options set — invalid backend permission should be ignored: %v", err)
	}
	assertArgs(t, args, []string{"--permission-mode", "plan"}, nil, testPrompt, false)
}

func TestResumeArgs_InvalidMode(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: testResumeID, agentrun.OptionMode: "invalid"},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("error should mention unknown mode: %v", err)
	}
}

func TestResumeArgs_InvalidHITL(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: testResumeID, agentrun.OptionHITL: "invalid"},
	}
	_, _, err := b.ResumeArgs(session, testPrompt)
	if err == nil {
		t.Fatal("expected error for invalid hitl")
	}
	if !strings.Contains(err.Error(), "unknown hitl") {
		t.Errorf("error should mention unknown hitl: %v", err)
	}
}

// --- resolvePermissionFlag contract tests ---

func TestResolvePermissionFlag_RootOptions(t *testing.T) {
	tests := []struct {
		name   string
		opts   map[string]string
		want   string
		wantOK bool
	}{
		{
			name:   "plan",
			opts:   map[string]string{agentrun.OptionMode: "plan"},
			want:   "plan",
			wantOK: true,
		},
		{
			name:   "act",
			opts:   map[string]string{agentrun.OptionMode: "act"},
			want:   "",
			wantOK: false,
		},
		{
			name:   "hitl off",
			opts:   map[string]string{agentrun.OptionHITL: "off"},
			want:   "bypassPermissions",
			wantOK: true,
		},
		{
			name:   "hitl on",
			opts:   map[string]string{agentrun.OptionHITL: "on"},
			want:   "",
			wantOK: false,
		},
		{
			name:   "plan plus hitl off — plan wins",
			opts:   map[string]string{agentrun.OptionMode: "plan", agentrun.OptionHITL: "off"},
			want:   "plan",
			wantOK: true,
		},
		{
			name:   "act plus hitl on — default",
			opts:   map[string]string{agentrun.OptionMode: "act", agentrun.OptionHITL: "on"},
			want:   "",
			wantOK: false,
		},
		{
			name:   "act plus hitl off — bypass",
			opts:   map[string]string{agentrun.OptionMode: "act", agentrun.OptionHITL: "off"},
			want:   "bypassPermissions",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolvePermissionFlag(tt.opts)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("resolvePermissionFlag(%v) = (%q, %v), want (%q, %v)", tt.opts, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestResolvePermissionFlag_RootSuppressesBackend(t *testing.T) {
	tests := []struct {
		name   string
		opts   map[string]string
		want   string
		wantOK bool
	}{
		{
			name:   "plan ignores bypassAll",
			opts:   map[string]string{agentrun.OptionMode: "plan", OptionPermissionMode: "bypassAll"},
			want:   "plan",
			wantOK: true,
		},
		{
			name:   "act ignores acceptEdits",
			opts:   map[string]string{agentrun.OptionMode: "act", OptionPermissionMode: "acceptEdits"},
			want:   "",
			wantOK: false,
		},
		{
			name:   "hitl on ignores bypassAll",
			opts:   map[string]string{agentrun.OptionHITL: "on", OptionPermissionMode: "bypassAll"},
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolvePermissionFlag(tt.opts)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("resolvePermissionFlag(%v) = (%q, %v), want (%q, %v)", tt.opts, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestResolvePermissionFlag_BackendOnly(t *testing.T) {
	tests := []struct {
		name   string
		opts   map[string]string
		want   string
		wantOK bool
	}{
		{
			name:   "acceptEdits",
			opts:   map[string]string{OptionPermissionMode: "acceptEdits"},
			want:   "acceptEdits",
			wantOK: true,
		},
		{
			name:   "bypassAll",
			opts:   map[string]string{OptionPermissionMode: "bypassAll"},
			want:   "bypassPermissions",
			wantOK: true,
		},
		{
			name:   "plan",
			opts:   map[string]string{OptionPermissionMode: "plan"},
			want:   "plan",
			wantOK: true,
		},
		{
			name:   "default omitted",
			opts:   map[string]string{OptionPermissionMode: "default"},
			want:   "",
			wantOK: false,
		},
		{
			name:   "empty",
			opts:   map[string]string{},
			want:   "",
			wantOK: false,
		},
		{
			name:   "invalid silently skipped",
			opts:   map[string]string{OptionPermissionMode: "bogus"},
			want:   "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolvePermissionFlag(tt.opts)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("resolvePermissionFlag(%v) = (%q, %v), want (%q, %v)", tt.opts, got, ok, tt.want, tt.wantOK)
			}
		})
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
