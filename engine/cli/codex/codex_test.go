package codex

import (
	"strings"
	"testing"

	"github.com/dmora/agentrun"
)

// Test constants.
const testThreadID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

// --- Constructor ---

func TestNew_Default(t *testing.T) {
	b := New()
	if b.binary != defaultBinary {
		t.Errorf("binary = %q, want %q", b.binary, defaultBinary)
	}
}

func TestNew_WithBinary(t *testing.T) {
	b := New(WithBinary("/usr/local/bin/codex"))
	if b.binary != "/usr/local/bin/codex" {
		t.Errorf("binary = %q, want %q", b.binary, "/usr/local/bin/codex")
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
			want:    []string{"exec", "--json", "--", "hello"},
		},
		{
			name:    "WithModel",
			session: agentrun.Session{Prompt: "hi", Model: "o3"},
			want:    []string{"exec", "--json", "-m", "o3", "--", "hi"},
		},
		{
			name: "WithEphemeral",
			session: agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{OptionEphemeral: "true"},
			},
			want: []string{"exec", "--json", "--ephemeral", "--", "hi"},
		},
		{
			name: "WithProfile",
			session: agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{OptionProfile: "myprofile"},
			},
			want: []string{"exec", "--json", "-p", "myprofile", "--", "hi"},
		},
		{
			name: "WithOutputSchema",
			session: agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{OptionOutputSchema: "schema.json"},
			},
			want: []string{"exec", "--json", "--output-schema", "schema.json", "--", "hi"},
		},
		{
			name: "WithSkipGitCheck",
			session: agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{OptionSkipGitCheck: "true"},
			},
			want: []string{"exec", "--json", "--skip-git-repo-check", "--", "hi"},
		},
		{
			name: "PromptAlwaysLast",
			session: agentrun.Session{
				Prompt:  "the prompt",
				Model:   "m",
				Options: map[string]string{OptionProfile: "p", OptionEphemeral: "1"},
			},
			want: []string{"exec", "--json", "-p", "p", "-m", "m", "--ephemeral", "--", "the prompt"},
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

func TestSpawnArgs_SubcommandSwitch(t *testing.T) {
	tests := []struct {
		name     string
		resumeID string
		wantExec bool
	}{
		{"UUID", testThreadID, false},
		{"thread_name", "my-thread", false},
		{"null_byte_skipped", "abc\x00def", true},
		{"empty_stays_exec", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			_, args := b.SpawnArgs(agentrun.Session{
				Prompt:  "hi",
				Options: map[string]string{agentrun.OptionResumeID: tt.resumeID},
			})
			isExecResume := len(args) >= 2 && args[0] == "exec" && args[1] == "resume"
			if tt.wantExec && isExecResume {
				t.Error("expected plain exec, got exec resume")
			}
			if !tt.wantExec && !isExecResume {
				t.Error("expected exec resume subcommand switch")
			}
		})
	}
}

func TestSpawnArgs_ResumeIDArgs(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "follow up",
		Options: map[string]string{agentrun.OptionResumeID: testThreadID},
	})
	// exec resume --json -- <thread_id> <prompt>
	want := []string{"exec", "resume", "--json", "--", testThreadID, "follow up"}
	assertArgsEqual(t, args, want)
}

func TestSpawnArgs_NullBytePrompt(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{Prompt: "hello\x00world"})
	// Null-byte prompt omitted; last arg should be "--" separator.
	last := args[len(args)-1]
	if last == "hello\x00world" {
		t.Error("null-byte prompt should be silently omitted")
	}
}

func TestSpawnArgs_NullByteModel(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{Prompt: "hi", Model: "bad\x00model"})
	assertNotContains(t, args, "-m")
}

func TestSpawnArgs_NullByteResumeIDAndPrompt(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "hello\x00world",
		Options: map[string]string{agentrun.OptionResumeID: "abc\x00def"},
	})
	// Both contain null bytes → resume path skipped (null ID), prompt skipped (null prompt).
	// Should produce plain exec --json -- with no prompt.
	assertArgsEqual(t, args, []string{"exec", "--json", "--"})
}

func TestSpawnArgs_OutputSchemaLeadingDash(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "hi",
		Options: map[string]string{OptionOutputSchema: "--malicious"},
	})
	assertNotContains(t, args, "--output-schema")
	assertNotContains(t, args, "--malicious")
}

