// Package optutil provides shared option resolution helpers for CLI backends.
package optutil

import (
	"fmt"

	"github.com/dmora/agentrun"
)

// RootOptionsSet reports whether either OptionMode or OptionHITL is present
// in opts. When true, root options take precedence over backend-specific
// permission/sandbox options.
func RootOptionsSet(opts map[string]string) bool {
	return opts[agentrun.OptionMode] != "" || opts[agentrun.OptionHITL] != ""
}

// ValidateModeHITL checks OptionMode and OptionHITL for valid values.
// The prefix is used in error messages (e.g., "claude", "codex").
func ValidateModeHITL(prefix string, opts map[string]string) error {
	if mode := agentrun.Mode(opts[agentrun.OptionMode]); mode != "" && !mode.Valid() {
		return fmt.Errorf("%s: unknown mode %q: valid: plan, act", prefix, mode)
	}
	if hitl := agentrun.HITL(opts[agentrun.OptionHITL]); hitl != "" && !hitl.Valid() {
		return fmt.Errorf("%s: unknown hitl %q: valid: on, off", prefix, hitl)
	}
	return nil
}
