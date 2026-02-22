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
// Use the Option* constants as keys in [agentrun.Session.Options]:
//
//   - [OptionSystemPrompt] — sets --system-prompt
//   - [OptionPermissionMode] — sets --permission-mode (use [PermissionMode] values)
//   - [OptionMaxTurns] — sets --max-turns
//   - [OptionResumeID] — Claude conversation ID for --resume (ResumeArgs only)
package claude
