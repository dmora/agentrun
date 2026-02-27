package stoputil

import (
	"strings"
	"testing"

	"github.com/dmora/agentrun"
)

func TestSanitize_Valid(t *testing.T) {
	got := Sanitize("end_turn")
	if got != agentrun.StopEndTurn {
		t.Errorf("got %q, want %q", got, agentrun.StopEndTurn)
	}
}

func TestSanitize_ControlChars(t *testing.T) {
	got := Sanitize("end\x00turn")
	if got != "" {
		t.Errorf("control chars should produce empty StopReason, got %q", got)
	}
}

func TestSanitize_TooLong(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := Sanitize(long)
	if len(string(got)) > MaxLen {
		t.Errorf("should be truncated to %d bytes, got %d", MaxLen, len(string(got)))
	}
}

func TestSanitize_MultibyteTruncation(t *testing.T) {
	// 62 ASCII chars + one 3-byte UTF-8 char = 65 bytes, over 64 limit.
	s := strings.Repeat("a", 62) + "日"
	got := Sanitize(s)
	if len(string(got)) > MaxLen {
		t.Errorf("should be truncated to ≤%d bytes, got %d", MaxLen, len(string(got)))
	}
	// The 3-byte char at position 62-64 should be cleanly removed, leaving 62 chars.
	if string(got) != strings.Repeat("a", 62) {
		t.Errorf("got %q, want %q", got, strings.Repeat("a", 62))
	}
}

func TestSanitize_Empty(t *testing.T) {
	got := Sanitize("")
	if got != "" {
		t.Errorf("empty string should produce empty StopReason, got %q", got)
	}
}
