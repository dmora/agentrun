//go:build !windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dmora/agentrun"
)

// envBlocklist contains environment variable names that must never be set via
// MCP tool calls. These control process execution, library loading, or identity.
var envBlocklist = map[string]bool{
	"PATH":                         true,
	"HOME":                         true,
	"USER":                         true,
	"SHELL":                        true,
	"LD_PRELOAD":                   true,
	"LD_LIBRARY_PATH":              true,
	"LD_AUDIT":                     true,
	"LD_CONFIG":                    true,
	"DYLD_INSERT_LIBRARIES":        true,
	"DYLD_LIBRARY_PATH":            true,
	"DYLD_FRAMEWORK_PATH":          true,
	"DYLD_FALLBACK_FRAMEWORK_PATH": true,
	"BASH_ENV":                     true,
	"ENV":                          true,
}

// envSensitivePatterns are substrings that indicate a secret-bearing key.
var envSensitivePatterns = []string{
	"_API_KEY",
	"_SECRET",
	"_TOKEN",
	"_PASSWORD",
	"_CREDENTIAL",
}

// validateEnvKeys rejects environment variable keys that are in the blocklist
// or match sensitive patterns. Returns a clear error naming the rejected key.
func validateEnvKeys(env map[string]string) error {
	for k := range env {
		upper := strings.ToUpper(k)
		if envBlocklist[upper] {
			return fmt.Errorf("environment variable %q is blocked for security", k)
		}
		for _, pat := range envSensitivePatterns {
			if strings.Contains(upper, pat) {
				return fmt.Errorf("environment variable %q matches sensitive pattern %q", k, pat)
			}
		}
	}
	return nil
}

// validateCWD ensures cwd is an absolute path under workspaceRoot with no
// symlink escape. Empty cwd defaults to workspaceRoot. Both paths are resolved
// via filepath.EvalSymlinks to prevent workspace escape via symlink chains.
func validateCWD(cwd, workspaceRoot string) (string, error) {
	if cwd == "" {
		return workspaceRoot, nil
	}
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("cwd must be an absolute path, got %q", cwd)
	}
	// Resolve symlinks on both paths to prevent escape via symlink chains.
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("cwd %q does not exist", cwd)
		}
		return "", fmt.Errorf("cwd %q: %w", cwd, err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("workspace root %q: %w", workspaceRoot, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("cwd %q is not under workspace %q", cwd, workspaceRoot)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("cwd %q escapes workspace %q", cwd, workspaceRoot)
	}
	return resolved, nil
}

// allowedOptions is the curated set of session option keys that MCP callers
// may set. HITL and Mode are hard-coded by the server, not caller-controlled.
var allowedOptions = map[string]bool{
	agentrun.OptionSystemPrompt:   true,
	agentrun.OptionMaxTurns:       true,
	agentrun.OptionThinkingBudget: true,
	agentrun.OptionEffort:         true,
	agentrun.OptionAgentID:        true,
	agentrun.OptionAddDirs:        true,
	agentrun.OptionSessionName:    true,
}

// validateOptions rejects any option key not in the allowlist.
func validateOptions(opts map[string]string) error {
	for k := range opts {
		if !allowedOptions[k] {
			return fmt.Errorf("option %q is not allowed (allowed: %s)", k, allowedOptionsList())
		}
	}
	return nil
}

func allowedOptionsList() string {
	keys := make([]string, 0, len(allowedOptions))
	for k := range allowedOptions {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

// cappedWriter bounds memory during subprocess stderr collection.
// Writes beyond the cap are silently discarded. Write always returns
// len(p), nil to satisfy io.Writer without surfacing back-pressure.
//
// All methods are goroutine-safe. The subprocess stderr goroutine calls
// Write concurrently with session_send calling Reset (between turns) and
// Bytes (to collect output). Bytes returns a copy to avoid aliasing the
// internal buffer.
type cappedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p) // preserve original length before reslice
	remaining := w.max - w.buf.Len()
	if remaining <= 0 {
		return n, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	w.buf.Write(p)
	return n, nil // always report full write to caller
}

// Reset clears the buffer. Safe for concurrent use.
func (w *cappedWriter) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Reset()
}

// Bytes returns a copy of the buffered data. Safe for concurrent use.
func (w *cappedWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}

// credentialPatterns matches common secret formats in stderr output.
var credentialPatterns = regexp.MustCompile(
	`(?i)` +
		`sk-[a-zA-Z0-9]{20,}` + `|` +
		`key-[a-zA-Z0-9]{20,}` + `|` +
		`Bearer\s+\S+` + `|` +
		`Authorization:\s*\S+` + `|` +
		`xoxb-[a-zA-Z0-9-]+` + `|` +
		`ghp_[a-zA-Z0-9]{36,}` + `|` +
		`ghs_[a-zA-Z0-9]{36,}` + `|` +
		`AKIA[0-9A-Z]{16}`,
)

// scrubStderr sanitizes raw stderr for inclusion in tool responses.
// It replaces credential patterns and truncates to maxBytes at a valid
// UTF-8 boundary.
func scrubStderr(raw []byte, maxBytes int) string {
	scrubbed := credentialPatterns.ReplaceAll(raw, []byte("[REDACTED]"))
	if len(scrubbed) > maxBytes {
		scrubbed = scrubbed[:maxBytes]
		// Remove trailing bytes from an incomplete UTF-8 sequence.
		for len(scrubbed) > 0 {
			r, size := utf8.DecodeLastRune(scrubbed)
			if r != utf8.RuneError || size != 1 {
				break
			}
			scrubbed = scrubbed[:len(scrubbed)-1]
		}
	}
	return string(scrubbed)
}