// --- SpawnArgs: exec-only flags NOT on resume ---

func TestSpawnArgs_ResumeNoExecOnlyFlags(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt: "hi",
		Options: map[string]string{
			agentrun.OptionResumeID: testThreadID,
			OptionProfile:           "myprofile",
			OptionOutputSchema:      "schema.json",
		},
	})
	assertNotContains(t, args, "-p")
	assertNotContains(t, args, "myprofile")
	assertNotContains(t, args, "--output-schema")
	assertNotContains(t, args, "schema.json")
}

// --- SpawnArgs: POSIX -- separator prevents flag injection ---

func TestSpawnArgs_SeparatorPreventsInjection(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
	}{
		{"FullAutoInjection", "--full-auto"},
		{"SandboxInjection", "--sandbox=danger-full-access"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			_, args := b.SpawnArgs(agentrun.Session{
				Prompt:  tt.prompt,
				Options: map[string]string{agentrun.OptionMode: string(agentrun.ModePlan)},
			})
			assertSeparatorBefore(t, args, tt.prompt)
		})
	}
}

func TestSpawnArgs_ResumeSeparatorPreventsInjection(t *testing.T) {
	b := New()
	_, args := b.SpawnArgs(agentrun.Session{
		Prompt:  "--full-auto",
		Options: map[string]string{agentrun.OptionResumeID: testThreadID},
	})
	assertSeparatorBefore(t, args, testThreadID)
	assertSeparatorBefore(t, args, "--full-auto")
}

// --- resolveExecPolicy (SAFETY-CRITICAL) ---

func TestResolveExecPolicy_RootOptions(t *testing.T) {
	tests := []struct {
		name        string
		opts        map[string]string
		wantSandbox string
		wantFull    bool
	}{
		{
			name:        "ModePlan",
			opts:        map[string]string{agentrun.OptionMode: string(agentrun.ModePlan)},
			wantSandbox: "read-only",
		},
		{
			name: "ModePlan_HITLOff_PlanWins",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModePlan),
				agentrun.OptionHITL: string(agentrun.HITLOff),
			},
			wantSandbox: "read-only",
		},
		{
			name:     "HITLOff_Alone",
			opts:     map[string]string{agentrun.OptionHITL: string(agentrun.HITLOff)},
			wantFull: true,
		},
		{
			name: "ModeAct_HITLOn",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModeAct),
				agentrun.OptionHITL: string(agentrun.HITLOn),
			},
		},
		{
			name: "ModeAct_HITLOff",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModeAct),
				agentrun.OptionHITL: string(agentrun.HITLOff),
			},
			wantFull: true,
		},
		{
			name: "BackendSandbox_RootWins",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModePlan),
				OptionSandbox:       string(SandboxFullAccess),
			},
			wantSandbox: "read-only",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sandbox, fullAuto := resolveExecPolicy(tt.opts)
			if sandbox != tt.wantSandbox {
				t.Errorf("sandbox = %q, want %q", sandbox, tt.wantSandbox)
			}
			if fullAuto != tt.wantFull {
				t.Errorf("fullAuto = %v, want %v", fullAuto, tt.wantFull)
			}
		})
	}
}

func TestResolveExecPolicy_BackendOptions(t *testing.T) {
	tests := []struct {
		name        string
		opts        map[string]string
		wantSandbox string
	}{
		{
			name:        "BackendSandbox_Alone",
			opts:        map[string]string{OptionSandbox: string(SandboxWorkspaceWrite)},
			wantSandbox: "workspace-write",
		},
		{
			name: "NullByteSandbox_Ignored",
			opts: map[string]string{OptionSandbox: "read-only\x00"},
		},
		{
			name: "InvalidSandbox_Ignored",
			opts: map[string]string{OptionSandbox: "invalid-value"},
		},
		{
			name: "Neither",
			opts: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sandbox, fullAuto := resolveExecPolicy(tt.opts)
			if sandbox != tt.wantSandbox {
				t.Errorf("sandbox = %q, want %q", sandbox, tt.wantSandbox)
			}
			if fullAuto {
				t.Error("fullAuto should be false for backend-only options")
			}
		})
	}
}

// --- resolveResumeFullAuto ---

