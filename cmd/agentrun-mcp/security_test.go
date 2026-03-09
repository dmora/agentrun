//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// validateEnvKeys tests
// ---------------------------------------------------------------------------

func TestValidateEnvKeys_Blocked(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"PATH", "PATH"},
		{"HOME", "HOME"},
		{"LD_PRELOAD", "LD_PRELOAD"},
		{"LD_AUDIT", "LD_AUDIT"},
		{"LD_CONFIG", "LD_CONFIG"},
		{"BASH_ENV", "BASH_ENV"},
		{"ENV", "ENV"},
		{"DYLD_INSERT_LIBRARIES", "DYLD_INSERT_LIBRARIES"},
		{"case_insensitive", "path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvKeys(map[string]string{tt.key: "val"})
			if err == nil {
				t.Fatalf("expected error for key %q", tt.key)
			}
			if !strings.Contains(err.Error(), "blocked") {
				t.Errorf("error = %v, want to contain 'blocked'", err)
			}
		})
	}
}

func TestValidateEnvKeys_SensitivePatterns(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"API_KEY", "MY_API_KEY"},
		{"SECRET", "APP_SECRET"},
		{"TOKEN", "AUTH_TOKEN"},
		{"PASSWORD", "DB_PASSWORD"},
		{"CREDENTIAL", "AZURE_CREDENTIAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvKeys(map[string]string{tt.key: "val"})
			if err == nil {
				t.Fatalf("expected error for key %q", tt.key)
			}
			if !strings.Contains(err.Error(), "sensitive") {
				t.Errorf("error = %v, want to contain 'sensitive'", err)
			}
		})
	}
}

func TestValidateEnvKeys_Allowed(t *testing.T) {
	allowed := map[string]string{
		"CLAUDE_MODEL":   "claude-3",
		"OPENCODE_FORK":  "true",
		"MY_CUSTOM_FLAG": "1",
	}
	if err := validateEnvKeys(allowed); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateEnvKeys_Empty(t *testing.T) {
	if err := validateEnvKeys(nil); err != nil {
		t.Fatalf("nil map should pass: %v", err)
	}
	if err := validateEnvKeys(map[string]string{}); err != nil {
		t.Fatalf("empty map should pass: %v", err)
	}
}

// ---------------------------------------------------------------------------
// validateCWD tests
// ---------------------------------------------------------------------------

func TestValidateCWD_InScope(t *testing.T) {
	workspace := t.TempDir()
	project := filepath.Join(workspace, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd, err := validateCWD(project, workspace)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	// EvalSymlinks resolves platform symlinks (e.g., /var → /private/var on macOS).
	wantProject, _ := filepath.EvalSymlinks(project)
	if cwd != wantProject {
		t.Errorf("cwd = %q, want %q", cwd, wantProject)
	}
}

func TestValidateCWD_DefaultsToWorkspace(t *testing.T) {
	workspace := t.TempDir()
	cwd, err := validateCWD("", workspace)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if cwd != workspace {
		t.Errorf("cwd = %q, want %q", cwd, workspace)
	}
}

func TestValidateCWD_Escape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	_, err := validateCWD(outside, workspace)
	if err == nil {
		t.Fatal("expected error for path outside workspace")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error = %v, want to contain 'escapes'", err)
	}
}

func TestValidateCWD_Root(t *testing.T) {
	workspace := t.TempDir()
	_, err := validateCWD("/", workspace)
	if err == nil {
		t.Fatal("expected error for / when workspace is not /")
	}
}

func TestValidateCWD_Relative(t *testing.T) {
	_, err := validateCWD("relative/path", "/tmp/workspace")
	if err == nil {
		t.Fatal("expected error for relative path")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %v, want to contain 'absolute'", err)
	}
}

func TestValidateCWD_NonExistent(t *testing.T) {
	workspace := t.TempDir()
	_, err := validateCWD(filepath.Join(workspace, "does-not-exist"), workspace)
	if err == nil {
		t.Fatal("expected error for non-existent path")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v, want to contain 'does not exist'", err)
	}
}

func TestValidateCWD_SymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}
	_, err := validateCWD(link, workspace)
	if err == nil {
		t.Fatal("expected error for symlink escaping workspace")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error = %v, want to contain 'escapes'", err)
	}
}

// ---------------------------------------------------------------------------
// validateOptions tests
// ---------------------------------------------------------------------------

