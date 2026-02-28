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
3. Run `make qa` (full quality gate: tidy-check, lint, test with race detector, vet, vulncheck, examples)
4. Open a PR against `main`

Use `make check` for fast iteration (lint + test without race detector). All PRs require CI to pass before merge.

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

### enginetest/clitest compliance suites

Backend authors prove correctness by calling `RunBackendTests` with a factory callback. The suite discovers optional capabilities (Resumer, Streamer, InputFormatter) via type assertion:

```go
package mybackend_test

import (
    "testing"
    "github.com/dmora/agentrun/engine/cli"
    "github.com/dmora/agentrun/engine/cli/mybackend"
    "github.com/dmora/agentrun/enginetest/clitest"
)

func TestCompliance(t *testing.T) {
    clitest.RunBackendTests(t, func() cli.Backend {
        return mybackend.New()
    })
}
```

Individual `RunSpawnerTests`, `RunParserTests`, and `RunResumerTests` are also exported for backends with unusual needs.

### Zero external dependencies

The root `agentrun` package and `engine/cli` package must have zero external dependencies. Only stdlib imports are allowed. Backend packages may import external libraries if absolutely necessary, but should prefer stdlib where possible.

## Message Field Conventions

When adding new fields to `Message` or its nested types, follow these patterns:

### Nil-guard pointer fields

Metadata structs (`*Usage`, `*InitMeta`, `*ProcessMeta`, `*ToolCall`) are pointer fields with `omitempty`. Only set them when at least one sub-field is meaningful — a non-nil pointer always carries real data:

```go
// Good: only set Usage when there's actual data.
if inputTokens > 0 || outputTokens > 0 {
    msg.Usage = &agentrun.Usage{InputTokens: inputTokens, OutputTokens: outputTokens}
}

// Bad: always allocating with zero values.
msg.Usage = &agentrun.Usage{}
```

### Sanitization at parse time

Structured metadata fields from wire data (`ErrorCode`, `StopReason`, `InitMeta.*`) must be sanitized at parse time via `errfmt.SanitizeCode` (control-char rejection, 128-byte cap). Free-form content (`Content`) is not sanitized — it carries assistant text verbatim. Engine-constructed values (`ProcessMeta.PID`, `ProcessMeta.Binary`) come from `exec.Cmd` and need no sanitization.

### omitempty semantics

Use bare types (always serialized) for primary fields that are meaningful at zero: `InputTokens`, `OutputTokens`. Use `omitempty` for optional fields where zero means "not reported by this backend": `CacheReadTokens`, `CostUSD`, `ContextSizeTokens`.