func TestResolveResumeFullAuto(t *testing.T) {
	tests := []struct {
		name string
		opts map[string]string
		want bool
	}{
		{
			name: "ModePlan_Suppressed",
			opts: map[string]string{agentrun.OptionMode: string(agentrun.ModePlan)},
			want: false,
		},
		{
			name: "ModePlan_HITLOff_Suppressed",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModePlan),
				agentrun.OptionHITL: string(agentrun.HITLOff),
			},
			want: false,
		},
		{
			name: "HITLOff_Alone",
			opts: map[string]string{agentrun.OptionHITL: string(agentrun.HITLOff)},
			want: true,
		},
		{
			name: "ModeAct_HITLOff",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModeAct),
				agentrun.OptionHITL: string(agentrun.HITLOff),
			},
			want: true,
		},
		{
			name: "ModeAct_HITLOn",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModeAct),
				agentrun.OptionHITL: string(agentrun.HITLOn),
			},
			want: false,
		},
		{
			name: "NoRootOptions",
			opts: map[string]string{},
			want: false,
		},
		{
			name: "BackendSandbox_NoEffect",
			opts: map[string]string{OptionSandbox: string(SandboxFullAccess)},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveResumeFullAuto(tt.opts)
			if got != tt.want {
				t.Errorf("resolveResumeFullAuto() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- resolveExecPolicy integration with SpawnArgs ---

func TestSpawnArgs_ExecPolicy_Integration(t *testing.T) {
	tests := []struct {
		name         string
		opts         map[string]string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "ModePlan_SandboxReadOnly",
			opts:         map[string]string{agentrun.OptionMode: string(agentrun.ModePlan)},
			wantContains: []string{"--sandbox", "read-only"},
			wantAbsent:   []string{"--full-auto"},
		},
		{
			name: "ModePlan_HITLOff_NoFullAuto",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModePlan),
				agentrun.OptionHITL: string(agentrun.HITLOff),
			},
			wantContains: []string{"--sandbox", "read-only"},
			wantAbsent:   []string{"--full-auto"},
		},
		{
			name:         "HITLOff_FullAuto",
			opts:         map[string]string{agentrun.OptionHITL: string(agentrun.HITLOff)},
			wantContains: []string{"--full-auto"},
			wantAbsent:   []string{"--sandbox"},
		},
		{
			name:         "BackendSandbox",
			opts:         map[string]string{OptionSandbox: string(SandboxFullAccess)},
			wantContains: []string{"--sandbox", "danger-full-access"},
			wantAbsent:   []string{"--full-auto"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			_, args := b.SpawnArgs(agentrun.Session{Prompt: "hi", Options: tt.opts})
			for _, w := range tt.wantContains {
				assertContains(t, args, w)
			}
			for _, w := range tt.wantAbsent {
				assertNotContains(t, args, w)
			}
		})
	}
}

// --- ResumeArgs ---

func TestResumeArgs_StoredThreadID(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	binary, args, err := b.ResumeArgs(agentrun.Session{}, "continue")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if binary != defaultBinary {
		t.Errorf("binary = %q, want %q", binary, defaultBinary)
	}
	assertContains(t, args, tid)
	if args[len(args)-1] != "continue" {
		t.Errorf("message should be last arg, got %q", args[len(args)-1])
	}
}

func TestResumeArgs_OptionResumeID_UUID(t *testing.T) {
	b := New()
	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: testThreadID},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, testThreadID)
}

func TestResumeArgs_OptionResumeID_ThreadName(t *testing.T) {
	b := New()
	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: "my-thread-name"},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, "my-thread-name")
}

func TestResumeArgs_StoredTakesPrecedence(t *testing.T) {
	b := New()
	stored := testThreadID
	b.threadID.CompareAndSwap(nil, &stored)

	other := "b2c3d4e5-f6a7-8901-bcde-f12345678901"
	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: other},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, stored)
	assertNotContains(t, args, other)
}

func TestResumeArgs_NoThreadID(t *testing.T) {
	b := New()
	_, _, err := b.ResumeArgs(agentrun.Session{}, "msg")
	if err == nil {
		t.Fatal("expected error for missing thread ID")
	}
	assertStringContains(t, err.Error(), "no thread ID")
}

func TestResumeArgs_NullByteID(t *testing.T) {
	b := New()
	_, _, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionResumeID: "abc\x00def"},
	}, "msg")
	if err == nil {
		t.Fatal("expected error for null-byte thread ID")
	}
	assertStringContains(t, err.Error(), "null bytes")
}

