# agentrun — Claude Code Project Guide

## Library-First Mindset

agentrun is a **public Go library** — not an application. Every decision must prioritize external consumers:

- **Composability**: interfaces should be wrappable, decoratable, and mixable without friction. Consumers build their own orchestrators on top of agentrun primitives.
- **Extensibility**: adding a custom backend (CLI or API) should require implementing 1–2 small interfaces, not understanding the whole codebase. No closed registries or internal-only extension points.
- **Greenfield**: no backwards compatibility concerns. Design the best API possible without legacy shims or deprecation paths.
- **Think like a library author**: exported API surface is a contract. Keep it small, intentional, and hard to misuse. Unexported internals can change freely.

## Build & Test Commands

```bash
make qa             # full quality gate (tidy-check + lint + test-race + vet + vulncheck + examples)
make check          # fast check: lint + test (no race detector)
make test           # go test -count=1 ./...
make test-race      # go test -race -count=1 ./...
make lint           # golangci-lint run ./...
make vet            # go vet ./...
make cover          # test with race + coverage report
make tidy-check     # verify go.mod/go.sum are clean
make vulncheck      # govulncheck ./...
make bench          # benchmarks with memory allocation stats
make fuzz           # fuzz tests (30s per target)
make fmt            # gofmt -w .
make tidy           # go mod tidy
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
│   ├── interfaces.go    ← Spawner, Parser, Resumer, Streamer, InputFormatter, Backend
│   ├── engine.go        ← Engine, NewEngine, Validate, Start (!windows)
│   ├── process.go       ← process impl, readLoop, signalProcess (!windows)
│   ├── options.go       ← EngineOptions, EngineOption, With* functions
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
| `engine/cli` | CLI subprocess engine: Backend→Engine adapter, process lifecycle, signal handling |
| `engine/cli/claude` | Claude Code backend (all 5 cli interfaces: Spawner, Parser, Resumer, Streamer, InputFormatter) |
| `engine/cli/opencode` | OpenCode backend (stub) |
| `engine/api/adk` | Google ADK API engine |
| `enginetest` | Shared compliance test suites (RunSpawnerTests, etc.) |
| `examples/` | Separate module with runnable examples |

## Key Conventions

- **Zero external dependencies** in the root package — stdlib only
- **Interfaces at consumer side**: `engine/cli/interfaces.go` defines Spawner/Parser/etc; backends implement them
- **Capabilities via type assertion**: optional features (Resumer, Streamer, InputFormatter) are separate interfaces resolved once at Start — no boolean flags
- **Function-field injection** for test doubles — no mock generation libraries
- **`enginetest/` compliance suites**: backends prove correctness via `Run*Tests` functions with factory callbacks
- **Separate examples module**: `examples/go.mod` avoids pulling example deps into library consumers
- **Platform build constraints**: Engine implementations using OS-specific features (signals, process groups) use `//go:build !windows` on implementation files. Interface and option files remain platform-agnostic.
- **Signal safety**: All process Signal/Kill calls use `signalProcess()` helper which handles `os.ErrProcessDone` — prevents errors on already-exited processes
