package errfmt

import (
	"strings"
	"testing"
)

func TestFormat_CodeAndMessage(t *testing.T) {
	result := Format("ERR01", "something broke")
	if result != "ERR01: something broke" {
		t.Errorf("Format() = %q, want %q", result, "ERR01: something broke")
	}
}

func TestFormat_MessageOnly(t *testing.T) {
	result := Format("", "something broke")
	if result != "something broke" {
		t.Errorf("Format() = %q, want %q", result, "something broke")
	}
}

func TestFormat_LongMessage(t *testing.T) {
	longMsg := strings.Repeat("x", MaxLen+500)
	result := Format("ERR", longMsg)
	if len(result) > MaxLen {
		t.Errorf("len(result) = %d, want <= %d", len(result), MaxLen)
	}
}

func TestFormat_UTF8Truncation(t *testing.T) {
	prefix := strings.Repeat("x", MaxLen-2)
	input := prefix + "\U0001F600" // 4-byte emoji at boundary
	result := Format("", input)
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
