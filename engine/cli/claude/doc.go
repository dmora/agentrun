// Package claude provides a Claude Code CLI backend for agentrun.
//
// The [Backend] type implements [cli.Spawner], [cli.Parser], [cli.Resumer],
// [cli.Streamer], and [cli.InputFormatter] to drive Claude Code as a
// subprocess, translating its stream-json output into [agentrun.Message]
// values.
//
// # Usage
//
// Create a backend and pass it to [cli.NewEngine]:
//
//	b := claude.New()
//	engine := cli.NewEngine(b)
//
// # Mode Selection
//
// The Claude backend implements both [cli.Streamer] (persistent stdin pipe)
// and [cli.Resumer] (subprocess replacement). The [cli.Engine] selects
// Streamer mode when both are present. Resumer is used only when the backend
// does not implement Streamer.
//
// # Message Types
//
// The Claude backend produces these [agentrun.MessageType] values:
//
//   - [agentrun.MessageInit] — session start (from "system/init" or "init" events)
//   - [agentrun.MessageSystem] — system status messages
//   - [agentrun.MessageText] — assistant text, may include a [agentrun.ToolCall] via Message.Tool
//   - [agentrun.MessageToolResult] — completed tool execution (from "tool" events)
//   - [agentrun.MessageResult] — turn completion with optional usage data
//   - [agentrun.MessageError] — error events
//
// Note: [agentrun.MessageToolUse] is never emitted by this backend. Tool
// invocations appear as tool_use blocks inside assistant messages, captured
// in [agentrun.Message.Tool]. If an assistant message contains multiple
// tool_use blocks, the last one wins (Message.Tool is singular).
//
// # Session Options
//
// The Claude backend honors these cross-cutting options from the root package:
//
//   - [agentrun.OptionSystemPrompt] — sets --system-prompt
//   - [agentrun.OptionMaxTurns] — sets --max-turns
//   - [agentrun.OptionThinkingBudget] — sets --max-thinking-tokens (thinking output)
//
// Cross-cutting session controls (from root package):
//
//   - [agentrun.OptionMode] — sets session intent ("plan" or "act")
//   - [agentrun.OptionHITL] — controls human-in-the-loop ("on" or "off")
//
// When OptionMode or OptionHITL are set, they map to --permission-mode:
//
//   - plan (any HITL) → --permission-mode plan
//   - act + hitl=off → --permission-mode bypassPermissions
//   - act + hitl=on  → default behavior (no flag)
//
// When neither OptionMode nor OptionHITL is set, the backend-specific
// [OptionPermissionMode] is used instead. The two control surfaces are
// independent — root options and backend options are never combined.
//
// Claude-specific options:
//
//   - [OptionPermissionMode] — sets --permission-mode (use [PermissionMode] values)
//   - [OptionResumeID] — Claude conversation ID for --resume (ResumeArgs only)
package claude
