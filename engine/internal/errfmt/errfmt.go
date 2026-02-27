// Package errfmt provides shared error formatting for backend parsers.
package errfmt

import (
	"unicode"
	"unicode/utf8"
)

// MaxLen caps error content to prevent unbounded propagation.
const MaxLen = 4096

// MaxCodeLen caps error codes (short identifiers).
const MaxCodeLen = 128

// truncateUTF8 caps s at max bytes, backtracking to a valid UTF-8 boundary.
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	end := limit
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// Truncate caps a string at MaxLen bytes with UTF-8-safe truncation.
func Truncate(s string) string {
	return truncateUTF8(s, MaxLen)
}

// SanitizeCode validates and truncates a raw error code string.
// Returns "" for strings containing control characters.
// Validate-then-truncate: control chars are rejected first, then
// rune-safe truncation ensures valid UTF-8 output.
func SanitizeCode(raw string) string {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return ""
		}
	}
	return truncateUTF8(raw, MaxCodeLen)
}
