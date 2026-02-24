// Package opencode provides an OpenCode CLI backend for agentrun.
//
// This backend implements cli.Spawner, cli.Parser, and cli.Resumer to
// drive OpenCode as a subprocess, translating its nd-JSON output into
// agentrun.Message values. It does NOT implement cli.Streamer or
// cli.InputFormatter — OpenCode uses resume-per-turn for multi-turn
// conversation.
//
// # Resume-per-turn pattern
//
// OpenCode's run command is single-shot: provide a message, get a
// response, process exits. For multi-turn, each Send() spawns a new
// process with --session <id> to resume the conversation. The session
// ID is auto-captured from the first step_start event and stored in the
// Backend (one instance per session).
//
// Callers relying on auto-capture must wait for MessageInit before
// calling Send, or supply OptionSessionID upfront.
//
// # Supported options
//
// Cross-cutting (root package):
//   - Session.Model → --model provider/model
//   - Session.AgentID → --agent <id>
//   - OptionThinkingBudget → --thinking (boolean: any non-empty value)
//
// Backend-specific (namespaced with "opencode." prefix):
//   - OptionSessionID → --session (auto-captured or explicit)
//   - OptionVariant → --variant (VariantHigh, VariantMax, VariantMinimal, VariantLow)
//   - OptionFork → --fork (fork session on resume)
//   - OptionTitle → --title (session title, max 512 bytes)
//
// # Event types
//
// OpenCode emits 6 JSON event types: step_start, text, tool_use,
// step_finish, reasoning, error. All events include a top-level
// "timestamp" field (millisecond Unix epoch) and "sessionID".
//
// Unlike Claude, OpenCode emits complete blocks (no streaming deltas)
// and reports tool_use after completion (input + output together).
// Tool events are mapped to MessageToolResult with both Input and
// Output populated on the ToolCall.
package opencode