func TestResumeArgs_NullByteMessage(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, _, err := b.ResumeArgs(agentrun.Session{}, "hello\x00world")
	if err == nil {
		t.Fatal("expected error for null-byte message")
	}
	assertStringContains(t, err.Error(), "null bytes")
}

func TestResumeArgs_WithModel(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, args, err := b.ResumeArgs(agentrun.Session{Model: "o3"}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, args, "-m")
	assertContains(t, args, "o3")
}

func TestResumeArgs_EmptyMessage(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, args, err := b.ResumeArgs(agentrun.Session{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	last := args[len(args)-1]
	if last == "" {
		t.Error("empty initialPrompt should not produce a trailing empty arg")
	}
}

func TestResumeArgs_NoExecOnlyFlags(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, args, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{
			OptionProfile:      "myprofile",
			OptionOutputSchema: "schema.json",
		},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertNotContains(t, args, "-p")
	assertNotContains(t, args, "myprofile")
	assertNotContains(t, args, "--output-schema")
	assertNotContains(t, args, "schema.json")
}

// --- ResumeArgs: policy matrix (--sandbox NEVER on resume) ---

func TestResumeArgs_PolicyMatrix(t *testing.T) {
	tests := []struct {
		name         string
		opts         map[string]string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:       "ModePlan_NoSandboxNoFullAuto",
			opts:       map[string]string{agentrun.OptionMode: string(agentrun.ModePlan)},
			wantAbsent: []string{"--sandbox", "read-only", "--full-auto"},
		},
		{
			name: "ModePlan_HITLOff_PlanSuppressesFullAuto",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModePlan),
				agentrun.OptionHITL: string(agentrun.HITLOff),
			},
			wantAbsent: []string{"--sandbox", "read-only", "--full-auto"},
		},
		{
			name:         "HITLOff_FullAutoOnly",
			opts:         map[string]string{agentrun.OptionHITL: string(agentrun.HITLOff)},
			wantContains: []string{"--full-auto"},
			wantAbsent:   []string{"--sandbox"},
		},
		{
			name: "ModeAct_HITLOff_FullAutoOnly",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModeAct),
				agentrun.OptionHITL: string(agentrun.HITLOff),
			},
			wantContains: []string{"--full-auto"},
			wantAbsent:   []string{"--sandbox"},
		},
		{
			name: "ModeAct_HITLOn_NoFlags",
			opts: map[string]string{
				agentrun.OptionMode: string(agentrun.ModeAct),
				agentrun.OptionHITL: string(agentrun.HITLOn),
			},
			wantAbsent: []string{"--sandbox", "--full-auto"},
		},
		{
			name:       "BackendSandbox_IgnoredOnResume",
			opts:       map[string]string{OptionSandbox: string(SandboxReadOnly)},
			wantAbsent: []string{"--sandbox", "read-only", "--full-auto"},
		},
		{
			name:       "BackendSandbox_FullAccess_IgnoredOnResume",
			opts:       map[string]string{OptionSandbox: string(SandboxFullAccess)},
			wantAbsent: []string{"--sandbox", "danger-full-access", "--full-auto"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			tid := testThreadID
			b.threadID.CompareAndSwap(nil, &tid)

			_, args, err := b.ResumeArgs(agentrun.Session{Options: tt.opts}, "msg")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, w := range tt.wantContains {
				assertContains(t, args, w)
			}
			for _, w := range tt.wantAbsent {
				assertNotContains(t, args, w)
			}
		})
	}
}

// Also test the SpawnArgs resume path (via OptionResumeID) for sandbox absence.
func TestSpawnArgs_ResumePath_PolicyMatrix(t *testing.T) {
	tests := []struct {
		name       string
		opts       map[string]string
		wantAbsent []string
	}{
		{
			name: "ModePlan_NoSandbox",
			opts: map[string]string{
				agentrun.OptionResumeID: testThreadID,
				agentrun.OptionMode:     string(agentrun.ModePlan),
			},
			wantAbsent: []string{"--sandbox", "read-only"},
		},
		{
			name: "BackendSandbox_Ignored",
			opts: map[string]string{
				agentrun.OptionResumeID: testThreadID,
				OptionSandbox:           string(SandboxFullAccess),
			},
			wantAbsent: []string{"--sandbox", "danger-full-access"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			_, args := b.SpawnArgs(agentrun.Session{Prompt: "hi", Options: tt.opts})
			for _, w := range tt.wantAbsent {
				assertNotContains(t, args, w)
			}
		})
	}
}

