# agentrun

[![CI](https://github.com/dmora/agentrun/actions/workflows/ci.yml/badge.svg)](https://github.com/dmora/agentrun/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/dmora/agentrun)](https://goreportcard.com/report/github.com/dmora/agentrun)
[![Go Reference](https://pkg.go.dev/badge/github.com/dmora/agentrun.svg)](https://pkg.go.dev/github.com/dmora/agentrun)

Composable Go interfaces for running AI agent sessions.

**agentrun** is a zero-dependency Go library that abstracts over different AI agent runtimes (CLI subprocesses, API clients) with a uniform Engine/Process model. Build agent orchestrators without coupling to any specific AI tool.

## Installation

```bash
go get github.com/dmora/agentrun
```

Each backend requires its CLI tool installed and on your `PATH`. The Quick Start below uses Claude Code (`claude`).

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/dmora/agentrun"
    "github.com/dmora/agentrun/engine/cli"
    "github.com/dmora/agentrun/engine/cli/claude"
)

func main() {
    engine := cli.NewEngine(claude.New())

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    proc, err := engine.Start(ctx, agentrun.Session{
        CWD:    "/path/to/project",
        Prompt: "Hello, world!",
    })
    if err != nil {
        panic(err)
    }
    defer proc.Stop(ctx)

    for msg := range proc.Output() {
        fmt.Println(msg.Content)
    }
    if err := proc.Err(); err != nil {
        fmt.Printf("session error: %v\n", err)
    }
}
```

## Process Lifecycle

`Engine.Start()` returns a `Process` — the active session handle:

| Method | Description |
|--------|-------------|
| `Output()` | Returns `<-chan Message` for receiving agent output |
| `Send(ctx, msg)` | Sends a follow-up message to the agent |
| `Stop(ctx)` | Terminates the subprocess (blocks until output channel closes) |
| `Wait()` | Blocks until the session ends naturally |
| `Err()` | Returns the terminal error, or nil if still running |

`Output()` is the primary consumption path — range over it to receive messages. When the channel closes, check `Err()` for the exit status.

## Multi-Turn Conversations

Use `RunTurn` for safe concurrent Send+drain. It handles ACP's blocking RPC and works with all engine types:

```go
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()

err := agentrun.RunTurn(ctx, proc, "Explain the auth module", func(msg agentrun.Message) error {
    fmt.Println(msg.Content)
    return nil
})
```

`RunTurn` sends the message in a goroutine while draining `Output()` on the calling goroutine. It returns when `MessageResult` arrives, the channel closes, or context expires.

Turn semantics differ by backend:
- **Streaming** (Claude, ACP) — persistent subprocess, messages flow on a shared channel
- **Spawn-per-turn** (OpenCode, Codex) — each turn spawns a new subprocess via `Resumer`. Call `Output()` at the start of each turn rather than caching the channel across turns.

See [`examples/interactive`](examples/interactive) for a full multi-turn REPL.

## Filtering Messages

The `filter` package provides composable channel middleware for message streams. Each filter consumes an input channel and returns a new, filtered channel — they can be chained:

```go
import "github.com/dmora/agentrun/filter"

// Drop streaming deltas, keep only complete messages.
for msg := range filter.Completed(ctx, proc.Output()) {
    fmt.Println(msg.Content)
}
```

Available filters:

| Function | Effect |
|----------|--------|
| `filter.Completed(ctx, ch)` | Drops streaming deltas, passes everything else |
| `filter.ResultOnly(ctx, ch)` | Keeps only `MessageResult` |
| `filter.Filter(ctx, ch, types...)` | Keeps only the specified message types |

The `filter.IsDelta(t)` predicate returns true for `_delta` message types — useful in custom filtering logic.

## Message Types

Messages from `proc.Output()` carry a `Type` field indicating the kind of content:

| Type | Constant | Description |
|------|----------|-------------|
| `text` | `MessageText` | Assistant text output |
| `tool_use` | `MessageToolUse` | Agent is invoking a tool |
| `tool_result` | `MessageToolResult` | Tool invocation output |
| `error` | `MessageError` | Error from agent or runtime |
| `system` | `MessageSystem` | System-level status changes |
| `init` | `MessageInit` | Handshake at session start |
| `result` | `MessageResult` | Turn completion with usage data |
| `context_window` | `MessageContextWindow` | Mid-turn context window fill state |
| `thinking` | `MessageThinking` | Complete thinking/reasoning block |
| `eof` | `MessageEOF` | End of message stream |

**Streaming deltas** — partial content from token-level streaming:

| Type | Constant | Description |
|------|----------|-------------|
| `text_delta` | `MessageTextDelta` | Partial text token |
| `tool_use_delta` | `MessageToolUseDelta` | Partial tool use JSON |
| `thinking_delta` | `MessageThinkingDelta` | Partial thinking content |

## Message Metadata

Messages carry structured metadata beyond `Content`. Key fields on the `Message` struct:

```go
type Message struct {
    Type       MessageType      // kind of message (see table above)
    Content    string           // text content (semantics vary by Type)
    Tool       *ToolCall        // tool invocation details (tool_use, tool_result)
    Usage      *Usage           // token counts and cost (result, context_window)
    StopReason StopReason       // why the turn ended (result only)
    ErrorCode  string           // machine-readable error code (error only)
    ResumeID   string           // session ID for resume (init only)
    Init       *InitMeta        // model, agent name/version (init only)
    Process    *ProcessMeta     // subprocess PID and binary (init only)
    Raw        json.RawMessage  // original unparsed JSON
    Timestamp  time.Time        // when the message was produced
}
```

Pointer fields (`Usage`, `Init`, `Process`, `Tool`) are nil unless meaningful data is present.

**Accessing result metadata:**

```go
for msg := range proc.Output() {
    if msg.Type == agentrun.MessageResult && msg.Usage != nil {
        fmt.Printf("tokens: in=%d out=%d cost=$%.4f\n",
            msg.Usage.InputTokens, msg.Usage.OutputTokens, msg.Usage.CostUSD)
        fmt.Printf("stop: %s\n", msg.StopReason)
    }
}
```

**Init metadata** — captured at session start:
- `Init.Model` — model identifier (all backends)
- `Init.AgentName`, `Init.AgentVersion` — agent identity (ACP only)
- `Process.PID`, `Process.Binary` — subprocess info (CLI/ACP engines)
- `ResumeID` — persist and pass back via `OptionResumeID` to resume later

**Error metadata:**
- `ErrorCode` — machine-readable code (e.g., `"rate_limit"`); human description in `Content`

## Session Configuration

Sessions carry cross-cutting options that backends translate into CLI flags or API parameters:

```go
session := agentrun.Session{
    CWD:    "/path/to/project",
    Prompt: "Refactor the auth module",
    Model:  "claude-sonnet-4-20250514",
    Options: map[string]string{
        agentrun.OptionSystemPrompt:   "You are a Go expert.",
        agentrun.OptionMode:           "act",                    // plan or act
        agentrun.OptionEffort:         "high",                   // low/medium/high/max
        agentrun.OptionThinkingBudget: "10000",                  // enable extended thinking
        agentrun.OptionHITL:           "off",                    // human-in-the-loop
        agentrun.OptionAddDirs:        "/shared/lib\n/shared/proto", // newline-separated
    },
    Env: map[string]string{
        "OPENCODE_AUTO_APPROVE": "1",
    },
}
```

Backend-specific options use a namespace prefix (e.g., `claude.OptionPermissionMode`, `codex.OptionSandbox`). See each backend package for available options.

## Error Handling

Sentinel errors for engine operations:

| Error | Meaning |
|-------|---------|
| `ErrUnavailable` | Engine cannot start (binary not found, API unreachable) |
| `ErrTerminated` | Session was terminated (`Stop()` called, connection closed) |
| `ErrSendNotSupported` | Backend lacks Send capability |

Subprocess exit codes are wrapped in `*ExitError`. Use `ExitCode()` to extract:

```go
err := proc.Wait()
if code, ok := agentrun.ExitCode(err); ok {
    fmt.Printf("agent exited with code %d\n", code)
}
```

`ErrTerminated` always takes precedence over `ExitError` when `Stop()` is called.

## Architecture

```
agentrun (interfaces + value types)
│
├── filter/                  Composable channel middleware
│
├── engine/cli/              CLI subprocess transport
│   ├── claude/              Claude Code backend
│   ├── codex/               Codex CLI backend
│   └── opencode/            OpenCode backend
│
├── engine/acp/              ACP JSON-RPC 2.0 engine
│
├── engine/api/
│   └── adk/                 Google ADK API engine
│
└── enginetest/              Compliance test suites
```

The root `agentrun` package defines four core types:

- **Engine** — starts and validates agent sessions
- **Process** — an active session handle with output channel
- **Session** — minimal session state passed to engines (value type)
- **Message** — structured output from agent processes

Engine implementations live in subpackages. CLI backends share a generic `cli.Engine` that delegates to backend-specific interfaces. Optional capabilities (resume, streaming) are discovered via type assertion.

### Built-in Backends

| Backend | Package | Transport | Resumer | Streamer |
|---------|---------|-----------|:-------:|:--------:|
| Claude Code | `engine/cli/claude` | CLI (streaming stdin) | yes | yes |
| Codex | `engine/cli/codex` | CLI (spawn-per-turn) | yes | — |
| OpenCode | `engine/cli/opencode` | CLI (spawn-per-turn) | yes | — |
| ACP | `engine/acp` | JSON-RPC 2.0 | n/a | n/a |

ACP is a separate engine type (not `cli.Backend`) — it communicates via a persistent JSON-RPC 2.0 subprocess.

## Write a Custom Backend

Implement `Spawner` + `Parser` (required) and `Resumer` (for multi-turn). The `cli.Engine` handles subprocess lifecycle, stdout scanning, and message pumping.

CLI backend interfaces (`Backend` = `Spawner` + `Parser`, optional capabilities are separate):

| Interface | Method | Purpose |
|-----------|--------|---------|
| `Spawner` | `SpawnArgs(Session) (string, []string)` | Build command to start a session |
| `Parser` | `ParseLine(string) (Message, error)` | Parse one stdout line into a Message |
| `Resumer` | `ResumeArgs(Session, string) (string, []string, error)` | Resume or start a new turn |
| `Streamer` | `StreamArgs(Session) (string, []string)` | Build long-lived streaming command |
| `InputFormatter` | `FormatInput(string) ([]byte, error)` | Encode messages for stdin pipe |

**Step 1 — Implement the interfaces:**

```go
type myBackend struct{}

func (b *myBackend) SpawnArgs(session agentrun.Session) (string, []string) {
    return "my-agent", []string{"--prompt", session.Prompt}
}

func (b *myBackend) ParseLine(line string) (agentrun.Message, error) {
    // Parse your CLI tool's JSON output into agentrun.Message.
    // Return cli.ErrSkipLine for blank lines or heartbeats.
    return agentrun.Message{}, cli.ErrSkipLine
}

func (b *myBackend) ResumeArgs(session agentrun.Session, msg string) (string, []string, error) {
    return "my-agent", []string{"--resume", session.Options[agentrun.OptionResumeID], "--prompt", msg}, nil
}
```

**Step 2 — Wire into the engine:**

```go
engine := cli.NewEngine(&myBackend{})
proc, _ := engine.Start(ctx, agentrun.Session{CWD: cwd, Prompt: "Hello"})
```

**Step 3 — Verify with the compliance suite:**

```go
func TestCompliance(t *testing.T) {
    clitest.RunBackendTests(t, func() cli.Backend { return &myBackend{} })
}
```

See [`examples/custom-backend`](examples/custom-backend) for a full runnable example. See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## License

[MIT](LICENSE)
