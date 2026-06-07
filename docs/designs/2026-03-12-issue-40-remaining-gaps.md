# Design: Issue #40 — Remaining Gaps After Partial Implementation

## 4.1 Context

### Problem Statement

Issue dmora/agentrun#40 identified four abstraction leaks exposed by the MCP diagnostic server (the first non-trivial consumer). A prior design document (`docs/designs/2026-03-09-unify-context-fill-and-turn-abstraction.md`) proposed solutions. Since then, significant implementation has landed. This document reconciles the current codebase state with the original four leaks and designs solutions for what remains.

### Leak Status Matrix

| # | Leak | Status | Evidence |
|---|------|--------|----------|
| 1 | `spawnPerTurn` branching duplicated in consumers | **Resolved** | `SequentialSender` interface (`process.go:68-71`), `RunTurn` type-asserts on it (`runturn.go:29`), CLI wraps spawn-per-turn processes (`engine/cli/process.go:81-87`). MCP server uses `RunTurn` uniformly. |
| 2 | `makeEngine` returns a boolean consumers must track | **Resolved** | `makeEngine` returns `(Engine, error)` only (`cmd/agentrun-mcp/engine.go:24`). `sessionEntry` has no `spawnPerTurn` field (`session.go:27-36`). Interactive example uses `RunTurn` with no branching (`examples/interactive/main.go:188-194`). |
| 3 | No unified turn summary | **Open** | MCP server collects messages into `[]agentrun.Message` and returns raw arrays (`tools.go:367-371`). No `TurnSummary` type in root package. |
| 4 | Context fill requires filtering 3+ message types | **Partially resolved** | CLI engine synthesizes `MessageContextWindow` via `synthesizeContextWindow` (`process.go:387-404`). `ContextFill` helper exists (`contextfill.go:1-17`). **Gap**: `ContextSizeTokens` is always 0 for CLI — no percentage-based fuel gauge. |

### Remaining Work

Two gaps remain open:

**Gap A: TurnSummary** — Every consumer reimplements message-iteration logic to extract text, usage, stop reason, denials, and errors from a turn's message stream. The MCP server (`tools.go:327-340`, `tools.go:367-386`) and any Foundry integration will duplicate this.

**Gap B: ContextSizeTokens for CLI** — CLI backends emit `MessageContextWindow` with `ContextUsedTokens` but `ContextSizeTokens == 0`. Consumers cannot compute fill percentage. ACP provides both fields natively. The issue hypothesizes that Claude CLI's result event includes `modelUsage.<model>.contextWindow`, but this was **not confirmed** in the parser (`engine/cli/claude/parse.go:299-331` — no `contextWindow` extraction).

### Known (from codebase)

- `ContextFill(msg)` in `contextfill.go:9-17` returns `(used, size, ok)` — works for any message type.
- `synthesizeContextWindow` in `process.go:387-404` creates `MessageContextWindow` from mid-turn content messages with fill data. Excludes `MessageResult`, `MessageInit`, `MessageError`.
- `emitWithSynthesis` in `process.go:365-382` emits content message then synthesized `MessageContextWindow` in order.
- `applyContextFill` in `process.go:518-539` tracks `maxCallFill` across a turn. Uses `callContextFill` which sums `InputTokens + CacheReadTokens + CacheWriteTokens`.
- `collectTerminalState` in MCP `tools.go:327-340` is the closest thing to a turn summary — but it's MCP-specific and only captures messages, duration, stderr, and exit code.
- `runTurnOutput` in `tools.go:272-278` carries `Messages []agentrun.Message` — raw, unsummarized.
- The `filter` package (`filter/filter.go`) provides `Filter`, `Completed`, `ResultOnly`, `IsDelta`. No context-fill-specific filter (but `Filter(ctx, ch, MessageContextWindow)` already works post-synthesis).
- Claude CLI parser `extractTokenUsage` (`engine/cli/claude/parse.go`) parses `usage.input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output_tokens`, `thinking_tokens`, and `costUSD` from `result` events. No evidence of `contextWindow` or `context_window` field parsing.

### Assumed

