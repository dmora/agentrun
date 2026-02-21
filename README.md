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
        ID:     "s1",
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
├── engine/cli/              CLI subprocess transport
│   ├── claude/              Claude Code backend
│   └── opencode/            OpenCode backend
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

Engine implementations live in subpackages. CLI backends share a generic `cli.CLIEngine` that delegates to backend-specific `Spawner` and `Parser` interfaces. Optional capabilities (resume, streaming) are discovered via type assertion.

## Development

```bash
make qa             # full quality gate (lint + test-race + vet + vulncheck + more)
make check          # fast check (lint + test, no race detector)
make cover          # test with coverage report
make examples-build # build example programs
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## License

[MIT](LICENSE)
