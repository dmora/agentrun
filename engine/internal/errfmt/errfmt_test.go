package errfmt

import (
	"strings"
	"testing"
)

func TestTruncate_ShortPassthrough(t *testing.T) {
	result := Truncate("short message")
	if result != "short message" {
		t.Errorf("Truncate() = %q, want %q", result, "short message")
	}
}

func TestTruncate_LongMessage(t *testing.T) {
	longMsg := strings.Repeat("x", MaxLen+500)
	result := Truncate(longMsg)
	if len(result) > MaxLen {
		t.Errorf("len(result) = %d, want <= %d", len(result), MaxLen)
	}
}

func TestTruncate_UTF8Truncation(t *testing.T) {
	prefix := strings.Repeat("x", MaxLen-2)
	input := prefix + "\U0001F600" // 4-byte emoji at boundary
	result := Truncate(input)
	if len(result) > MaxLen {
		t.Errorf("len(result) = %d, want <= %d", len(result), MaxLen)
	}
	for i, r := range result {
		if r == '\uFFFD' {
			t.Errorf("invalid UTF-8 at byte %d", i)
			break
		}
	}
}

func TestSanitizeCode_Valid(t *testing.T) {
	result := SanitizeCode("rate_limit")
	if result != "rate_limit" {
		t.Errorf("SanitizeCode() = %q, want %q", result, "rate_limit")
	}
}

func TestSanitizeCode_Empty(t *testing.T) {
	result := SanitizeCode("")
	if result != "" {
		t.Errorf("SanitizeCode() = %q, want %q", result, "")
	}
}

func TestSanitizeCode_ControlCharRejected(t *testing.T) {
	result := SanitizeCode("rate\x00limit")
	if result != "" {
		t.Errorf("SanitizeCode() = %q, want empty (control char rejection)", result)
	}
}

func TestSanitizeCode_NewlineRejected(t *testing.T) {
	result := SanitizeCode("rate\nlimit")
	if result != "" {
		t.Errorf("SanitizeCode() = %q, want empty (newline is control char)", result)
	}
}

func TestSanitizeCode_TabRejected(t *testing.T) {
	result := SanitizeCode("rate\tlimit")
	if result != "" {
		t.Errorf("SanitizeCode() = %q, want empty (tab is control char)", result)
	}
}

func TestSanitizeCode_NullByteRejected(t *testing.T) {
	result := SanitizeCode("\x00rate_limit")
	if result != "" {
		t.Errorf("SanitizeCode() = %q, want empty", result)
	}
}

func TestSanitizeCode_LongTruncated(t *testing.T) {
	long := strings.Repeat("a", MaxCodeLen+50)
	result := SanitizeCode(long)
	if len(result) > MaxCodeLen {
		t.Errorf("len(result) = %d, want <= %d", len(result), MaxCodeLen)
	}
}

func TestSanitizeCode_UTF8SafeTruncation(t *testing.T) {
	prefix := strings.Repeat("x", MaxCodeLen-2)
	input := prefix + "\U0001F600" // 4-byte emoji at boundary
	result := SanitizeCode(input)
	if len(result) > MaxCodeLen {
		t.Errorf("len(result) = %d, want <= %d", len(result), MaxCodeLen)
	}
	for i, r := range result {
		if r == '\uFFFD' {
			t.Errorf("invalid UTF-8 at byte %d", i)
			break
		}
	}
}