- **A1**: Claude CLI's wire format may include context window capacity in some event type (issue references `modelUsage.<model>.contextWindow` on result events). Unconfirmed — grep across the Claude backend found zero matches. This is the **critical unknown** for Gap B.
- **A2**: Future API engines (ADK) will provide their own context window reporting and won't rely on CLI synthesis.
- **A3**: TurnSummary is needed by at least 2 consumers (MCP server, Foundry) to justify a root-package type.

### Missing

- **M1**: Claude CLI wire format sample showing `contextWindow` field (or confirmation it doesn't exist).
- **M2**: Whether Foundry currently reimplements turn message iteration (and in how many places).
- **M3**: Whether consumers need turn-level peak context fill in the summary, or just per-message signals.

### Context Manifest

Files examined:
- `process.go` (lines 53-71) — SequentialSender interface
- `runturn.go` (lines 1-91) — RunTurn, drainOutput
- `contextfill.go` (lines 1-17) — ContextFill helper
- `message.go` (lines 1-348) — Message, Usage, MessageType constants
- `engine/cli/process.go` (lines 81-87, 300-539) — sequentialProcess, scanLines, emitWithSynthesis, synthesizeContextWindow, applyContextFill
- `engine/cli/engine.go` (lines 1-178) — Engine.Start, resolveCapabilities
- `engine/acp/update.go` (known from prior analysis) — parseUsageUpdate
- `cmd/agentrun-mcp/tools.go` (lines 1-730) — all tool handlers, doRunTurn, collectTerminalState
- `cmd/agentrun-mcp/engine.go` (lines 1-41) — makeEngine (no boolean return)
- `cmd/agentrun-mcp/session.go` (lines 1-207) — sessionEntry (no spawnPerTurn field)
- `examples/interactive/main.go` (lines 1-221) — no spawnPerTurn branching, uses RunTurn
- `filter/filter.go` (lines 1-88) — Filter, Completed, ResultOnly, IsDelta
- `engine/cli/claude/parse.go` (known from prior analysis) — extractTokenUsage, no contextWindow
- `docs/designs/2026-03-09-unify-context-fill-and-turn-abstraction.md` — prior design document

---

## 4.2 Solution Pool

### Gap A: TurnSummary

#### Candidate A1: `TurnSummary` Type + `CollectTurn` Function in Root Package

A struct that aggregates turn-level data: accumulated text, thinking text, tool calls, total usage, stop reason, denials, errors. A `CollectTurn(ctx, proc, message) (TurnSummary, error)` function wraps `RunTurn` internally and populates the summary.

**Approach:** `CollectTurn` calls `RunTurn` with an internal handler that dispatches messages into a `TurnSummary` accumulator. Returns the completed summary. Consumers also get a `TurnCollector` (stateful accumulator) for those who need the raw handler callback (e.g., to also stream deltas to a UI).

**Strengths:**
- Eliminates repeated iteration boilerplate.
- Type-safe access to turn results (stop reason, denials, usage) without manual message filtering.
- Both MCP server and Foundry can adopt immediately.
- `TurnCollector` variant lets streaming consumers accumulate summaries while also forwarding messages.

**Weaknesses:**
- Locks in a summary schema — new message fields require `TurnSummary` updates.
- Consumers with custom aggregation (e.g., per-tool-call cost attribution) bypass it.
- Increased root package API surface.
- Text accumulation: should it concatenate all `MessageText` content? What about multi-text turns (Claude CLI emits multiple text blocks in tool-use loops)?

**Fit:**
Good. The MCP server's `collectTerminalState` + raw messages pattern maps directly. The root package already has `RunTurn` and `ContextFill` as consumer helpers.

#### Candidate A2: `TurnSummary` as a Standalone Accumulator (No `CollectTurn` Wrapper)

A struct with an `Add(msg Message)` method that consumers call from their existing `RunTurn` handler. No wrapping of `RunTurn` itself.

**Approach:** Consumers create a `TurnSummary`, pass `summary.Add` as the handler (or call it within their handler), and read fields after the turn completes.

**Strengths:**
- Composable — works with any message consumption pattern (RunTurn callback, channel iteration, etc.).
- Minimal API surface: one type, one method.
- Consumer retains full control of the handler.

**Weaknesses:**
- Consumer still writes `RunTurn(ctx, proc, msg, summary.Add)` or equivalent — slight boilerplate remains.
- `Add` must handle all message types correctly, including idempotency if called twice with the same message (e.g., consumer logging + accumulating).
- No "just give me the answer" convenience — always requires manual wiring.

**Fit:**
Good for library-first philosophy (composable, no hidden magic). Slightly more ceremony than A1 but more flexible.

#### Candidate A3: Leave Summarization to Consumers (Status Quo)

No library type. Consumers continue iterating messages with their own logic.

**Strengths:** Maximum flexibility, no schema maintenance.

**Weaknesses:** Repeated boilerplate. Bug risk in reimplemented iteration logic (e.g., forgetting to check `msg.Usage` nil guard). Every consumer derives the same 5 fields.

**Fit:** Acceptable for a library with 1-2 consumers. But with MCP server + Foundry + examples, the duplication is real.

### Self-Critique: Gap A

| Candidate | Strongest counter-argument | Worst case | Hidden cost |
|-----------|---------------------------|------------|-------------|
| **A1 (CollectTurn wrapper)** | Hides the message stream — consumers who want both streaming UI and summary must either use TurnCollector (increased API surface) or re-derive the summary from messages anyway. | New message fields added to `Message` but not to `TurnSummary` — summary silently loses data until someone notices. | Schema maintenance: every new metadata field on Message requires a `TurnSummary` update. The summary and the message diverge. |
| **A2 (standalone accumulator)** | `summary.Add` is a one-liner, but consumers still need to know it exists and wire it in. Marginal improvement over manual iteration for simple cases. | Consumer calls `Add` in wrong order or misses messages — accumulator returns partial summary without error. | Thread safety: if multiple goroutines call `Add` (unlikely but possible with custom drain loops), the accumulator needs a mutex or "single-writer only" contract. |
| **A3 (status quo)** | Three consumers already duplicate this logic. Each will independently discover edge cases (nil Usage, missing StopReason, empty Denials). | A consumer mishandles nil `Usage` and panics in production. | Time cost: every new consumer author reads Message godoc and writes iteration logic from scratch. |

**Preferred: A2 (standalone accumulator)** — composable, minimal API surface, no hidden wrapping. With an optional `CollectTurn` convenience added only if the two-line wiring proves too verbose in practice.

### Gap B: ContextSizeTokens for CLI

#### Candidate B1: Capture `contextWindow` from Claude CLI Wire Format (If Available)

The Claude backend parser extracts a `contextWindow` field from result events (or init events) and stores it on the message. The CLI engine caches this value and populates `ContextSizeTokens` on synthesized `MessageContextWindow` messages.

**Approach:** Add extraction logic to `extractTokenUsage` in `engine/cli/claude/parse.go`. Store the model's context window size on `Usage.ContextSizeTokens` on the `MessageResult`. The CLI engine caches this value (`contextSize` field on `process`) and uses it for subsequent `MessageContextWindow` synthesis.

**Strengths:**
- Full fuel gauge: consumers get `ContextSizeTokens` + `ContextUsedTokens` for percentage computation.
- Unified consumer experience across CLI and ACP.

**Weaknesses:**
- **Depends on unconfirmed wire format.** If the field doesn't exist, this approach is impossible.
- First-turn mid-turn messages lack `ContextSizeTokens` until the first result populates the cache. This creates a "warm-up" gap where the denominator is 0.
- Couples the CLI engine to Claude-specific wire format details.
- Adds state to the CLI process (cached `contextSize`), complicating the stateless-parser model.

**Fit:** Ideal if wire data exists. Blocked until confirmed.

#### Candidate B2: Hardcode Model Context Sizes in Claude Backend

Maintain a map of known Claude model IDs to their context window sizes. Populate `ContextSizeTokens` from this map based on `InitMeta.Model`.

**Approach:** `claude.ModelContextSize` map. On `MessageInit` (which carries `InitMeta.Model`), the engine looks up the context size and caches it for `MessageContextWindow` synthesis.

**Strengths:**
- Available immediately (no wire format dependency).
- First-turn `MessageContextWindow` messages carry `ContextSizeTokens` from the start.

**Weaknesses:**
- **Maintenance burden.** New Claude model releases require library updates. Stale map → wrong percentages.
- Anthropic has released models with varying context sizes (100K, 200K). A wrong size is worse than no size.
- Violates the "backends parse wire data" principle — this is static config masquerading as dynamic data.
- Other CLI backends (Codex, OpenCode) still lack `ContextSizeTokens`.

**Fit:** Fragile. A library should not maintain a model registry.

#### Candidate B3: Consumer-Supplied Context Size via Session Option

Add `OptionContextSize` to the root package. Consumers set it when they know the model's context size (e.g., from an API call, config file, or prior turn's result). The CLI engine uses this value for `MessageContextWindow` synthesis.