func TestOptionsAllowlist_Allows(t *testing.T) {
	opts := map[string]string{
		"system_prompt":   "You are helpful.",
		"max_turns":       "5",
		"thinking_budget": "10000",
		"effort":          "high",
	}
	if err := validateOptions(opts); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestOptionsAllowlist_Rejects(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"hitl", "hitl"},
		{"mode", "mode"},
		{"resume_id", "resume_id"},
		{"permission_mode", "claude.permission_mode"},
		{"arbitrary", "something_random"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOptions(map[string]string{tt.key: "val"})
			if err == nil {
				t.Fatalf("expected error for option %q", tt.key)
			}
			if !strings.Contains(err.Error(), "not allowed") {
				t.Errorf("error = %v, want to contain 'not allowed'", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// scrubStderr tests
// ---------------------------------------------------------------------------

func TestScrubStderr_Credentials(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"sk_key", "error: sk-abc12345678901234567890 not valid"},
		{"key_prefix", "key-abc12345678901234567890 expired"},
		{"bearer", "Bearer eyJhbGciOiJIUzI1NiJ9.token"},
		{"authorization", "Authorization: Basic dXNlcjpwYXNz"},
		{"slack_token", "got xoxb-12345-abcdefghijklmnop from env"},
		{"github_pat", "using ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"github_app", "auth ghs_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"aws_key", "access key AKIAIOSFODNN7EXAMPLE found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scrubStderr([]byte(tt.input), 4096)
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("expected [REDACTED] in output, got %q", got)
			}
		})
	}
}

func TestScrubStderr_Truncation(t *testing.T) {
	input := strings.Repeat("x", 4096)
	got := scrubStderr([]byte(input), 2048)
	if len(got) != 2048 {
		t.Errorf("len = %d, want 2048", len(got))
	}
}

func TestScrubStderr_UTF8Safe(t *testing.T) {
	// 4-byte UTF-8 character (emoji) at the truncation boundary.
	emoji := "🔥" // 4 bytes
	input := strings.Repeat("x", 2046) + emoji
	got := scrubStderr([]byte(input), 2048)
	if !utf8.ValidString(got) {
		t.Error("truncated output contains invalid UTF-8")
	}
	// Should truncate to 2046 (before the emoji) since splitting it would be invalid.
	if len(got) != 2046 {
		t.Errorf("len = %d, want 2046 (emoji trimmed to preserve UTF-8)", len(got))
	}
}

func TestScrubStderr_Clean(t *testing.T) {
	input := "normal error: something went wrong"
	got := scrubStderr([]byte(input), 4096)
	if got != input {
		t.Errorf("clean stderr should pass through unchanged, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// cappedWriter tests
// ---------------------------------------------------------------------------

func TestCappedWriter_BoundsMemory(t *testing.T) {
	w := &cappedWriter{max: 10}
	n, err := w.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write(5) = (%d, %v), want (5, nil)", n, err)
	}
	// Write 20 more bytes — only 5 should fit.
	n, err = w.Write([]byte("worldworldworldworld"))
	if err != nil || n != 20 {
		t.Fatalf("Write(20) = (%d, %v), want (20, nil)", n, err)
	}
	got := w.Bytes()
	if len(got) != 10 {
		t.Errorf("len(Bytes()) = %d, want 10", len(got))
	}
	if string(got) != "helloworld" {
		t.Errorf("Bytes() = %q, want %q", got, "helloworld")
	}

	// Further writes are silently discarded.
	n, err = w.Write([]byte("more"))
	if err != nil || n != 4 {
		t.Fatalf("Write(4) after cap = (%d, %v), want (4, nil)", n, err)
	}
	if len(w.Bytes()) != 10 {
		t.Errorf("len(Bytes()) = %d after discard, want 10", len(w.Bytes()))
	}
}

func TestCappedWriter_ExactBoundary(t *testing.T) {
	w := &cappedWriter{max: 5}
	n, err := w.Write([]byte("12345"))
	if err != nil || n != 5 {
		t.Fatalf("Write(5) = (%d, %v), want (5, nil)", n, err)
	}
	if len(w.Bytes()) != 5 {
		t.Errorf("len(Bytes()) = %d, want 5", len(w.Bytes()))
	}

	// Next write should be fully discarded.
	n, err = w.Write([]byte("6"))
	if err != nil || n != 1 {
		t.Fatalf("Write(1) at cap = (%d, %v), want (1, nil)", n, err)
	}
	if len(w.Bytes()) != 5 {
		t.Errorf("len(Bytes()) = %d, want 5", len(w.Bytes()))
	}
}

func TestCappedWriter_ResetClears(t *testing.T) {
	w := &cappedWriter{max: 10}
	w.Write([]byte("hello"))
	w.Reset()
	if len(w.Bytes()) != 0 {
		t.Errorf("len(Bytes()) after Reset = %d, want 0", len(w.Bytes()))
	}
	// Can write again after reset.
	w.Write([]byte("world"))
	if string(w.Bytes()) != "world" {
		t.Errorf("Bytes() after re-write = %q, want %q", w.Bytes(), "world")
	}
}

func TestCappedWriter_ConcurrentWriteAndReset(t *testing.T) {
	w := &cappedWriter{max: 1024}
	done := make(chan struct{})

	// Writer goroutine.
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			w.Write([]byte("data"))
		}
	}()

	// Main goroutine resets and reads concurrently.
	for i := 0; i < 100; i++ {
		w.Reset()
		_ = w.Bytes()
	}
	<-done
}
