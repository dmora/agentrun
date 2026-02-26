// Package codex provides a Codex CLI backend for agentrun.
//
// This backend implements cli.Spawner, cli.Parser, and cli.Resumer to
// drive Codex as a subprocess, translating its nd-JSON output into
// agentrun.Message values. It does NOT implement cli.Streamer or
// cli.InputFormatter — Codex uses resume-per-turn for multi-turn
// conversation.
//
// # Resume-per-turn pattern
//
// Codex's exec command is single-shot: provide a message, get a
// response, process exits. For multi-turn, each Send() spawns a new
// process via "codex exec resume --json <thread_id> <prompt>".
// The thread ID is auto-captured from the first thread.started event
// and stored in the Backend (one instance per session).
//
// Callers relying on auto-capture must wait for MessageInit before
// calling Send, or supply OptionResumeID upfront.
//
// # Supported options
//
// Cross-cutting (root package):
//   - Session.Model → -m <model>
//   - OptionMode → sandbox policy (ModePlan → --sandbox read-only on exec)
//   - OptionHITL → automation (HITLOff → --full-auto, suppressed by ModePlan)
//   - OptionResumeID → thread ID for exec resume (auto-captured or explicit).
//     Consumers capture the thread ID from MessageInit.ResumeID.
//     Accepts UUID or thread name (codex resolves both).
//
// Backend-specific (namespaced with "codex." prefix):
//   - OptionSandbox → --sandbox (exec only, not resume)
//   - OptionEphemeral → --ephemeral
//   - OptionProfile → -p <profile> (exec only)
//   - OptionOutputSchema → --output-schema <file> (exec only)
//   - OptionSkipGitCheck → --skip-git-repo-check
//
// # Flag support: exec vs exec resume
//
// Exec only (first turn): --sandbox, -p, --output-schema
// Both exec and resume: -m, --full-auto, --ephemeral, --skip-git-repo-check, --json
//
// The sandbox policy established on the first exec persists for the
// session. ModePlan on resume suppresses --full-auto but does not
// emit --sandbox (not supported by the CLI on resume).
//
// # Event types
//
// Codex exec emits JSONL events with a top-level "type" field:
// thread.started, turn.started, item.started, item.completed,
// turn.completed, turn.failed, error.
//
// item.completed contains a nested "item" object with its own "type":
// agent_message, reasoning, command_execution, error, file_changes,
// web_search, mcp_tool_call.
//
// Unlike Claude, Codex emits complete blocks (no streaming deltas)
// and events lack a timestamp field (engine auto-sets via time.Now).
//
// # Minimum tested version
//
// codex-cli v0.105.0 (exec --json format).
package codex