**Approach:** Consumers who need percentage-based fuel gauges provide `session.Options[agentrun.OptionContextSize] = "200000"`. The CLI engine reads this at Start time and populates `ContextSizeTokens` on synthesized messages. If not set, `ContextSizeTokens` remains 0 (absolute-only).

**Strengths:**
- No wire format dependency.
- Consumer controls the value — can use any source (API metadata, config, hardcoded).
- Works for all CLI backends (not just Claude).
- Library stays out of the model-registry business.

**Weaknesses:**
- Consumer must know the context size out-of-band. Many won't.
- `ContextSizeTokens` varies by model — if the model changes mid-session (unlikely but possible), the cached value is stale.
- Adds an option constant to the root package for a niche use case.

**Fit:** Pragmatic escape hatch. Doesn't solve the common case (consumer doesn't know context size) but enables power users.

#### Candidate B4: Accept CLI Limitation; Document as Backend Difference

CLI's `MessageContextWindow` carries absolute `ContextUsedTokens` only. Consumers who need percentage use ACP (which provides both fields). Document this as a known backend capability difference.

**Approach:** No code changes. Update godoc to clearly state that `ContextSizeTokens == 0` on CLI means "capacity unknown — use absolute value only." Consumers check `ContextSizeTokens > 0` before computing percentage.

