# agentrun — Claude Code Project Guide

## Build & Test Commands

```bash
make check          # lint + test (primary CI gate)
make lint           # golangci-lint run ./...
make test           # go test -race -count=1 ./...
make fmt            # gofmt -w .
make tidy           # go mod tidy
make coverage       # test with coverage report
make vulncheck      # govulncheck ./...
make examples-build # cd examples && go build ./...
```

Run a single test: `go test -race -run TestName ./path/to/package/...`

## Architecture

agentrun is a **two-layer engine pattern**:

1. **Root package** (`agentrun`) — interfaces + value types only (Engine, Process, Session, Message)
2. **Engine packages** — concrete implementations that satisfy root interfaces

```
agentrun (interfaces)
├── engine/cli/          ← CLIEngine: subprocess transport adapter
│   ├── interfaces.go    ← Spawner, Parser, Resumer, Streamer (consumer-side)
│   ├── claude/          ← Claude Code backend
│   └── opencode/        ← OpenCode backend
├── engine/api/
│   └── adk/             ← Google ADK API engine
└── enginetest/          ← Compliance test suites
```

## Package Structure

| Package | Purpose |
|---------|---------|
| `agentrun` | Root: Engine, Process, Session, Message interfaces/types |
| `engine/cli` | Generic CLI subprocess engine + consumer-side interfaces |
| `engine/cli/claude` | Claude Code backend (Spawner + Parser) |
| `engine/cli/opencode` | OpenCode backend (Spawner + Parser) |
| `engine/api/adk` | Google ADK API engine |
| `enginetest` | Shared compliance test suites (RunSpawnerTests, etc.) |
| `examples/` | Separate module with runnable examples |

## Key Conventions

- **Zero external dependencies** in the root package — stdlib only
- **Interfaces at consumer side**: `engine/cli/interfaces.go` defines Spawner/Parser/etc; backends implement them
- **Capabilities via type assertion**: optional features (Resumer, Streamer) are separate interfaces checked at runtime — no boolean flags
- **Function-field injection** for test doubles — no mock generation libraries
- **`enginetest/` compliance suites**: backends prove correctness via `Run*Tests` functions with factory callbacks
- **Separate examples module**: `examples/go.mod` avoids pulling example deps into library consumers