func TestResumeArgs_SubcommandStructure(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, args, err := b.ResumeArgs(agentrun.Session{}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must be: exec resume --json ... -- <thread_id> [prompt]
	if len(args) < 3 {
		t.Fatalf("args too short: %v", args)
	}
	if args[0] != "exec" || args[1] != "resume" || args[2] != "--json" {
		t.Errorf("expected exec resume --json prefix, got %v", args[:3])
	}
}

func TestResumeArgs_SeparatorPresent(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, args, err := b.ResumeArgs(agentrun.Session{}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSeparatorBefore(t, args, testThreadID)
}

// --- ResumeArgs: validateSessionOptions ---

func TestResumeArgs_InvalidMode(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, _, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionMode: "invalid"},
	}, "msg")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	assertStringContains(t, err.Error(), "unknown mode")
}

func TestResumeArgs_InvalidHITL(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, _, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{agentrun.OptionHITL: "maybe"},
	}, "msg")
	if err == nil {
		t.Fatal("expected error for invalid HITL")
	}
	assertStringContains(t, err.Error(), "unknown hitl")
}

func TestResumeArgs_InvalidSandbox(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	_, _, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{OptionSandbox: "invalid"},
	}, "msg")
	if err == nil {
		t.Fatal("expected error for invalid sandbox")
	}
	assertStringContains(t, err.Error(), "unknown sandbox")
}

func TestResumeArgs_SandboxValidation_SkippedWhenRootSet(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)

	// Invalid sandbox but root options set → sandbox validation skipped.
	_, _, err := b.ResumeArgs(agentrun.Session{
		Options: map[string]string{
			agentrun.OptionMode: string(agentrun.ModePlan),
			OptionSandbox:       "invalid",
		},
	}, "msg")
	if err != nil {
		t.Fatalf("unexpected error: sandbox validation should be skipped when root set: %v", err)
	}
}

// --- ThreadID ---

func TestThreadID_Empty(t *testing.T) {
	b := New()
	if id := b.ThreadID(); id != "" {
		t.Errorf("ThreadID() = %q, want empty", id)
	}
}

func TestThreadID_AfterStore(t *testing.T) {
	b := New()
	tid := testThreadID
	b.threadID.CompareAndSwap(nil, &tid)
	if id := b.ThreadID(); id != tid {
		t.Errorf("ThreadID() = %q, want %q", id, tid)
	}
}

// --- resolveThreadID precedence ---

func TestResolveThreadID_Precedence(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		optVal string
		want   string
	}{
		{"stored_only", testThreadID, "", testThreadID},
		{"option_only", "", "my-thread-name", "my-thread-name"},
		{"stored_wins", testThreadID, "other-thread", testThreadID},
		{"neither", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New()
			if tt.stored != "" {
				b.threadID.CompareAndSwap(nil, &tt.stored)
			}
			session := agentrun.Session{}
			if tt.optVal != "" {
				session.Options = map[string]string{agentrun.OptionResumeID: tt.optVal}
			}
			got := b.resolveThreadID(session)
			if got != tt.want {
				t.Errorf("resolveThreadID() = %q, want %q", got, tt.want)
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

func assertSeparatorBefore(t *testing.T, args []string, positional string) {
	t.Helper()
	sepIdx := indexOf(args, "--")
	posIdx := indexOf(args, positional)
	if sepIdx < 0 {
		t.Fatalf("missing -- separator in args %v", args)
	}
	if posIdx < 0 {
		t.Fatalf("%q not found in args %v", positional, args)
	}
	if sepIdx >= posIdx {
		t.Errorf("-- (idx %d) must come before %q (idx %d)", sepIdx, positional, posIdx)
	}
}

// --- Effort option tests ---

func TestSpawnArgs_Effort(t *testing.T) {
	tests := []struct {
		name     string
		effort   string
		contains string
	}{
		{"low", "low", "model_reasoning_effort=low"},
		{"medium", "medium", "model_reasoning_effort=medium"},
		{"high", "high", "model_reasoning_effort=high"},
		{"max_to_xhigh", "max", "model_reasoning_effort=xhigh"},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := agentrun.Session{
				Prompt:  "test",
				Options: map[string]string{agentrun.OptionEffort: tt.effort},
			}
			_, args := b.SpawnArgs(session)
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, tt.contains) {
				t.Errorf("args missing %q in: %v", tt.contains, args)
			}
		})
	}
}

