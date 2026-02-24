package opencode

import (
	"strings"
	"testing"

	"github.com/dmora/agentrun"
)

// Test constants.
const testSessionID = "ses_abc123def456ghi789jklmno"

// --- Constructor ---

func TestNew_Default(t *testing.T) {
	b := New()
	if b.binary != defaultBinary {
		t.Errorf("binary = %q, want %q", b.binary, defaultBinary)
	}
}

func TestNew_WithBinary(t *testing.T) {
	b := New(WithBinary("/usr/local/bin/opencode"))
	if b.binary != "/usr/local/bin/opencode" {
		t.Errorf("binary = %q, want %q", b.binary, "/usr/local/bin/opencode")
	}
}

func TestNew_WithBinary_Empty(t *testing.T) {
	b := New(WithBinary(""))
	if b.binary != defaultBinary {
		t.Errorf("empty WithBinary should keep default, got %q", b.binary)
	}
}

// --- SpawnArgs ---

func TestSpawnArgs(t *testing.T) {
	tests := []struct {
		name    string
		session agentrun.Session
		want    []string
	}{
		{
			name:    "Minimal",
			session: agentrun.Session{Prompt: "hello"},
			want:    []string{"run", "--format", "json", "hello"},
		},
		{
			name:    "WithModel",
			session: agentrun.Session{Prompt: "hi", Model: "anthropic/claude-sonnet"},
			want:    []string{"run", "--format", "json", "--model", "anthropic/claude-sonnet", "hi"},
		},
		{
			name:    "WithAgent",
			session: agentrun.Session{Prompt: "hi", AgentID: "coder"},
			want:    []string{"run", "--format", "json", "--agent", "coder", "hi"},
		},
		{
			name: "WithThinking",
			session: agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{agentrun.OptionThinkingBudget: "10000"},
			},
			want: []string{"run", "--format", "json", "--thinking", "hi"},
		},
		{
			name: "WithVariant",
			session: agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{OptionVariant: string(VariantHigh)},
			},
			want: []string{"run", "--format", "json", "--variant", "high", "hi"},
		},
		{
			name: "WithTitle",
			session: agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{OptionTitle: "my session"},
			},
			want: []string{"run", "--format", "json", "--title", "my session", "hi"},
		},
		{
			name:    "PromptAlwaysLast",
			session: agentrun.Session{Prompt: "the prompt", Model: "m", AgentID: "a"},
			want:    []string{"run", "--format", "json", "--model", "m", "--agent", "a", "the prompt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			binary, args := b.SpawnArgs(tt.session)
			if binary != defaultBinary {
				t.Errorf("binary = %q, want %q", binary, defaultBinary)
			}
			assertArgsEqual(t, args, tt.want)
		})
	}
}

func TestSpawnArgs_WithResumeID(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "hi",
		Options: map[string]string{agentrun.OptionResumeID: testSessionID},
	})
	assertContains(t, args, "--session")
	assertContains(t, args, testSessionID)
}

func TestSpawnArgs_ResumeID_InvalidSkipped(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "hi",
		Options: map[string]string{agentrun.OptionResumeID: "not-valid-format"},
	})
	assertNotContains(t, args, "--session")
}

func TestSpawnArgs_ResumeID_NullByteSkipped(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "hi",
		Options: map[string]string{agentrun.OptionResumeID: "ses_abc\x00evil1234567890123"},
	})
	assertNotContains(t, args, "--session")
}

func TestSpawnArgs_TitleOverLimit(t *testing.T) {
	longTitle := strings.Repeat("x", maxTitleLen+1)
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "hi",
		Options: map[string]string{OptionTitle: longTitle},
	})
	for _, a := range args {
		if a == "--title" {
			t.Error("--title should be omitted for titles exceeding maxTitleLen")
		}
	}
}

func TestSpawnArgs_NullBytePrompt(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{Prompt: "hello\x00world"})
	// Prompt should be silently omitted.
	last := args[len(args)-1]
	if last == "hello\x00world" {
		t.Error("null-byte prompt should be silently omitted")
	}
}

