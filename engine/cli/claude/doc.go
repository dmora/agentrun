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
//   - [agentrun.MessageText] — assistant text, may include tool calls via Message.Tool/Tools
//   - [agentrun.MessageThinking] — assistant extended-thinking content, emitted
//     when an assistant event's content array has thinking blocks but no text
//   - [agentrun.MessageToolResult] — completed tool execution (from "tool" events)
//   - [agentrun.MessageResult] — turn completion with optional usage data
//   - [agentrun.MessageSubagentResult] — a subagent's terminal result: a demoted
//     subagent "result" line, or a terminal "task_notification" (background
//     subagent completion)
//   - [agentrun.MessageBackgroundTasks] — an authoritative snapshot of in-flight
//     background subagent tasks (from "background_tasks_changed"), filtered to
//     subagents (local_agent); the tracker's pending set
//   - [agentrun.MessageError] — error events
//
// When partial-message streaming is enabled (the default — see
// [WithPartialMessages]), "stream_event" content_block_delta events also
// produce these token-level delta types:
//
//   - [agentrun.MessageTextDelta] — incremental assistant text
//   - [agentrun.MessageToolUseDelta] — incremental tool_use input JSON
//   - [agentrun.MessageThinkingDelta] — incremental extended-thinking content
//
// Note: [agentrun.MessageToolUse] is never emitted by this backend. Tool
// invocations appear as tool_use blocks inside assistant messages. Every block
// is captured in [agentrun.Message.Tools] (in order); [agentrun.Message.Tool]
// holds the last one (last-one-wins, kept for backward compatibility).
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
//   - [agentrun.OptionSessionName] — sets --name (human-readable session name)
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
// Session resume:
//
//   - [agentrun.OptionResumeID] — backend-agnostic session ID for resume.
//     StreamArgs adds --resume when set and valid. ResumeArgs reads from
//     the same key. Consumers capture the ID from MessageInit.ResumeID.
//
// Claude-specific options:
//
//   - [OptionPermissionMode] — sets --permission-mode (use [PermissionMode] values)
//   - [OptionAllowedTools] — sets --allowedTools (newline-separated tool names)
//   - [OptionRemoteControl] — sets --remote-control (truthy boolean enables MCP remote control)
package claude
