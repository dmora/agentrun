# agentrun — Claude Code Project Guide

## Library-First Mindset

agentrun is a **public Go library** — not an application. Every decision must prioritize external consumers:

- **Composability**: interfaces should be wrappable, decoratable, and mixable without friction. Consumers build their own orchestrators on top of agentrun primitives.
- **Extensibility**: adding a custom backend (CLI or API) should require implementing 1–2 small interfaces, not understanding the whole codebase. No closed registries or internal-only extension points.
- **Greenfield**: no backwards compatibility concerns. Design the best API possible without legacy shims or deprecation paths.
- **Think like a library author**: exported API surface is a contract. Keep it small, intentional, and hard to misuse. Unexported internals can change freely.

## Design Philosophy — Root is Language, Backends are Dialect

See [DESIGN.md](DESIGN.md) for full rationale, examples, and anti-patterns.

The root package defines the **shared vocabulary** for all backends:
- **Output vocabulary**: `MessageType` constants (what agents say)
- **Input vocabulary**: `Option*` constants (what you ask of agents)
- **Structural config**: `Session.Model`, `Session.Prompt`

Backend packages translate vocabulary into their wire format (CLI flags, API bodies).

**Decision rule for where a constant lives:**
> Would this concept exist if backend X didn't exist? **Yes → root. No → backend package.**

Examples: `OptionSystemPrompt` → root (every LLM has one). `OptionPermissionMode` → `claude/` (Claude CLI sandboxing).

**Anti-pattern:** Don't place cross-cutting constants in a backend just because only one backend exists today. Design for the intended architecture (N backends), not the current snapshot.

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
├── filter/              ← Composable channel middleware (Completed, Filter, ResultOnly)
├── engine/cli/          ← CLIEngine: subprocess transport adapter
│   ├── interfaces.go    ← Spawner, Parser, Resumer, Streamer, InputFormatter, Backend
│   ├── engine.go        ← Engine, NewEngine, Validate, Start (!windows)
│   ├── process.go       ← process impl, readLoop, signalProcess (!windows)
│   ├── options.go       ← EngineOptions, EngineOption, With* functions
│   ├── internal/        ← Shared helpers (not importable by consumers)
│   │   ├── jsonutil/    ← JSON extraction (GetString, GetInt, ContainsNull)
│   │   └── optutil/     ← Option resolution (RootOptionsSet)
│   ├── claude/          ← Claude Code backend
│   ├── codex/           ← Codex CLI backend
│   └── opencode/        ← OpenCode backend
├── engine/internal/     ← Shared helpers across all engine types (not importable by consumers)
│   ├── errfmt/          ← Error formatting + ErrorCode sanitization (Truncate, SanitizeCode)
│   └── stoputil/        ← StopReason sanitization (control char rejection, length truncation)
├── engine/acp/          ← ACP engine: JSON-RPC 2.0 persistent subprocess
│   ├── conn.go          ← Bidirectional JSON-RPC 2.0 Conn (platform-agnostic)
│   ├── protocol.go      ← ACP method constants + request/response types
│   ├── update.go        ← session/update → agentrun.Message mapping (usage_update → MessageContextWindow)
│   ├── options.go       ← EngineOptions, PermissionHandler, With* functions
│   ├── engine.go        ← Engine, NewEngine, Validate, Start (!windows)
│   └── process.go       ← process impl, Send, Stop, emit (!windows)
├── engine/api/
│   └── adk/             ← Google ADK API engine
└── enginetest/          ← Compliance test suites (namespace for future root compliance)
    └── clitest/         ← CLI backend compliance: RunBackendTests, RunSpawner/Parser/ResumerTests
