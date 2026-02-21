# Contributing to agentrun

## Development Setup

```bash
git clone https://github.com/dmora/agentrun.git
cd agentrun
make check    # runs lint + test
```

Requirements:
- Go 1.24+
- [golangci-lint](https://golangci-lint.run/welcome/install/) v2.9+

## Pull Request Process

1. Create a feature branch from `main`
2. Make your changes
3. Ensure `make check` passes (lint + tests with race detector)
4. Ensure `make examples-build` succeeds
5. Open a PR against `main`

All PRs require CI to pass before merge. The lint workflow checks formatting, module tidiness, and linter rules.

## Design Decisions

These conventions guide all implementation work in agentrun. Future implementers should follow these patterns.

### Root package = interfaces + value types only

The `agentrun` package contains only interface definitions and simple value types (Session, Message). No concrete implementations, no constructor functions, no external dependencies. This keeps the import lightweight for consumers who only need the types.

### Interfaces live at the consumer side

Following Go convention, interfaces are defined where they are *used*, not where they are *implemented*:
- `engine/cli/interfaces.go` defines Spawner, Parser, Resumer, Streamer
- `engine/cli/claude/` and `engine/cli/opencode/` implement them
- This allows adding new backends without modifying the interface package

### Capabilities via type assertion

Optional features are separate interfaces, not boolean flags:

```go
// Good: type assertion for optional capability
if r, ok := backend.(cli.Resumer); ok {
    proc, err = r.Resume(ctx, session)
}

// Bad: boolean flag
if backend.SupportsResume() { ... }
```

This keeps the core interface small and lets backends opt in to capabilities naturally.

### Function-field injection for test doubles

Use struct fields with function types for dependency injection instead of mock generation libraries:

```go
type CLIEngine struct {
    SpawnFunc func(ctx context.Context, args []string) (*exec.Cmd, error)
}
```

This approach is simpler, more readable, and avoids build-time code generation.

### enginetest/ compliance suites

Backend authors prove correctness by calling shared test functions:

```go
func TestMyBackend(t *testing.T) {
    enginetest.RunSpawnerTests(t, func() cli.Spawner {
        return mybackend.New()
    })
}
```

The `Run*Tests` pattern (RunSpawnerTests, RunParserTests, etc.) ensures all backends satisfy the same behavioral contract.

### Zero external dependencies

The root `agentrun` package and `engine/cli` package must have zero external dependencies. Only stdlib imports are allowed. Backend packages may import external libraries if absolutely necessary, but should prefer stdlib where possible.

## Design Notes for Future Issues

The following feedback was captured during architecture review and should be addressed in subsequent issues:

- `Process.Send()` may need to distinguish streaming vs resume-per-turn models (consider Sendable type-assertion or split) — #63
- Server restart resilience requires a Recoverer interface for HandleInit/ParseInit duality — #64
- Session may need TransportHint/StreamingMode for turn-boundary detection — #63
- Message should include a RawLine field for crash-recovery log pipeline — #63
- Backend self-registration pattern prevents reverse-engineering wiring — #65-67
- HIPAA requires structured subprocess event logging (SessionEvent audit callback) — #63
- CWD path validation (ValidateProjectPath) should be exported from parent — #65
- Scanner buffer size should be configurable (dropped messages = patient safety issue) — #65
- RecoverableEngine as optional interface keeps API engines clean — #63
- CLIOptions typed struct prevents stringly-typed contract in Session.Options — #64
- Subprocess test helpers (SIGTERM/SIGKILL lifecycle) belong in enginetest/ — #68