func TestSpawnArgs_NullByteModel(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{Prompt: "hi", Model: "bad\x00model"})
	for _, a := range args {
		if a == "--model" {
			t.Error("--model should be omitted for null-byte model")
		}
	}
}

func TestSpawnArgs_NullByteAgentID(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{Prompt: "hi", AgentID: "bad\x00agent"})
	for _, a := range args {
		if a == "--agent" {
			t.Error("--agent should be omitted for null-byte agent ID")
		}
	}
}

func TestSpawnArgs_NullByteVariant(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "hi",
		Options: map[string]string{OptionVariant: "high\x00"},
	})
	for _, a := range args {
		if a == "--variant" {
			t.Error("--variant should be omitted for null-byte variant")
		}
	}
}

func TestSpawnArgs_IgnoredOptions(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt: "hi",
		Options: map[string]string{
			agentrun.OptionMode:         string(agentrun.ModePlan),
			agentrun.OptionHITL:         string(agentrun.HITLOff),
			agentrun.OptionSystemPrompt: "you are a bot",
			agentrun.OptionMaxTurns:     "5",
		},
	})
	forbidden := []string{"--mode", "--hitl", "--system-prompt", "--max-turns", "--permission-mode"}
	for _, f := range forbidden {
		for _, a := range args {
			if a == f {
				t.Errorf("flag %q should be silently ignored", f)
			}
		}
	}
}

// --- ResumeArgs ---

func TestResumeArgs_StoredSessionID(t *testing.T) {
	b := New()
	// Simulate auto-capture.
	sid := testSessionID
	b.sessionID.CompareAndSwap(nil, &sid)

	binary, args, err := b.ResumeArgs(agentrun.Session{}, "continue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if binary != defaultBinary {
		t.Errorf("binary = %q, want %q", binary, defaultBinary)
	}
	assertContains(t, args, "--session")
	assertContains(t, args, sid)
	if args[len(args)-1] != "continue" {
		t.Errorf("message should be last arg, got %q", args[len(args)-1])
	}
}

func TestResumeArgs_OptionResumeID(t *testing.T) {
	b := New()
	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: "ses_explicit123456789012345"},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, "ses_explicit123456789012345")
}

func TestResumeArgs_StoredTakesPrecedence(t *testing.T) {
	b := New()
	stored := "ses_stored12345678901234567"
	b.sessionID.CompareAndSwap(nil, &stored)

	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: "ses_option12345678901234567"},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, stored)
	assertNotContains(t, args, "ses_option12345678901234567")
}

func TestResumeArgs_NoSessionID(t *testing.T) {
	b := New()
	_, _, err := b.ResumeArgs(agentrun.Session{}, "msg")
	if err == nil {
		t.Fatal("expected error for missing session ID")
	}
	assertStringContains(t, err.Error(), "no session ID")
}

func TestResumeArgs_NullByteSessionID(t *testing.T) {
	b := New()
	_, _, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: "ses_abc\x00def"},
	}, "msg")
	if err == nil {
		t.Fatal("expected error for null-byte session ID")
	}
	assertStringContains(t, err.Error(), "invalid session ID format")
}

func TestResumeArgs_NullByteMessage(t *testing.T) {
	b := New()
	sid := testSessionID
	b.sessionID.CompareAndSwap(nil, &sid)

	_, _, err := b.ResumeArgs(agentrun.Session{}, "hello\x00world")
	if err == nil {
		t.Fatal("expected error for null-byte message")
	}
	assertStringContains(t, err.Error(), "null bytes")
}

func TestResumeArgs_WithFork(t *testing.T) {
	b := New()
	sid := testSessionID
	b.sessionID.CompareAndSwap(nil, &sid)

	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{OptionFork: "true"},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, "--fork")
}

func TestResumeArgs_WithModel(t *testing.T) {
	b := New()
	sid := testSessionID
	b.sessionID.CompareAndSwap(nil, &sid)

	_, args, err := b.ResumeArgs(agentrun.Session{Model: "gpt-4o"}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, "--model")
	assertContains(t, args, "gpt-4o")
}