func TestSpawnArgs_Effort_Skipped(t *testing.T) {
	tests := []struct {
		name   string
		effort string
	}{
		{"empty", ""},
		{"invalid", "xhigh"},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := agentrun.Session{
				Prompt:  "test",
				Options: map[string]string{agentrun.OptionEffort: tt.effort},
			}
			_, args := b.SpawnArgs(session)
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "model_reasoning_effort") {
				t.Errorf("effort flag should be skipped: %v", args)
			}
		})
	}
}

func TestResumeArgs_Effort(t *testing.T) {
	b := New()
	b.threadID.Store(&[]string{testThreadID}[0])

	session := agentrun.Session{
		Options: map[string]string{
			agentrun.OptionResumeID: testThreadID,
			agentrun.OptionEffort:   "high",
		},
	}
	_, args, err := b.ResumeArgs(session, "test")
	if err != nil {
		t.Fatalf("ResumeArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "model_reasoning_effort=high") {
		t.Errorf("args missing effort: %v", args)
	}
}

func TestResumeArgs_Effort_Invalid(t *testing.T) {
	b := New()
	b.threadID.Store(&[]string{testThreadID}[0])

	session := agentrun.Session{
		Options: map[string]string{
			agentrun.OptionResumeID: testThreadID,
			agentrun.OptionEffort:   "xhigh",
		},
	}
	_, _, err := b.ResumeArgs(session, "test")
	if err == nil {
		t.Fatal("expected error for invalid effort")
	}
	if !strings.Contains(err.Error(), "unknown effort") {
		t.Errorf("error should mention unknown effort: %v", err)
	}
}

// --- AddDirs option tests ---

func TestSpawnArgs_AddDirs(t *testing.T) {
	tests := []struct {
		name     string
		addDirs  string
		contains []string
		excludes []string
	}{
		{"single", "/foo/bar", []string{"--add-dir", "/foo/bar"}, nil},
		{"multiple", "/foo\n/bar", []string{"--add-dir", "/foo", "--add-dir", "/bar"}, nil},
		{"skip_relative", "relative/path", nil, []string{"--add-dir"}},
		{"skip_leading_dash", "-/foo", nil, []string{"--add-dir"}},
		{"empty", "", nil, []string{"--add-dir"}},
	}

	b := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := agentrun.Session{
				Prompt:  "test",
				Options: map[string]string{agentrun.OptionAddDirs: tt.addDirs},
			}
			_, args := b.SpawnArgs(session)
			joined := strings.Join(args, " ")
			for _, c := range tt.contains {
				if !strings.Contains(joined, c) {
					t.Errorf("args missing %q in: %v", c, args)
				}
			}
			for _, e := range tt.excludes {
				if strings.Contains(joined, e) {
					t.Errorf("args should not contain %q: %v", e, args)
				}
			}
		})
	}
}

func TestResumeArgs_AddDirs(t *testing.T) {
	b := New()
	b.threadID.Store(&[]string{testThreadID}[0])

	tests := []struct {
		name     string
		addDirs  string
		contains []string
		excludes []string
	}{
		{"single", "/foo/bar", []string{"--add-dir", "/foo/bar"}, nil},
		{"skip_relative", "relative/path", nil, []string{"--add-dir"}},
		{"skip_leading_dash", "-/foo", nil, []string{"--add-dir"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := agentrun.Session{
				Options: map[string]string{
					agentrun.OptionResumeID: testThreadID,
					agentrun.OptionAddDirs:  tt.addDirs,
				},
			}
			_, args, err := b.ResumeArgs(session, "test")
			if err != nil {
				t.Fatalf("ResumeArgs: %v", err)
			}
			assertArgsContainsExcludes(t, args, tt.contains, tt.excludes)
		})
	}
}

func indexOf(args []string, target string) int {
	for i, a := range args {
		if a == target {
			return i
		}
	}
	return -1
}

func assertArgsContainsExcludes(t *testing.T, args, contains, excludes []string) {
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
}

func assertStringContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%q does not contain %q", s, substr)
	}
}