**Strengths:**
- Zero complexity. No maintenance burden.
- Honest — if the wire data doesn't exist, don't fabricate it.
- Consumers already need to handle `ContextSizeTokens == 0` (per `Usage.ContextSizeTokens` godoc).

**Weaknesses:**
- CLI consumers can't build percentage-based fuel gauges. The core issue's motivating use case is only half-solved for CLI.
- "Just use ACP" isn't always an option — Claude Code CLI is the most common backend.

**Fit:** Acceptable as a near-term position while waiting for wire format confirmation. Unacceptable as a permanent answer if the wire data can be obtained.

### Self-Critique: Gap B

| Candidate | Strongest counter-argument | Worst case | Hidden cost |
|-----------|---------------------------|------------|-------------|
| **B1 (wire capture)** | Blocked on unconfirmed wire data. May never be possible. | Anthropic changes the field location or semantics in a Claude CLI update — silent data regression. | State management (cached contextSize on process struct). Parser loses stateless property for one field. |
| **B2 (hardcoded map)** | A library maintaining a model registry is a maintenance anti-pattern. First wrong value shipped to consumers poisons trust. | New model released with 1M context window. Library shows 200K. Consumer thinks they have 80% remaining when they really have 20%. | Every model release cycle requires a library patch. Semantic versioning pressure to add models without breaking changes. |
| **B3 (consumer option)** | Shifts the burden to every consumer. Most won't know the value. The option exists but solves the problem for ~10% of use cases. | Consumer hardcodes 200K for a model that actually has 128K. Fill gauge shows 60% when the real fill is 93%. Agent runs out of context unexpectedly. | Option validation (must be positive int). Documentation explaining where consumers can find context sizes. |
| **B4 (accept limitation)** | The issue's motivating example is a fuel gauge. "No percentage for CLI" is a non-answer for the primary use case. | Consumer builds a fuel gauge, deploys it, discovers it only works for ACP backends — has to rearchitect after shipping. | None, but opportunity cost: the issue stays partially unresolved indefinitely. |

**Preferred: B4 (accept limitation) as immediate position + B1 (wire capture) as follow-up when confirmed.**