```

## Package Structure

| Package | Purpose |
|---------|---------|
| `agentrun` | Root: Engine, Process, Session, Message interfaces/types |
| `filter` | Composable channel middleware for message streams (Completed, Filter, ResultOnly, IsDelta) |
| `engine/cli` | CLI subprocess engine: Backend→Engine adapter, process lifecycle, signal handling |
| `engine/cli/claude` | Claude Code backend (all 5 cli interfaces: Spawner, Parser, Resumer, Streamer, InputFormatter) |
| `engine/cli/codex` | Codex CLI backend (Spawner, Parser, Resumer — resume-per-turn) |
| `engine/cli/opencode` | OpenCode backend (Spawner, Parser, Resumer) |
| `engine/internal/errfmt` | Shared error formatting + ErrorCode sanitization for all engine parsers |
| `engine/cli/internal/jsonutil` | Shared JSON extraction helpers (GetString, GetInt, GetMap, ContainsNull) |
| `engine/cli/internal/optutil` | Shared option resolution + validation (RootOptionsSet, ValidateModeHITL) |
| `engine/internal/stoputil` | Shared StopReason sanitization (control char rejection, rune-safe truncation) — used by CLI and ACP engines |
| `engine/acp` | ACP engine: JSON-RPC 2.0 persistent subprocess, multi-turn without MCP cold boot, surfaces usage_update as MessageContextWindow |
| `engine/api/adk` | Google ADK API engine |
| `enginetest` | Namespace for compliance test suites (reserved for future root Engine/Process compliance) |
| `enginetest/clitest` | CLI backend compliance: RunBackendTests discovers capabilities via type assertion, RunSpawner/Parser/ResumerTests |
| `examples/` | Separate module with runnable examples |

## Key Conventions

- **Zero external dependencies** in the root package — stdlib only
- **Interfaces at consumer side**: `engine/cli/interfaces.go` defines Spawner/Parser/etc; backends implement them
- **Capabilities via type assertion**: optional features (Resumer, Streamer, InputFormatter) are separate interfaces resolved once at Start — no boolean flags
- **Function-field injection** for test doubles — no mock generation libraries
- **`enginetest/clitest` compliance suites**: backends prove correctness via `clitest.RunBackendTests(t, factory)` — discovers Resumer/Streamer/InputFormatter via type assertion. Individual `RunSpawnerTests`, `RunParserTests`, `RunResumerTests` also exported for backends with unusual needs.
- **Separate examples module**: `examples/go.mod` avoids pulling example deps into library consumers
- **Platform build constraints**: Engine implementations using OS-specific features (signals, process groups) use `//go:build !windows` on implementation files. Interface and option files remain platform-agnostic.
- **Signal safety**: All process Signal/Kill calls use `signalProcess()` helper which handles `os.ErrProcessDone` — prevents errors on already-exited processes
- **Cross-cutting session controls**: `Mode` (plan/act), `HITL` (on/off), and `Effort` (low/medium/high/max) types live in root with `Valid()` methods. Root options and backend-specific options (e.g., `claude.OptionPermissionMode`) are independent control surfaces — root wins when set, backend used when absent. Effort validation runs at engine level (`Start()`) for symmetric spawn/resume coverage. See `resolvePermissionFlag()` in Claude backend and `resolveVariant()` in OpenCode backend.
- **Session.Clone()**: Deep-copies Options and Env maps. Used by both CLI and ACP engines in `cloneSession()` — single implementation in root, no "keep in sync" duplication.
- **ValidateModeHITL**: Shared in `engine/cli/internal/optutil` with prefix parameter for error messages. Claude and Codex backends delegate to it.
- **Option parse helpers**: `ParsePositiveIntOption`, `ParseBoolOption`, `StringOption`, `ParseListOption` in `session_options.go` — backends use these instead of scattered `strconv` parsing. Both typed parsers validate null bytes and return `(value, ok, error)`.
- **Session.Env**: Per-session environment variables, merged with parent via `MergeEnv(os.Environ(), session.Env)`. Validated by `ValidateEnv()` (keys: no empty/=/null; values: no null). Both CLI and ACP engines thread env through subprocess spawn. MergeEnv uses last-wins override semantics (appended entries shadow base); returns nil when extra is empty (inherit parent).
- **OptionAddDirs**: Newline-separated absolute paths for additional directory access. Backends apply `filepath.IsAbs` + leading-dash guard per entry. Supported by Claude (`--add-dir`) and Codex (`--add-dir`); OpenCode silently ignores.
- **RunTurn helper**: `runturn.go` encapsulates concurrent Send+drain pattern. Callers must provide a context with deadline/timeout. Safe for all engine types.
- **Shared test infrastructure**: `testutil_test.go` contains `mockProcess` — shared across root-package test files.

