// Package stoputil provides shared StopReason sanitization for CLI backends.
package stoputil

import (
	"unicode"
	"unicode/utf8"

	"github.com/dmora/agentrun"
)

// MaxLen is the maximum byte length for a sanitized StopReason.
const MaxLen = 64

// Sanitize validates and truncates a raw stop_reason string.
// Returns empty StopReason for strings containing control characters.
// Validate-then-truncate: control chars are rejected first, then
// rune-safe truncation ensures valid UTF-8 output.
func Sanitize(raw string) agentrun.StopReason {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return ""
		}
	}
	if len(raw) > MaxLen {
		// Backtrack to the start of the last rune to ensure valid UTF-8.
		end := MaxLen
		for end > 0 && !utf8.RuneStart(raw[end]) {
			end--
		}
		return agentrun.StopReason(raw[:end])
	}
	return agentrun.StopReason(raw)
}