The rationale: fabricating `ContextSizeTokens` from a hardcoded map (B2) or consumer input (B3) creates false precision that's worse than no value. The honest answer is "CLI doesn't report this." If the wire data is confirmed (B1), it should be captured — but that's a separate, gated task.

---

## 4.3 Decisions

### ADR-1: Provide `TurnSummary` as Standalone Accumulator

> In the context of **multiple consumers (MCP server, Foundry, future orchestrators) reimplementing message-iteration logic to extract turn results**, facing **the desire for a reusable summary without hiding the raw message stream**, we decided **to add a `TurnSummary` struct with an `Add(Message)` method in the root package**, and neglected **a `CollectTurn` wrapper around RunTurn (A1, hides message stream from consumers who want both streaming and summary) and status quo (A3, continued duplication and nil-guard bugs)**, to achieve **a composable, opt-in accumulator that consumers wire into any message consumption pattern**, accepting **marginal wiring boilerplate (`summary.Add` in handler) and a schema that must grow with new Message metadata fields**.

Confidence: **medium** — the pattern is sound, but the exact field set needs validation with both MCP server and Foundry to ensure the summary captures what both consumers actually need. The schema risk (growing with new fields) is real but manageable if fields are added conservatively.

Rejected: **A1 (CollectTurn)** — wrapping RunTurn hides the message stream. Consumers who want both live streaming (delta forwarding) and post-turn summary can't use it without the TurnCollector variant, which doubles the API surface. **A3 (status quo)** — three+ consumers reimplementing the same nil-guarded iteration is a bug farm.

Grounding: MCP server's `collectTerminalState` (`tools.go:327-340`) already derives duration, exit code, and error from process state. `doRunTurn` (`tools.go:367-386`) collects messages into a slice but doesn't extract text, usage, or stop reason. Foundry's integration (per memory: "~10 lines to add backend in `internal/agent/agentrun.go:buildEngine`") will need the same extraction.

### ADR-2: Accept CLI ContextSizeTokens Limitation; Gate Wire Capture on Confirmation

> In the context of **CLI backends emitting `MessageContextWindow` with `ContextUsedTokens` but `ContextSizeTokens == 0`**, facing **the inability to compute fill percentage without knowing context window capacity**, we decided **to document this as a known backend capability difference and defer `ContextSizeTokens` capture until the Claude CLI wire format is confirmed to include a `contextWindow` field**, and neglected **hardcoding model context sizes (B2, maintenance anti-pattern, false precision) and consumer-supplied option (B3, shifts burden, solves <10% of cases)**, to achieve **honest reporting (absolute fill when capacity is unknown) without false precision**, accepting **that CLI consumers cannot build percentage-based fuel gauges until the wire format is confirmed or a future solution emerges**.

Confidence: **medium** — this is the right near-term position, but it leaves the motivating use case (fuel gauge percentage) unsolved for CLI. If the wire format is confirmed, B1 should be implemented immediately as an amendment.

Rejected: **B2 (hardcoded map)** — a library maintaining a model context-size registry is a maintenance anti-pattern. First wrong value shipped to consumers poisons trust in the fuel gauge. **B3 (consumer option)** — shifts the burden. Most consumers don't know model context sizes. False precision from wrong values is worse than no value.

Grounding: Claude backend parser (`engine/cli/claude/parse.go:299-331`) parses 6 token/cost fields from result events. No `contextWindow` field is extracted. Grep across the `claude` backend found zero matches for "contextWindow" or "context_window".

### ADR-3: Add Convenience `CollectTurn` Only If Accumulator Wiring Proves Verbose

> In the context of **the `TurnSummary` accumulator requiring consumers to wire `summary.Add` into their handler**, facing **potential feedback that two lines of wiring is too much ceremony for the common case**, we decided **to ship the accumulator first (ADR-1) and add `CollectTurn` only if consumer feedback indicates the wiring is a pain point**, and neglected **shipping both simultaneously (increases API surface before validating need)**, to achieve **minimal initial API surface with a clear upgrade path**, accepting **a brief period where consumers write `summary.Add` manually**.

