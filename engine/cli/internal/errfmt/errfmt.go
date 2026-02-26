// Package errfmt provides shared error formatting for CLI backend parsers.
package errfmt

import "unicode/utf8"

// MaxLen caps error content to prevent unbounded propagation.
const MaxLen = 4096

// Format formats an error code and message pair.
// Content is capped at MaxLen bytes, truncated at a valid UTF-8 boundary.
func Format(code, message string) string {
	var content string
	if code != "" {
		content = code + ": " + message
	} else {
		content = message
	}
	if len(content) > MaxLen {
		content = content[:MaxLen]
		for !utf8.ValidString(content) {
			content = content[:len(content)-1]
		}
	}
	return content
}
