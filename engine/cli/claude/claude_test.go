package claude

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// Test fixture constants shared across all test files in this package.
const (
	testModel          = "claude-sonnet-4-5-20250514"
	testPrompt         = "hello world"
	testSystemPrompt   = "be helpful"
	testResumeID       = "conv-abc123"
	testBinary         = "/usr/local/bin/claude"
	testToolName       = "Read"
	testThinkingBudget = "10000"
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

func TestNew_WithPartialMessagesFalse(t *testing.T) {
	b := New(WithPartialMessages(false))
	if b.partialMessages {
		t.Error("partialMessages should be false")
	}
}

func TestNew_PartialMessagesDefault(t *testing.T) {
	b := New()
	if !b.partialMessages {
		t.Error("partialMessages should default to true")
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

// --- validateResumeID tests ---

func TestValidateResumeID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{"simple", "conv-abc123", false},
		{"alphanumeric", "abc123", false},
		{"underscore", "ses_abc123", false},
		{"hyphen", "conv-abc-123", false},
		{"max_length", strings.Repeat("a", 128), false},
		{"too_long", strings.Repeat("a", 129), true},
		{"empty", "", true},
		{"spaces", "has spaces", true},
		{"null_bytes", "abc\x00def", true},
		{"special_chars", "abc!@#$", true},
		{"newline", "abc\ndef", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResumeID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateResumeID(%q) error = %v, wantErr %v", tt.id, err, tt.wantErr)
			}
		})
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