Confidence: **high** — YAGNI until proven otherwise. Adding `CollectTurn` later is backward-compatible and risk-free.

---

## 4.4 Component Specification

### Component 1: `TurnSummary` Type (Root Package)

A struct that accumulates turn-level data from individual messages. Designed for single-goroutine use (not concurrent-safe — callers protect with their own synchronization if needed, matching the typical `RunTurn` handler pattern which is single-goroutine).

Fields the summary should expose:
- **Text**: accumulated assistant text (`MessageText` content, concatenated in order).
- **Thinking**: accumulated thinking text (`MessageThinking` content, concatenated in order).
- **ToolCalls**: slice of `ToolCall` from `MessageToolUse` messages.
- **Usage**: the `*Usage` from the `MessageResult` (turn-level totals). Nil if no result received.
- **StopReason**: from `MessageResult`.
- **Denials**: from `MessageResult`.
- **IsError**: from `MessageResult`.
- **Errors**: slice of error messages (`MessageError` content) encountered during the turn.
- **Result**: boolean indicating whether a `MessageResult` was received.
- **Messages**: the raw `[]Message` for consumers who need full access. Accumulated in order.

The `Add(Message)` method dispatches on `msg.Type`:
- `MessageText`: appends `msg.Content` to Text.
- `MessageThinking`: appends `msg.Content` to Thinking.
- `MessageToolUse`: appends `msg.Tool` to ToolCalls.
- `MessageError`: appends `msg.Content` to Errors.
- `MessageResult`: captures Usage, StopReason, Denials, IsError. Sets Result to true.
- All types: appends to Messages.
- Delta types (`IsDelta`): ignored for accumulation (deltas are partial; complete messages follow).

Design tension: should `Add` return an error? No — accumulation is infallible. A consumer whose handler needs to return errors should call `summary.Add` inside their handler and return errors separately.

### Data Flow

```
Consumer with RunTurn + TurnSummary:
  var summary agentrun.TurnSummary
  err := agentrun.RunTurn(ctx, proc, msg, func(m agentrun.Message) error {
      summary.Add(m)
      // optional: also forward deltas to UI
      return nil
  })
  // summary.Text, summary.Usage, summary.StopReason, etc. are populated

Consumer with channel iteration:
  var summary agentrun.TurnSummary
  for msg := range proc.Output() {
      summary.Add(msg)
      if msg.Type == agentrun.MessageResult {
          break
      }
  }
```

---

## 4.5 Dependency and Blast-Radius Map

### Direct Changes

| File/Component | Change |
|----------------|--------|
| Root package (new file, e.g., `turnsummary.go`) | `TurnSummary` type + `Add` method |
| Root package test file | Tests for `TurnSummary.Add` across all message types |

### Indirect Impact

| Component | Impact |
|-----------|--------|
| `cmd/agentrun-mcp/tools.go` | Can replace `[]agentrun.Message` accumulation + `collectTerminalState` with `TurnSummary`. `runTurnOutput` can derive fields from summary. |
| `examples/interactive/` | Optional adoption — the interactive example streams deltas and may not need a summary. |
| Foundry (external) | Can adopt `TurnSummary` when integrating agentrun. |

### Risk Zones

- **TurnSummary schema growth**: Every new metadata field on `Message` (e.g., future `ReasoningEffort` on result) needs a `TurnSummary` field. This is the primary maintenance cost.
- **Text concatenation semantics**: Multi-text turns (Claude tool-use loops emit multiple `MessageText` per turn) produce concatenated text. Consumers wanting per-block text need `Messages` instead of `Text`. This must be documented.
- **No breaking changes**: `TurnSummary` is purely additive. No existing API is modified.

---

## 4.6 Implementation Instructions (Handoff Contract)

### What to Build

1. **`TurnSummary` type in root package.** A struct with exported fields for text, thinking, tool calls, usage, stop reason, denials, errors, and raw messages. An `Add(Message)` method that dispatches by message type. Single-goroutine use only (no internal mutex).

2. **Tests for `TurnSummary`.** Cover all message types including: normal turn (text + result), thinking turn, tool-use turn, error turn, turn with denials, delta-only messages (should be ignored for accumulation), multi-text turns, and the degenerate case (no result received).

