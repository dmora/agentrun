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

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    "github.com/dmora/agentrun"
    "github.com/dmora/agentrun/engine/cli"
    "github.com/dmora/agentrun/engine/cli/claude"
)

func main() {
    engine := cli.NewEngine(claude.New())

    proc, err := engine.Start(context.Background(), agentrun.Session{
        CWD:    "/path/to/project",
        Prompt: "Hello, world!",
    })
    if err != nil {
        panic(err)
    }

    for msg := range proc.Output() {
        fmt.Println(msg.Content)
    }
}
```

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
- **Session** — minimal session state passed to engines
- **Message** — structured output from agent processes

Engine implementations live in subpackages. CLI backends share a generic `cli.Engine` that delegates to backend-specific `Spawner` and `Parser` interfaces. Optional capabilities (resume, streaming) are discovered via type assertion.

### CLI Backend Interfaces

`Backend` = `Spawner` + `Parser`. Optional capabilities are separate interfaces:

| Interface | Method | Purpose |
|-----------|--------|---------|
| `Spawner` | `SpawnArgs(Session) (string, []string)` | Build command to start a session |
| `Parser` | `ParseLine(string) (Message, error)` | Parse one stdout line into a Message |
| `Resumer` | `ResumeArgs(Session, string) (string, []string, error)` | Resume or start a new turn |
| `Streamer` | `StreamArgs(Session) (string, []string)` | Build long-lived streaming command |
| `InputFormatter` | `FormatInput(string) ([]byte, error)` | Encode messages for stdin pipe |

### Built-in Backends

| Backend | Package | Transport | Resumer | Streamer |
|---------|---------|-----------|:-------:|:--------:|
| Claude Code | `engine/cli/claude` | CLI (streaming stdin) | yes | yes |
| Codex | `engine/cli/codex` | CLI (spawn-per-turn) | yes | — |
| OpenCode | `engine/cli/opencode` | CLI (spawn-per-turn) | yes | — |
| ACP | `engine/acp` | JSON-RPC 2.0 | n/a | n/a |

ACP is a separate engine type (not `cli.Backend`) — it communicates via a persistent JSON-RPC 2.0 subprocess.

## Write a Custom Backend

Implement `Spawner` + `Parser` (required) and `Resumer` (for multi-turn). The CLIEngine handles subprocess lifecycle, stdout scanning, and message pumping.

**Step 1 — Implement the interfaces:**

```go
type myBackend struct{}

func (b *myBackend) SpawnArgs(session agentrun.Session) (string, []string) {
    return "my-agent", []string{"--prompt", session.Prompt}
}

func (b *myBackend) ParseLine(line string) (agentrun.Message, error) {
    // Parse your CLI tool's JSON output into agentrun.Message.
    // Return cli.ErrSkipLine for blank lines or heartbeats.
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

See [`examples/custom-backend`](examples/custom-backend) for a full runnable example.

## Testing

```bash
make qa       # full quality gate
make check    # fast iteration (lint + test)
```

Custom backend authors can verify their implementation against shared safety and correctness contracts using the [compliance suite](enginetest/clitest):

```go
clitest.RunBackendTests(t, func() cli.Backend { return &myBackend{} })
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines and design decisions.

## License

[MIT](LICENSE)
