// Package claude provides a Claude Code CLI backend for agentrun.
//
// This backend implements the cli.Spawner and cli.Parser interfaces to drive
// Claude Code as a subprocess, translating its JSON output into agentrun.Message
// values.
package claude