### In Scope

- `TurnSummary` type and `Add` method.
- Comprehensive tests.
- Godoc explaining usage patterns (RunTurn callback, channel iteration).
- Documenting text concatenation behavior for multi-text turns.

### Out of Scope

- **`CollectTurn` convenience function** — deferred per ADR-3. Add later if warranted by feedback.
- **MCP server migration to `TurnSummary`** — consumers adopt at their own pace. Not required for this change.
- **`ContextSizeTokens` for CLI** — deferred per ADR-2. Gated on wire format confirmation (see Questions).
- **Changes to synthesized `MessageContextWindow`** — already working correctly.
- **Changes to `ContextFill` helper** — already working correctly.

### Affected Files and Components

| File | Change Type |
|------|-------------|
| Root package: `turnsummary.go` (new) | New type + method |
| Root package: `turnsummary_test.go` (new) | Tests |

### Acceptance Criteria

1. `TurnSummary.Add` correctly accumulates text from `MessageText`, thinking from `MessageThinking`, tool calls from `MessageToolUse`, errors from `MessageError`, and result metadata from `MessageResult`.
2. Delta message types are ignored for field accumulation (not appended to Text/Thinking). They ARE still appended to `Messages`.
3. `TurnSummary.Usage` is nil before `MessageResult` arrives and non-nil after (when the result carries usage).
4. Multi-text turns concatenate correctly (e.g., two `MessageText` messages → Text contains both, separated by content boundaries).
5. `TurnSummary` is usable with both `RunTurn` callback and direct channel iteration patterns.
6. `make qa` is green.
7. No changes to existing public API.

---

## 4.7 Verification Criteria

- **Normal turn**: `TurnSummary` after a text + result turn has non-empty `Text`, non-nil `Usage`, populated `StopReason`, `Result == true`, and 2 entries in `Messages`.
- **Thinking turn**: `Thinking` is populated from `MessageThinking`. `Text` is populated from `MessageText`. Both coexist.
- **Tool-use turn**: `ToolCalls` contains entries with correct `Name` and `Input`. Multiple tool calls accumulate.
- **Error turn**: `Errors` contains error messages. `Result` may be true or false depending on whether the turn completed.
- **Denial turn**: `Denials` is populated from `MessageResult.Denials`.
- **Delta messages**: `Text` does not contain delta fragments. `Messages` does contain delta messages (for consumers who need the full stream).
- **No result**: `Result == false`, `Usage == nil`, `StopReason` is empty.
- **IsError result**: `IsError == true` when result carries `is_error: true`.
- **Idempotency**: Calling `Add` with the same message twice does not corrupt state (it double-adds, which is the caller's bug, but it shouldn't panic or produce inconsistent types).

---

## 4.8 Assumptions

**A1: Claude CLI `contextWindow` wire field.**
The issue references `modelUsage.<model>.contextWindow` on result events. Not confirmed in the parser or any captured wire samples. This is the blocker for percentage-based fuel gauges on CLI.
*Invalidated if:* The field is confirmed to exist → implement B1 (wire capture) as an amendment.
*Also invalidated if:* Confirmed the field does NOT exist in any Claude CLI event → B4 (accept limitation) becomes the permanent position. Consider B3 (consumer option) as a fallback for power users.

**A2: TurnSummary field set is sufficient for MCP server and Foundry.**
The proposed fields (Text, Thinking, ToolCalls, Usage, StopReason, Denials, IsError, Errors, Result, Messages) cover what `collectTerminalState` and `doRunTurn` derive today. Foundry's needs are assumed to be similar.
*Invalidated if:* Foundry needs fields not in this set (e.g., per-tool-call latency, intermediate context fill snapshots). In that case, extend `TurnSummary` or Foundry uses `Messages` for custom derivation.

**A3: Single-goroutine `Add` is sufficient.**
`RunTurn`'s handler is called from a single goroutine (`drainOutput`). Channel iteration is also single-goroutine. No known consumer calls a handler from multiple goroutines.
*Invalidated if:* A consumer with parallel message processing needs concurrent `Add`. In that case, add an internal mutex (simple, backward-compatible).

