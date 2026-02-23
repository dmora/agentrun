# agentrun Design Philosophy

## The Problem

AI agent runtimes are fragmenting: Claude Code (CLI), OpenCode (CLI), Google ADK (API), and more are coming. Each has its own wire format, session model, and configuration surface. Orchestrators that want to support multiple backends face a coupling problem — every backend change ripples through application code.

## The Core Principle

> **Give orchestrators a single vocabulary for talking to any AI agent runtime, without coupling to a specific backend's wire format.**

agentrun is that vocabulary.

## Root is Language, Backends are Dialect

The root package (`agentrun`) defines the **language** — the shared concepts that exist independent of any specific backend. Backend packages translate that language into their specific **dialect** (wire format, CLI flags, API bodies).

| Layer | Root (language) | Backend (dialect) |
|-------|----------------|-------------------|
| Lifecycle | `Engine`, `Process` | `cli.Engine`, `adk.Engine` |
| Output vocabulary | `MessageType` constants | Parser implementations |
| Input vocabulary | `Option*` constants | Flag/API mapping |
| Config (structural) | `Session.Model`, `Session.Prompt` | — |
| Config (cross-cutting) | `OptionSystemPrompt`, `OptionMaxTurns` | `--system-prompt`, `--max-turns` |
| Config (backend-specific) | — | `OptionPermissionMode`, `OptionResumeID` |

### Why Option Keys are Vocabulary

`MessageType` constants define what agents **say** — the output vocabulary. Option key constants define what you **ask of** agents — the input vocabulary. Both are shared concepts that exist across backends. Both belong in root.

`Session.Options` is the extensibility mechanism. Well-known `Option*` constants in root give that map structure and discoverability without making `Session` a kitchen-sink struct.

## Decision Rule: Where Does a Constant Live?

> **Would this concept exist if backend X didn't exist?**
>
> - **Yes** — it goes in the root package.
> - **No** — it goes in the backend package.

### Examples

| Concept | "Exists without Claude?" | Where | Why |
|---------|------------------------|-------|-----|
| `OptionSystemPrompt` | Yes — every LLM accepts one | `agentrun` | Universal input to any model |
| `OptionMaxTurns` | Yes — any agentic loop has a budget | `agentrun` | Universal loop control |
| `OptionThinkingBudget` | Yes — any reasoning model | `agentrun` | Universal reasoning control |
| `OptionPermissionMode` | No — Claude CLI sandboxing | `claude` | Claude-specific subprocess security |
| `OptionResumeID` | No — Claude conversation ID | `claude` | Claude-specific session resumption |
| `MessageText` | Yes — every agent produces text | `agentrun` | Universal output type |
| `MessageThinkingDelta` | Yes — any streaming reasoning model | `agentrun` | Universal streaming output |

### The Test in Practice

When adding a new option key, ask:

1. Would OpenCode need this? Would ADK need this? Would a future MCP backend need this?
2. If yes to 2+ backends → root.
3. If only one backend has the concept → that backend's package.

## Anti-Patterns

### Don't pattern-match on current state

**Wrong reasoning:** "Only the Claude backend exists, so all option keys belong in `claude/`."

This confuses *incidental* state (we built Claude first) with *intentional* design (the library supports many backends). The library's architecture is designed for N backends. Option key placement should reflect the intended design, not the current snapshot.

### Don't duplicate cross-cutting constants across backends

If `claude/` defines `OptionSystemPrompt` and then `opencode/` defines the same thing, that's a signal the constant belongs one level up. The root package is the single source of truth for shared vocabulary.

### Don't expand Session fields for every cross-cutting option

`Session.Model` and `Session.Prompt` are structural fields because they're fundamental to every session. But not every cross-cutting option deserves a struct field — that turns Session into a kitchen-sink. Use `Option*` constants with `Session.Options` for features that backends may or may not support.

## Two Consumer Profiles

The library serves two types of consumers equally:

### Direct backend user
Knows which backend they're using. Imports both root and backend packages. Uses root vocabulary for universal concepts, backend constants for backend-specific features.

```go
session := agentrun.Session{
    Options: map[string]string{
        agentrun.OptionSystemPrompt: "be concise",    // language
        claude.OptionPermissionMode: "bypassAll",      // dialect
    },
}
engine := cli.NewEngine(claude.New())
```

### Orchestrator
Backend-agnostic. Imports only root. Configures sessions using root vocabulary. Backend is injected.

```go
session := agentrun.Session{
    Options: map[string]string{
        agentrun.OptionSystemPrompt:   task.SystemPrompt,
        agentrun.OptionMaxTurns:       "10",
        agentrun.OptionThinkingBudget: "8000",
    },
}
proc, _ := engine.Start(ctx, session) // engine could be anything
```

Both patterns work because they share the same vocabulary. The direct user just also speaks the dialect.
