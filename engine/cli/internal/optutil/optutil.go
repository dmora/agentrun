// Package optutil provides shared option resolution helpers for CLI backends.
package optutil

import "github.com/dmora/agentrun"

// RootOptionsSet reports whether either OptionMode or OptionHITL is present
// in opts. When true, root options take precedence over backend-specific
// permission/sandbox options.
func RootOptionsSet(opts map[string]string) bool {
	return opts[agentrun.OptionMode] != "" || opts[agentrun.OptionHITL] != ""
}