**A4: Text concatenation without separator is acceptable.**
`MessageText` content typically ends with natural boundaries. Concatenating without a separator matches the "stream reassembly" pattern. If this proves wrong, a newline separator can be added.
*Invalidated if:* Consumers report garbled text from multi-text turns. Add `\n` separator.

---

## 4.9 Metadata

`2026-03-12 | design | issue-40-remaining-gaps`

Issue: dmora/agentrun#40 — Unify real-time context fill signal across CLI and ACP backends
Predecessor: `docs/designs/2026-03-09-unify-context-fill-and-turn-abstraction.md`

Status of original ADRs:
- **ADR-1 (SequentialSender)**: Implemented. `SequentialSender` interface in `process.go:68-71`.
- **ADR-2 (Synthesize MessageContextWindow)**: Implemented. `synthesizeContextWindow` in `engine/cli/process.go:387-404`.
- **ADR-3 (ContextFill helper)**: Implemented. `contextfill.go:1-17`.
- **ADR-4 (Defer TurnSummary)**: This document picks up TurnSummary as the next deliverable.

---

## Clarification Questions for Operator Validation

### Critical (blocks design decisions)

**Q1: Does Claude CLI's wire format include a `contextWindow` field on any event type?**
The issue references `modelUsage.<model>.contextWindow` on result events. The parser doesn't extract it, and grep found no evidence of it in the codebase. If this field exists, we can implement percentage-based fuel gauges for CLI (B1). If not, CLI is permanently limited to absolute fill values. **Can you capture a raw Claude CLI result event JSON and check for a `contextWindow` or `context_window` field?**

**Q2: Is the `TurnSummary` scope as described sufficient for Foundry's needs?**
The proposed fields are: Text, Thinking, ToolCalls, Usage, StopReason, Denials, IsError, Errors, Result, Messages. Does Foundry need anything beyond this (e.g., per-tool-call timing, intermediate context fill snapshots, turn duration)?

### Important (affects design nuances)

**Q3: Text concatenation strategy for multi-text turns.**
When Claude does a tool-use loop, it emits multiple `MessageText` messages in one turn. Should `TurnSummary.Text` concatenate them with no separator, a newline separator, or store them as `[]string` (breaking the "single Text field" simplicity)? The issue doesn't specify. Current proposal: concatenate with no separator (matches stream reassembly). Newline separator is an easy alternative.

**Q4: Should `TurnSummary` track context fill peak?**
The MCP server currently doesn't derive peak context fill from messages. But a fuel gauge consumer might want "peak fill this turn" as a summary field. Should `TurnSummary` track the max `ContextUsedTokens` seen across all messages in the turn? This adds one field and one comparison in `Add`.

**Q5: Should `TurnSummary` be in the root package or a new `turn` package?**
Root package keeps it discoverable and co-located with `RunTurn`. A separate package reduces root API surface but adds import friction. Current proposal: root package (matches `ContextFill` placement).

### Nice to Know (informs priority)

**Q6: How many call sites in Foundry currently reimplement turn message iteration?**
This validates assumption A3 (≥2 consumers justify a root-package type). If Foundry only has 1 call site, the urgency is lower.

**Q7: Is TurnSummary higher priority than the remaining ContextSizeTokens gap?**
Both are open. TurnSummary is unblocked; ContextSizeTokens is blocked on Q1. Should TurnSummary proceed immediately, or wait until both gaps can be addressed together?

---

## Appendix: Implementation Sequencing

Since leaks #1, #2, and #4 (partial) are already resolved, the remaining work is:

1. **Phase 1 (unblocked)**: `TurnSummary` type + `Add` method + tests. Root package addition.
2. **Phase 2 (gated on Q1)**: If Claude CLI `contextWindow` confirmed → wire capture in Claude parser + CLI engine caching + `ContextSizeTokens` on synthesized `MessageContextWindow`.
3. **Phase 3 (gated on feedback)**: Optional `CollectTurn` convenience wrapper if consumer feedback warrants it.
4. **Phase 4 (optional)**: MCP server migration to `TurnSummary` (consumer-side cleanup, not a library change).

Phase 1 can proceed immediately. Phase 2 is independently blocked on wire format confirmation.