func TestResumeArgs_WithThinking(t *testing.T) {
	b := New()
	sid := testSessionID
	b.sessionID.CompareAndSwap(nil, &sid)

	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionThinkingBudget: "any"},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, "--thinking")
}

func TestResumeArgs_IgnoredOptions(t *testing.T) {
	b := New()
	sid := testSessionID
	b.sessionID.CompareAndSwap(nil, &sid)

	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{
			agentrun.OptionMode: string(agentrun.ModePlan),
			agentrun.OptionHITL: string(agentrun.HITLOff),
		},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	forbidden := []string{"--mode", "--hitl", "--permission-mode"}
	for _, f := range forbidden {
		for _, a := range args {
			if a == f {
				t.Errorf("flag %q should be silently ignored", f)
			}
		}
	}
}

func TestResumeArgs_InvalidSessionIDFormat(t *testing.T) {
	b := New()
	_, _, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: "not-a-valid-session-id"},
	}, "msg")
	if err == nil {
		t.Fatal("expected error for invalid session ID format")
	}
	assertStringContains(t, err.Error(), "invalid session ID format")
}

func TestResumeArgs_EmptyMessage(t *testing.T) {
	b := New()
	sid := testSessionID
	b.sessionID.CompareAndSwap(nil, &sid)

	_, args, err := b.ResumeArgs(agentrun.Session{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Last arg should NOT be an empty string.
	last := args[len(args)-1]
	if last == "" {
		t.Error("empty initialPrompt should not produce a trailing empty arg")
	}
}

// --- SessionID ---

func TestSessionID_Empty(t *testing.T) {
	b := New()
	if id := b.SessionID(); id != "" {
		t.Errorf("SessionID() = %q, want empty", id)
	}
}

func TestSessionID_AfterStore(t *testing.T) {
	b := New()
	sid := "ses_test1234567890123456789"
	b.sessionID.CompareAndSwap(nil, &sid)
	if id := b.SessionID(); id != sid {
		t.Errorf("SessionID() = %q, want %q", id, sid)
	}
}

// --- validateSessionID ---

func Test_validateSessionID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"valid_short", "ses_abcdefghij1234567890", false},
		{"valid_long", "ses_abcdefghij1234567890abcdefghij12345678", false},
		{"missing_prefix", "abc123def456ghi789jklmno", true},
		{"too_short", "ses_abc", true},
		{"empty", "", true},
		{"special_chars", "ses_abc!@#$%^&*()1234567890", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSessionID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSessionID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
	}
}

// --- resolveSessionID precedence ---

func TestResolveSessionID_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		stored  string
		optVal  string
		want    string
		wantSrc string
	}{
		{"stored only", "ses_stored12345678901234567", "", "ses_stored12345678901234567", "stored"},
		{"option only", "", "ses_option12345678901234567", "ses_option12345678901234567", "option"},
		{"stored wins", "ses_stored12345678901234567", "ses_option12345678901234567", "ses_stored12345678901234567", "stored"},
		{"neither", "", "", "", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			if tt.stored != "" {
				b.sessionID.CompareAndSwap(nil, &tt.stored)
			}
			session := agentrun.Session{}
			if tt.optVal != "" {
				session.Options = map[string]string{agentrun.OptionResumeID: tt.optVal}
			}
			got := b.resolveSessionID(session)
			if got != tt.want {
				t.Errorf("resolveSessionID() = %q, want %q (%s)", got, tt.want, tt.wantSrc)
			}
		})
	}
}

// --- Test helpers ---

func assertArgsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("args length = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q\ngot:  %v\nwant: %v", i, got[i], want[i], got, want)
			return
		}
	}
}

func assertContains(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("args %v does not contain %q", args, want)
}

func assertNotContains(t *testing.T, args []string, unwanted string) {
	t.Helper()
	for _, a := range args {
		if a == unwanted {
			t.Errorf("args %v should not contain %q", args, unwanted)
			return
		}
	}
}

func assertStringContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%q does not contain %q", s, substr)
	}
}