## Message Field Reference

Per-field semantics for `Message` metadata. Refer to godoc for full documentation.

- **Usage omitempty semantics**: Existing fields (`InputTokens`, `OutputTokens`) use bare `int` (always serialized, 0 = zero tokens). New fields (`CacheReadTokens`, `CacheWriteTokens`, `ThinkingTokens`, `CostUSD`, `ContextSizeTokens`, `ContextUsedTokens`) use `omitempty` (0 = not reported by this backend). Asymmetry is intentional and documented per-field in godoc. Nil-guard in Claude CLI's `extractTokenUsage` checks token+cost fields atomically; context window fields (`ContextSizeTokens`, `ContextUsedTokens`) are handled separately by ACP's `parseUsageUpdate`.
- **StopReason type**: Output vocabulary (open set, no `Valid()` method). Only 3 universal constants in root (`StopEndTurn`, `StopMaxTokens`, `StopToolUse`). Backend-specific values pass through as raw strings. Consumers should handle unknown values gracefully.
- **StopReason carry-forward**: Claude CLI `result.stop_reason` is null in streaming mode. Real stop_reason arrives in `message_delta` events. Engine readLoop carries it forward via goroutine-local variable (`applyStopReasonCarryForward` in `engine/cli/process.go`). Init resets stale state, result applies (no clobber). Backend stays stateless.
- **CostUSD**: `float64` matching Claude CLI wire format. Parsers must sanitize NaN/Inf/negative to zero before populating. Documented as approximate (not for billing reconciliation). Pragmatic exception to "2+ backends" rule — cost tracking is a universal orchestrator need.
- **ErrorCode**: Machine-readable error code on `MessageError`. Format is backend-specific (CLI: string codes like `"rate_limit"`, ACP: exported constants like `acp.ErrCodeToolCallFailed`). Human-readable description stays in `Content`. Sanitized via `errfmt.SanitizeCode` (reject-on-control, 128-byte cap).
- **MessageContextWindow**: Mid-turn context window fill state. Only `ContextSizeTokens` and `ContextUsedTokens` are meaningful on this message type; other Usage fields are zero and should be ignored. Currently produced by ACP's `usage_update` notification. Cost data intentionally excluded (CostUSD is authoritative only on MessageResult to avoid double-counting).
- **InitMeta**: Init-time metadata (`Model`, `AgentName`, `AgentVersion`) set exclusively on `MessageInit`. Nil on all other message types. Nil-guard: only non-nil when at least one field contains meaningful data (matching `*Usage` discipline). All fields use `omitempty` strings. Claude CLI populates `Model` only. ACP populates all 3 fields (from `initializeResult.AgentInfo` and `sessionModelState.CurrentModelID`). OpenCode/Codex: `Init` stays nil (no init metadata in wire format). All 3 fields sanitized via `errfmt.SanitizeCode` at parse time (control-char rejection + 128-byte cap).
- **Sanitization convention**: Structured metadata/identifier fields on Message (`ErrorCode`, `StopReason`, `InitMeta.*`) are sanitized at parse time via `errfmt.SanitizeCode` (reject-on-control, 128-byte cap). `ResumeID` follows a different pattern: validated by backend-specific format checks (regex in OpenCode/ACP, UUID in Codex) but not sanitized through `errfmt` — Claude CLI stores raw `session_id`. Free-form content (`Content`) is not sanitized — it carries assistant text verbatim.
