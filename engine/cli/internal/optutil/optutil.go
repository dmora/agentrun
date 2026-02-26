// Package optutil provides shared option resolution helpers for CLI backends.
package optutil

import (
	"fmt"
	"path/filepath"
	"strings"

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

// ValidateEffort checks that OptionEffort, if present, is a recognized value.
// The prefix is used in error messages (e.g., "claude", "codex").
func ValidateEffort(prefix string, opts map[string]string) error {
	if e := agentrun.Effort(opts[agentrun.OptionEffort]); e != "" && !e.Valid() {
		return fmt.Errorf("%s: unknown effort %q: valid: low, medium, high, max", prefix, e)
	}
	return nil
}

// AppendAddDirs appends --add-dir (or the given flagName) for each valid
// entry in OptionAddDirs. Entries must be absolute paths without leading
// dashes. Invalid entries are silently skipped.
func AppendAddDirs(args []string, opts map[string]string, flagName string) []string {
	for _, dir := range agentrun.ParseListOption(opts, agentrun.OptionAddDirs) {
		if filepath.IsAbs(dir) && !strings.HasPrefix(dir, "-") {
			args = append(args, flagName, dir)
		}
	}
	return args
}
