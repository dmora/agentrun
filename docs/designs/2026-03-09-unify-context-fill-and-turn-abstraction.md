# Design: Unify Real-Time Context Fill Signal and Eliminate Turn Abstraction Leaks

## 4.1 Context

### Problem Statement

Callers of agentrun building real-time context fill indicators (fuel gauges) must handle backend-specific differences that the library should abstract. A review of the MCP diagnostic server — the first non-trivial consumer — exposed four abstraction leaks that force every consumer to manage engine-level complexity:

1. **`spawnPerTurn` branching**: duplicated in every consumer (MCP server, interactive example, Foundry)
2. **`makeEngine` returns a boolean**: consumers must track `spawnPerTurn` externally
3. **No unified turn summary**: consumers reimplement message-iteration logic
4. **Context fill requires filtering on 3+ message types**: CLI piggybacks on `MessageText`/`MessageThinking`; ACP emits dedicated `MessageContextWindow`

### Current Architecture State

**Known** (from codebase):

- `Process` interface (`process.go:18-51`) defines `Output()`, `Send()`, `Stop()`, `Wait()`, `Err()` — no metadata about send semantics.
- `RunTurn` (`runturn.go:21-28`) always calls `proc.Send(ctx, message)` in a goroutine and drains `Output()` concurrently. Works for ACP (blocking Send + concurrent Output) and CLI streaming (stdin pipe), but for spawn-per-turn backends, `Send()` spawns a new subprocess and `Output()` must be called fresh after Send.
- CLI engine's `process.Send()` (`engine/cli/process.go:122-159`) has three internal paths: stdin pipe (Streamer), `replaceSubprocess` (Resumer while running), and `resumeAfterCleanExit` (Resumer after clean exit). All paths work, but the Output channel is replaced on resume (`process.go:618-620`), meaning `RunTurn`'s concurrent drain starts on the wrong channel.
- MCP server's `makeEngine()` (`cmd/agentrun-mcp/engine.go:25-42`) returns `(Engine, bool, error)` where the bool is `spawnPerTurn`, derived from `cli.Streamer` type assertion.
- `spawnPerTurn` branching appears in **5 consumer call sites** across MCP server and interactive example (`cmd/agentrun-mcp/tools.go:371,479,605`; `examples/interactive/main.go:110,115`).
- `sessionEntry` struct (`cmd/agentrun-mcp/session.go:31`) stores `spawnPerTurn bool` for later dispatch.
- `drainSpawnPerTurn()` (`cmd/agentrun-mcp/tools.go:393-407`) is a simplified drain loop without Send — functionally equivalent to `RunTurn` if `RunTurn` could handle Send-then-drain ordering.
- CLI engine populates `ContextUsedTokens` via `applyContextFill()` (`engine/cli/process.go:468-489`) on mid-turn messages (per-call fill) and `MessageResult` (peak fill). No `ContextSizeTokens` — Claude CLI wire format lacks `contextWindow` in mid-turn events.
- ACP engine emits `MessageContextWindow` via `parseUsageUpdate()` (`engine/acp/update.go:284-328`) with both `ContextSizeTokens` and `ContextUsedTokens`.
- Claude CLI parser `extractTokenUsage()` (`engine/cli/claude/parse.go:299-331`) does NOT capture any `contextWindow` field. No evidence of `contextWindow` in Claude CLI's mid-turn wire format (grep found zero matches across the claude backend).
- `filter` package (`filter/filter.go`) provides `Filter`, `Completed`, `ResultOnly`, `IsDelta` — composable channel middleware. No context-fill-specific filter exists.

**Assumed:**

- Claude CLI's result event may include `modelUsage.<model>.contextWindow` — referenced in the issue but not confirmed in the wire format captured by the parser. **Assumption is that this field exists on result events but not mid-turn events.**
- Foundry (dors-orchestrator) will have the same `spawnPerTurn` branching pattern once integrated. Currently has ~10 lines for `"opencode-acp"` backend in `internal/agent/agentrun.go:buildEngine`.
- Future backends (API-based like ADK) will have persistent-session semantics similar to ACP (Send blocks, Output streams concurrently).

**Missing:**

- Confirmation of Claude CLI's exact `contextWindow` wire field location and availability per event type.
- Whether other CLI backends (Codex, OpenCode) will ever emit per-call usage mid-turn (currently they don't — usage only on result).
- The exact number of Foundry call sites that branch on `spawnPerTurn`.

### Context Manifest

Files examined:
- `process.go` (lines 1-51) — Process interface
- `message.go` (lines 1-338) — Message, Usage, MessageType constants
- `runturn.go` (lines 1-76) — RunTurn, drainOutput implementation
- `engine/cli/engine.go` (lines 52-124) — CLI Engine.Start, capability resolution
- `engine/cli/process.go` (lines 100-519) — CLI process.Send, applyContextFill, scanLines
- `engine/cli/interfaces.go` (lines 1-90) — Spawner, Parser, Resumer, Streamer, InputFormatter, Backend
- `engine/acp/update.go` (lines 270-336) — parseUsageUpdate, MessageContextWindow production
- `engine/acp/process.go` (lines 90-188) — ACP process.Send blocking semantics
- `engine/cli/claude/parse.go` (lines 280-348) — extractTokenUsage, no contextWindow capture
- `cmd/agentrun-mcp/engine.go` (lines 1-43) — makeEngine, spawnPerTurn derivation
- `cmd/agentrun-mcp/tools.go` (lines 330-630) — doRunTurn, doSessionStart, doSessionSend branching
- `cmd/agentrun-mcp/session.go` (line 31) — sessionEntry.spawnPerTurn field
- `examples/interactive/main.go` (lines 50-126) — consumer spawnPerTurn branching
- `filter/filter.go` (lines 1-88) — composable channel middleware

---

## 4.2 Solution Pool

The issue identifies four abstraction leaks. They decompose into two independent design axes:

- **Axis A**: Turn execution abstraction (`spawnPerTurn` branching, `makeEngine` boolean)
- **Axis B**: Context fill unification (multiple message types, missing `ContextSizeTokens`)

A third concern — TurnSummary — is orthogonal and can be addressed independently.

### Axis A: Turn Execution

#### Candidate A1: Make `RunTurn` Handle All Engine Types Internally

`RunTurn` currently calls `Send()` in a goroutine and drains `Output()` concurrently. For spawn-per-turn backends, this fails because `Send()` replaces the subprocess and `Output()` channel. The fix: make `RunTurn` aware of send semantics.

**Approach:** Add an optional interface (e.g., `SendSemantics` or `TurnRunner`) to `Process` that `RunTurn` type-asserts on. When the process signals "send-then-drain" semantics, `RunTurn` calls `Send()` first, then drains `Output()` sequentially. When absent (or "concurrent" semantics), current behavior is preserved.

**Strengths:**
- Consumers never see `spawnPerTurn` — `RunTurn` is the only call site for all backends.
- No boolean return from engine construction. `makeEngine` returns `(Engine, error)`.
- The semantic knowledge stays inside the process, where it belongs.
- Backward-compatible: existing `RunTurn` callers keep working.

**Weaknesses:**
- Adds a new interface to the root package (public API surface cost).
- `RunTurn` becomes more complex internally — two code paths in a critical function.
- Process implementations must opt into the interface correctly; a wrong implementation silently breaks.

**Fit:**
- Matches agentrun's "capabilities via type assertion" pattern (Resumer, Streamer, InputFormatter).
- Root package already has `RunTurn`; enhancing it is natural.

#### Candidate A2: `RunTurn` Calls `Send` Then Drains for All Engine Types

Instead of detecting semantics, always call `Send()` first (blocking), then drain `Output()`. For ACP, `Send()` blocks while streaming updates to `Output()` — deadlock if not drained concurrently.

**Approach:** Not viable. ACP's `Send()` writes to a channel that must be drained concurrently — sequential Send-then-drain deadlocks.

**Eliminated immediately.** ACP's blocking Send + concurrent Output is a hard constraint.

#### Candidate A3: Engine Returns a `TurnRunner` Instead of `Process`

Replace the `Process` interface with a `TurnRunner` that has a single method: `RunTurn(ctx, message, handler) error`. The engine itself encapsulates send semantics.

**Approach:** Each engine returns a `TurnRunner` wrapping its process. The runner knows whether to call Send+drain concurrently (ACP, streaming CLI) or Send-then-drain sequentially (spawn-per-turn).

**Strengths:**
- Eliminates the leak entirely — consumers never learn about send semantics.
- `RunTurn` becomes a method, not a free function — cleaner API.
- No need for capability detection; the runner is fully encapsulated.

**Weaknesses:**
- Fundamentally changes the core `Engine`/`Process` contract — large blast radius.
- Consumers lose direct access to `Output()`, `Stop()`, `Wait()`, `Err()` for advanced use cases (monitoring, custom drain loops, process management).
- Would need to re-expose `Process` methods on `TurnRunner` or wrap them, duplicating the interface.
- Not backward-compatible — breaks every existing consumer.

**Fit:**
- Too high a cost for a greenfield-but-already-shipping library with existing consumers (MCP server, Foundry, examples). The library-first mindset means API stability matters even without a v1 tag.

#### Candidate A4: Process Exposes a Method That Reports Send Semantics

Add a method to `Process` (e.g., `SendMode() SendMode`) that returns whether Send requires sequential or concurrent drain. `RunTurn` inspects this and branches internally.

**Approach:** Concrete method on Process interface rather than optional interface via type assertion.

**Strengths:**
- Explicit, always available — no type assertion needed.
- `RunTurn` can branch cleanly.

**Weaknesses:**
- Adds a required method to `Process` — every implementation must implement it, including mocks and wrappers.
- Breaks the "capabilities via type assertion" pattern used everywhere else.
- API engines (ADK) must return a meaningful value even though they may not have Send semantics.
- A method that returns a constant is a code smell — it's really a type tag, not behavior.

**Fit:**
- Poor fit. Violates the established pattern. Adds ceremony to every Process implementation.

### Self-Critique: Axis A

| Candidate | Strongest counter-argument | Worst case | Hidden cost |
|-----------|---------------------------|------------|-------------|
| **A1 (optional interface)** | Adds a public interface for an implementation detail that consumers shouldn't need to know about. | A third send-mode appears (neither sequential nor concurrent) and the boolean abstraction breaks. | Tests must cover both RunTurn paths; subtle bugs possible if a process incorrectly identifies its semantics. |
| **A3 (TurnRunner)** | Destroys the composable Process model — consumers wrapping Process for logging/metrics lose their integration point. | Every existing consumer breaks; migration cost outweighs benefit. | Dual interface problem — must re-expose Process control methods, or consumers can't manage lifecycle. |
| **A4 (SendMode method)** | Forces every Process mock and wrapper to implement a method that returns a constant — pure ceremony. | Future engine types with dynamic send semantics (e.g., some turns are concurrent, some sequential) need a mutable method, which violates the "constant" assumption. | Every test helper and middleware wrapper must be updated. |

**Preferred: A1** — optional interface on Process, consumed only by `RunTurn`.

### Axis B: Context Fill

#### Candidate B1: CLI Engine Synthesizes `MessageContextWindow`

When the CLI engine's `applyContextFill()` populates `ContextUsedTokens` on a mid-turn message, it **also** emits a separate `MessageContextWindow` message with the same data. Consumers filter on one message type for all backends.

**Approach:** In `scanLines`, after enriching a mid-turn message with context fill, synthesize and emit a `MessageContextWindow` message. `ContextSizeTokens` remains 0 for CLI (unknown). Consumers get a unified signal type but must tolerate `ContextSizeTokens == 0` (absolute-only, no percentage).

**Strengths:**
- Unified message type — consumers write one `case MessageContextWindow:` handler.
- No changes to the root package API.
- CLI's context fill data is already computed; this just re-packages it.

**Weaknesses:**
- Duplicates data: `ContextUsedTokens` appears on both the content message AND the synthesized `MessageContextWindow`. Consumers that process all messages may double-count.
- `ContextSizeTokens == 0` on CLI means consumers still can't compute percentage — the "fuel gauge" use case is only partially solved.
- Adds complexity to `scanLines` — must emit two messages per parsed line in some cases.
- Message ordering becomes subtle: does the synthesized `MessageContextWindow` come before or after the content message?

**Fit:**
- Partial solution. Unifies the type but not the data completeness. Consumers still need backend awareness for percentage computation.

#### Candidate B2: CLI Engine Captures `ContextSizeTokens` from Init/Model Metadata and Synthesizes `MessageContextWindow`

Extends B1: the CLI engine (or Claude backend parser) captures `contextWindow` from model metadata (init event, result event, or a new metadata source) and stores it as engine state. Synthesized `MessageContextWindow` messages carry both `ContextSizeTokens` and `ContextUsedTokens`.

**Approach:** Claude backend parser extracts `contextWindow` from the result event's `modelUsage.<model>.contextWindow` field (if it exists). The CLI engine stores this as `contextSize` state. On subsequent mid-turn messages with usage data, the engine emits `MessageContextWindow` with both fields populated.

**Strengths:**
- Full fuel gauge: consumers get both capacity and fill for all backends.
- Truly unified — one message type, one code path, percentage-capable.

**Weaknesses:**
- **Depends on unconfirmed wire format.** If Claude CLI doesn't expose `contextWindow` on any event, this approach requires hardcoding model context sizes or fetching them externally — both undesirable.
- Context size from the result event arrives **after** mid-turn messages. First-turn mid-turn messages would lack `ContextSizeTokens` until the first result populates it. This creates a "warm-up" gap.
- Couples the CLI engine to Claude-specific wire format details that may change.
- `contextSize` as engine state complicates the stateless-parser model (parser currently returns `(Message, error)` with no side effects).

**Fit:**
- Ideal if the wire data exists. Risky if it doesn't. The "warm-up" gap is a real UX issue for first-turn fuel gauges.

#### Candidate B3: Root Package `ContextFill` Helper That Extracts Fill from Any Message

Instead of synthesizing new messages, provide a helper function in the root package (or filter package) that extracts context fill information from any message type. Consumers call the helper instead of switching on message type.

**Approach:** Add `func ContextFill(msg Message) (used, size int, ok bool)` to the root package. It checks `MessageContextWindow` first, then falls back to mid-turn `ContextUsedTokens` on content messages. Returns `ok == false` when no fill data is present.

**Strengths:**
- No message synthesis — no ordering or duplication concerns.
- Simple consumer API: `if used, size, ok := agentrun.ContextFill(msg); ok { ... }`.
- Works with existing message stream — no engine changes.
- `ok == false` lets consumers skip messages cleanly.

**Weaknesses:**
- Consumers still iterate all messages — the helper just hides the type switch. Not composable with channel-based `filter.Filter()`.
- Doesn't solve the `ContextSizeTokens` gap for CLI — `size` will be 0 for CLI backends.
- A helper function is a band-aid over the real problem (heterogeneous signals). As more message types carry usage data, the helper grows.
- Doesn't enable `filter.Filter(ctx, ch, MessageContextWindow)` for CLI — consumers still need to process multiple types.

**Fit:**
- Quick win, minimal blast radius. But doesn't achieve the "unified signal" goal from the issue.

#### Candidate B4: Synthesize `MessageContextWindow` Only (No Duplication on Content Messages)

CLI engine synthesizes `MessageContextWindow` when context fill data is available, and **removes** `ContextUsedTokens` from the content message. Context fill lives exclusively on `MessageContextWindow` messages, regardless of backend.

**Approach:** `applyContextFill()` no longer sets `ContextUsedTokens` on mid-turn content messages. Instead, a separate `MessageContextWindow` is emitted. `ContextUsedTokens` on `MessageResult` remains (peak fill). `ContextSizeTokens` is 0 for CLI until a source is confirmed.

**Strengths:**
- No duplication — context fill has one canonical location per turn interval.
- Clean consumer pattern: filter on `MessageContextWindow` for real-time fill, read `MessageResult` for final summary.
- Content messages stay focused on content.

**Weaknesses:**
- **Breaking change.** Consumers of CLI mid-turn `ContextUsedTokens` (from PR #39, just merged) lose that data unless they also listen to `MessageContextWindow`. PR #39 was a deliberate design choice; reversing it one PR later signals instability.
- `ContextUsedTokens` on `MessageResult` (peak fill) is a different concept than `MessageContextWindow` (instantaneous fill). Having one on result and one on context_window could confuse consumers.
- If `ContextSizeTokens` is 0, the synthesized message is "used X of unknown" — marginally more useful than the current content-message approach.

**Fit:**
- Cleaner architecture but high churn cost. The recent PR #39 investment would be partially undone.

### Self-Critique: Axis B

| Candidate | Strongest counter-argument | Worst case | Hidden cost |
|-----------|---------------------------|------------|-------------|
| **B1 (synthesize, keep on content)** | Duplicated data on two messages per API call — consumers that sum usage across messages will double-count context fill. | A consumer unknowingly processes both the content message and the synthesized context_window, reporting fill twice. | Message ordering contract needs documentation; output channel buffer pressure increases. |
| **B2 (synthesize with size)** | Depends on unconfirmed wire format data. If contextWindow isn't available mid-turn, the first turn has no capacity until result arrives. | Claude CLI drops the contextWindow field in an update — silent regression. | State management in CLI engine for contextSize; parser loses its stateless property. |
| **B3 (helper function)** | Band-aid — doesn't unify the signal, just hides the switch statement. Not composable with filter package patterns. | Helper grows with every new message type that carries usage data, becoming a maintenance burden. | Consumers must call the helper on every message — can't opt into a filtered channel. |
| **B4 (synthesize, remove from content)** | Breaking change on top of PR #39 (just merged). Signals API instability to early adopters. | Early consumers relying on mid-turn ContextUsedTokens on content messages break silently (field becomes 0). | Documentation/CLAUDE.md updates to reverse PR #39 semantics; test changes across CLI backends. |

**Preferred: B1 + B3 hybrid** — Synthesize `MessageContextWindow` in CLI engine for channel-based filtering (B1), AND provide a `ContextFill` helper for callback-based consumers (B3). Keep `ContextUsedTokens` on content messages (no breaking change). Accept the duplication with clear documentation.

### TurnSummary (Orthogonal)

#### Candidate T1: `TurnSummary` Type + `CollectTurn` Helper

A `TurnSummary` struct in the root package that collects text, thinking, tool calls, usage, stop reason, and denials from a message stream. A `CollectTurn` function or method iterates messages and populates it.

**Approach:** `CollectTurn(ctx, proc, message) (TurnSummary, error)` wraps `RunTurn` and collects messages into a summary. Or: `TurnSummary` is a standalone accumulator that consumers feed messages into via `Add(msg)`.

**Strengths:** Eliminates message-iteration boilerplate. Both MCP server and Foundry benefit.

**Weaknesses:** Locks in a summary schema — if new message types or metadata fields arrive, the summary must grow. Consumers with custom aggregation needs bypass it anyway.

#### Candidate T2: Leave Summarization to Consumers

Status quo. Consumers iterate messages with their own logic.

**Strengths:** Maximum flexibility. No schema to maintain.

**Weaknesses:** Repeated boilerplate across every consumer. Bug risk in reimplemented iteration logic.

**Recommendation:** T1, but deferred. TurnSummary has value but is separable from the context-fill and turn-execution changes. Address it in a follow-up issue to keep blast radius manageable.

---

## 4.3 Decisions

### ADR-1: RunTurn Handles All Engine Types via Optional Interface

> In the context of **consumers branching on `spawnPerTurn` to choose between `RunTurn` and `drainSpawnPerTurn`**, facing **three distinct send-drain orderings across CLI streaming, CLI spawn-per-turn, and ACP engines**, we decided **to add an optional interface on Process (discoverable by RunTurn via type assertion) that declares send-then-drain ordering, allowing RunTurn to handle all engine types internally**, and neglected **replacing Process with TurnRunner (A3, too destructive to existing API) and adding a required SendMode method to Process (A4, violates capability-via-type-assertion pattern)**, to achieve **elimination of `spawnPerTurn` as a consumer-visible concept**, accepting **one new public interface in the root package and increased internal complexity in RunTurn**.

Confidence: **high** — the pattern (optional interface + type assertion) is established in the codebase (Resumer, Streamer, InputFormatter in `engine/cli/interfaces.go`). The two RunTurn code paths (concurrent vs sequential) are well-understood from existing `RunTurn` and `drainSpawnPerTurn` implementations.

Rejected: **A3 (TurnRunner)** — breaks every consumer by replacing the Process interface. Consumers that wrap Process for logging/metrics lose their integration point. Cost far exceeds benefit. **A4 (SendMode method)** — forces every Process implementation and mock to implement a method returning a constant. Violates the type-assertion pattern used consistently across CLI backends.

Grounding: `drainSpawnPerTurn` (`cmd/agentrun-mcp/tools.go:393-407`) and `RunTurn` (`runturn.go:21-28`) contain the two code paths that would be unified. The `spawnPerTurn` boolean appears in 5+ consumer call sites (`tools.go:371,479,605`; `interactive/main.go:110,115`).

### ADR-2: CLI Engine Synthesizes `MessageContextWindow` for Unified Context Fill

> In the context of **CLI backends piggybacking context fill on content messages while ACP emits dedicated `MessageContextWindow`**, facing **consumers needing different filter/switch logic per backend to monitor context fill**, we decided **to have the CLI engine synthesize `MessageContextWindow` messages whenever `applyContextFill` populates `ContextUsedTokens`, while preserving `ContextUsedTokens` on content messages for backward compatibility**, and neglected **removing `ContextUsedTokens` from content messages (B4, breaking change after PR #39) and helper-function-only approach (B3, not composable with filter package)**, to achieve **a unified `MessageContextWindow` signal that all backends emit, enabling `filter.Filter(ctx, ch, MessageContextWindow)` for fuel-gauge consumers**, accepting **data duplication (context fill on both content message and synthesized context_window) which must be documented clearly**.

Confidence: **medium** — the synthesis is straightforward mechanically, but the duplication creates a documentation burden and a risk of consumer confusion. The `ContextSizeTokens == 0` gap for CLI means the fuel gauge is absolute-only (no percentage) until a `contextWindow` source is confirmed.

Assumption: **A-B2-1**: Claude CLI's wire format includes `contextWindow` on result events (referenced in the issue). If confirmed, `ContextSizeTokens` can be populated from the first result event onward, enabling percentage-based gauges. If not confirmed, CLI's `MessageContextWindow` carries `ContextUsedTokens` only (absolute fill).

Rejected: **B4 (synthesize, remove from content)** — breaking change one PR after #39. Signals API instability. Early consumers relying on mid-turn `ContextUsedTokens` break. **B3 (helper only)** — doesn't compose with `filter.Filter()` channel patterns; consumers still iterate all message types.

Grounding: `applyContextFill` (`engine/cli/process.go:468-489`) already computes per-call fill and populates `ContextUsedTokens` on mid-turn messages. The synthesis point is after this enrichment, before the message is sent to the output channel. `parseUsageUpdate` (`engine/acp/update.go:284-328`) is the ACP equivalent — same message type, same fields.

### ADR-3: Provide `ContextFill` Helper for Callback-Based Consumers

> In the context of **some consumers using `RunTurn` with callback handlers (not channel-based filters)**, facing **callback handlers still needing to switch on message type to extract context fill**, we decided **to add a `ContextFill(msg Message) (used, size int, ok bool)` helper function in the root package**, and neglected **putting it in the filter package (wrong abstraction level — filter operates on channels, this operates on single messages)**, to achieve **a single-call extraction point that works regardless of whether the consumer uses channels or callbacks**, accepting **a minor API surface increase (one exported function)**.

Confidence: **high** — simple function, no state, easy to test. Independent of ADR-2.

Rejected: filter package placement — `filter.ContextFill` would be importing root types and returning root values, but filter's abstraction is channel middleware, not per-message extraction. A per-message helper belongs in root.

### ADR-4: Defer TurnSummary to Follow-Up Issue

> In the context of **consumers reimplementing message iteration to extract turn results**, facing **the desire to address all four leaks in one change**, we decided **to defer TurnSummary to a separate issue**, and neglected **bundling it with context fill and RunTurn changes**, to achieve **manageable blast radius per change and independent review/testing**, accepting **continued iteration boilerplate in consumers until the follow-up lands**.

Confidence: **high** — TurnSummary is orthogonal. Bundling it increases review surface and merge risk.

---

## 4.4 Component Specification

### Component 1: Sequential Send Interface (Root Package)

A new optional interface in the root package that `Process` implementations may satisfy. The interface signals that `Send()` must complete before `Output()` is drained for the new turn's messages. The interface name should convey "send completes before drain begins" semantics without exposing implementation details like "spawn" or "subprocess."

The interface has no methods beyond a marker — or alternatively, a single method that `RunTurn` calls instead of the default concurrent pattern. The preferred approach is a marker interface (no methods) because the actual send/drain logic is already implemented by `Process.Send()` and `Process.Output()` — `RunTurn` just needs to know the ordering.

If a process satisfies this interface, `RunTurn` calls `Send()` first (blocking), checks for errors, then drains `Output()` until `MessageResult` or channel close. If a process does not satisfy this interface, `RunTurn` uses the current concurrent pattern (Send in goroutine, drain in main goroutine).

### Component 2: Enhanced `RunTurn` (Root Package)

`RunTurn` gains an internal type assertion on the process. Two paths:

**Path 1 (default, concurrent):** Current behavior. Send in goroutine, drain in calling goroutine. Used by ACP and CLI streaming backends.

**Path 2 (sequential):** Send first (blocking, with context), then drain Output() until MessageResult or channel close. Used by spawn-per-turn backends. Error from Send short-circuits — no drain attempted.

The handler callback contract is unchanged: called for each message including MessageResult. Error from handler stops the drain. Context cancellation stops both paths.

The `drainOutput` internal function can be reused for path 2 by passing a nil `sendCh` (Send already completed).

### Component 3: Synthesized `MessageContextWindow` in CLI Engine

After `applyContextFill()` enriches a mid-turn message with `ContextUsedTokens > 0`, the CLI engine emits an additional `MessageContextWindow` message to the output channel. This happens in `scanLines` after the enriched content message is emitted.

The synthesized message carries:
- `Type: MessageContextWindow`
- `Usage.ContextUsedTokens`: same value as the content message's `ContextUsedTokens`
- `Usage.ContextSizeTokens`: 0 initially (CLI lacks capacity data), populated from cached model metadata if ADR-2's assumption A-B2-1 is confirmed
- `Timestamp`: same as the triggering content message

The synthesized message is emitted **after** the content message (content-first ordering). This matches ACP's behavior where `usage_update` notifications arrive between content updates.

No synthesis on `MessageResult` — result already carries peak `ContextUsedTokens` via `applyContextFill`, and `MessageContextWindow` is a mid-turn signal.

### Component 4: `ContextFill` Helper (Root Package)

A pure function that extracts context fill information from any `Message`. Logic:

1. If `msg.Usage` is nil, return `(0, 0, false)`.
2. If `msg.Usage.ContextUsedTokens > 0` or `msg.Usage.ContextSizeTokens > 0`, return `(used, size, true)`.
3. Otherwise return `(0, 0, false)`.

This works for both `MessageContextWindow` (ACP and synthesized CLI) and mid-turn content messages with `ContextUsedTokens`. The `ok` return value lets callers skip messages without context fill data.

### Data Flow

```
CLI Engine:
  Parser → ParseLine → Message (content)
  Engine → applyContextFill → enriches ContextUsedTokens on content message
  Engine → emit content message to Output channel
  Engine → synthesize MessageContextWindow → emit to Output channel
  [consumer receives both; filters on MessageContextWindow for fuel gauge]

ACP Engine:
  Protocol → usage_update notification
  Engine → parseUsageUpdate → MessageContextWindow
  Engine → emit to Output channel
  [consumer receives MessageContextWindow; same filter as CLI]

Consumer (channel-based):
  filter.Filter(ctx, proc.Output(), agentrun.MessageContextWindow)
  → receives unified context fill signal from any backend

Consumer (callback-based):
  handler := func(msg Message) error {
      if used, size, ok := agentrun.ContextFill(msg); ok {
          updateFuelGauge(used, size)
      }
      // ... handle other message types
  }
  RunTurn(ctx, proc, prompt, handler)
```

---

## 4.5 Dependency and Blast-Radius Map

### Direct Changes

| File/Component | Change |
|----------------|--------|
| Root package (`agentrun`) | New interface (sequential send marker), `ContextFill` helper function |
| `runturn.go` | Type assertion + sequential path in `RunTurn` |
| `engine/cli/process.go` | CLI process satisfies sequential-send interface (spawn-per-turn backends); synthesize `MessageContextWindow` in scanLines after `applyContextFill` |
| `engine/cli/engine.go` | Possibly: detect spawn-per-turn at Start time and store on process struct for interface satisfaction |

### Indirect Impact

| Component | Impact |
|-----------|--------|
| `cmd/agentrun-mcp/` | Can remove `spawnPerTurn` boolean, `drainSpawnPerTurn()`, `sessionEntry.spawnPerTurn`. `makeEngine` returns `(Engine, error)`. All call sites use `RunTurn` uniformly. |
| `examples/interactive/` | Can remove `spawnPerTurn` branching — single `RunTurn` call for all backends. |
| `filter/` | No changes needed. Consumers use existing `filter.Filter(ctx, ch, MessageContextWindow)`. |
| `engine/acp/` | No changes — already emits `MessageContextWindow` natively. |
| `engine/cli/claude/`, `codex/`, `opencode/` | No parser changes. Synthesis happens at engine level, not parser level. |
| `enginetest/clitest/` | May need new compliance test for `MessageContextWindow` synthesis. |

### Risk Zones

- **`scanLines` in `engine/cli/process.go`**: This is the hottest path in the CLI engine — every stdout line flows through it. Adding message synthesis increases complexity. Must not block the output channel (check buffer capacity before synthesis, or always emit synchronously).
- **Output channel buffer pressure**: Synthesizing an extra message per mid-turn usage event doubles the context-fill message volume on the output channel. With `OutputBuffer` default of 64, this should be fine, but high-frequency usage events could cause back-pressure.
- **RunTurn sequential path**: The new path must handle context cancellation correctly during the blocking `Send()` call. If `Send` blocks indefinitely and context is cancelled, the function must return `ctx.Err()` promptly — same contract as the concurrent path.

---

## 4.6 Implementation Instructions (Handoff Contract)

### What to Build

1. **Sequential-send interface in root package.** A new exported interface that Process implementations can satisfy to signal "Send must complete before Output is drained." Marker interface (no methods required) or a single-method interface — implementation decides. The interface name should be descriptive (e.g., `SequentialSender`) without leaking subprocess terminology.

2. **RunTurn enhancement.** `RunTurn` type-asserts the process against the new interface. When satisfied, calls `Send(ctx, message)` synchronously, then drains Output using the existing `drainOutput` helper with a nil `sendCh`. When not satisfied, current concurrent behavior is preserved.

3. **CLI process satisfies the interface.** When the CLI engine's process has spawn-per-turn semantics (Resumer without Streamer), the process satisfies the sequential-send interface. The condition is already computed at Start time in `resolveCapabilities`.

4. **Synthesize `MessageContextWindow` in CLI engine.** After `applyContextFill` enriches a mid-turn message with `ContextUsedTokens > 0`, emit an additional `MessageContextWindow` message with the same `ContextUsedTokens` and `ContextSizeTokens == 0` (or cached value if available). Emit after the content message. Do not synthesize on `MessageResult` or `MessageInit`.

5. **`ContextFill` helper function in root package.** `func ContextFill(msg Message) (used, size int, ok bool)`. Returns context fill data from any message type. `ok` is false when no fill data is present.

### In Scope

- The five items above.
- Tests for RunTurn's sequential path (using a mock process satisfying the new interface).
- Tests for MessageContextWindow synthesis in CLI engine.
- Tests for ContextFill helper.
- Updating MCP server and interactive example to remove `spawnPerTurn` branching (proof that the abstraction works).
- Updating `MessageContextWindow` godoc to note it is now emitted by CLI engines as well.

### Out of Scope

- **TurnSummary** — deferred per ADR-4. Create a follow-up issue.
- **`ContextSizeTokens` for CLI** — depends on confirming Claude CLI wire format. If confirmed, a follow-up captures it. The design accommodates this as a future enhancement (cached `contextSize` state in CLI engine).
- **Removing `ContextUsedTokens` from content messages** — explicitly not done. PR #39 semantics preserved for backward compatibility.
- **Changes to backend parsers** — synthesis happens at engine level. Parsers remain stateless.
- **Changes to ACP engine** — already emits `MessageContextWindow` natively.

### Affected Files and Components

| File | Change Type |
|------|-------------|
| Root package: new file or existing file | New interface + ContextFill function |
| `runturn.go` | Modified: sequential path |
| `engine/cli/process.go` | Modified: interface satisfaction + MessageContextWindow synthesis |
| `engine/cli/engine.go` | Possibly modified: pass capability info for interface satisfaction |
| `cmd/agentrun-mcp/engine.go` | Modified: remove spawnPerTurn from makeEngine return |
| `cmd/agentrun-mcp/tools.go` | Modified: remove drainSpawnPerTurn, simplify all call sites |
| `cmd/agentrun-mcp/session.go` | Modified: remove spawnPerTurn from sessionEntry |
| `examples/interactive/main.go` | Modified: remove spawnPerTurn branching |
| Test files for all above | New/modified |

### Acceptance Criteria

1. `RunTurn` works correctly for all three send modes (streaming CLI, spawn-per-turn CLI, ACP) without consumers branching on engine type.
2. `makeEngine` (or equivalent) no longer returns a boolean. Consumers do not need to know or track `spawnPerTurn`.
3. CLI backends that report per-call usage mid-turn emit `MessageContextWindow` messages that can be filtered with `filter.Filter(ctx, ch, MessageContextWindow)`.
4. `ContextFill(msg)` returns correct values for `MessageContextWindow` (both ACP and CLI-synthesized), mid-turn content messages with usage, and returns `ok == false` for messages without context fill data.
5. MCP server's `doRunTurn`, `doSessionStart`, and `doSessionSend` use a single code path (no spawnPerTurn branching).
6. Interactive example's `run()` function uses a single code path for first turn and subsequent turns.
7. All existing tests pass. `make qa` is green.
8. No breaking changes to the root package's existing public API (existing `ContextUsedTokens` on content messages is preserved).

---

## 4.7 Verification Criteria

- **RunTurn sequential correctness**: A test using a mock process with sequential-send semantics verifies that Send completes before Output is drained. Messages emitted by Output after Send are received by the handler. Context cancellation during Send returns `ctx.Err()`.
- **RunTurn concurrent correctness**: Existing RunTurn tests continue passing (regression).
- **MessageContextWindow synthesis**: A CLI engine test verifies that when a backend emits mid-turn messages with usage data, `MessageContextWindow` messages appear in the output channel interleaved after the content messages. The synthesized messages carry the correct `ContextUsedTokens` value.
- **No synthesis on result**: `MessageResult` does not trigger a synthesized `MessageContextWindow`.
- **ContextFill helper**: Returns `(used, size, true)` for MessageContextWindow with non-zero fields, `(used, 0, true)` for CLI content messages with ContextUsedTokens, and `(0, 0, false)` for messages without usage or with zero context fill.
- **MCP server simplification**: The `doSessionSend` function has no `if spawnPerTurn` branch. `sessionEntry` has no `spawnPerTurn` field.
- **Channel-based filtering**: `filter.Filter(ctx, ch, MessageContextWindow)` receives context fill signals from both CLI and ACP backends in an integration-style test.
- **Buffer pressure**: Under sustained mid-turn usage events, the output channel does not deadlock due to synthesized messages (verified by test with small buffer size).

---

## 4.8 Assumptions

**A1: Claude CLI's `contextWindow` wire field.** The issue references `modelUsage.<model>.contextWindow` on result events. This has not been confirmed in the codebase (grep found zero matches). If this field exists, `ContextSizeTokens` can be populated for CLI backends. If not, CLI's `MessageContextWindow` carries absolute fill only.
*Invalidated if:* Claude CLI never exposes context window capacity in any event type. In that case, percentage-based fuel gauges remain ACP-only, and CLI consumers use absolute token counts.

**A2: RunTurn's concurrent pattern works for all non-spawn-per-turn backends.** All future engine types (API-based) will tolerate `Send()` being called in a goroutine while `Output()` is drained concurrently.
*Invalidated if:* A future engine requires Send-then-drain ordering but isn't a spawn-per-turn subprocess (e.g., an API engine with request-then-poll semantics). The sequential-send interface would accommodate this, but the naming might be misleading.

**A3: Output channel buffer is sufficient for synthesized messages.** The default `OutputBuffer` of 64 absorbs the doubled message rate from context fill synthesis without back-pressure.
*Invalidated if:* A backend emits usage data on every streaming token (not just per API call), causing hundreds of synthesized messages per turn. Currently, Claude CLI emits usage per `assistant` event (one per API call, typically 1-5 per turn), so this is safe.

**A4: Spawn-per-turn semantics are detectable at Start time.** The `resolveCapabilities` function in `engine/cli/engine.go` already determines whether a backend is streaming or spawn-per-turn. This determination is stable for the process lifetime.
*Invalidated if:* A backend dynamically switches between streaming and spawn-per-turn modes within a session. No current or planned backend does this.

---

## 4.9 Metadata

`2026-03-09 | design | unify-context-fill-turn-abstraction`

Issue: dmora/agentrun#40 — Unify real-time context fill signal across CLI and ACP backends

Related: dmora/agentrun#39 (ContextUsedTokens on mid-turn messages — predecessor, merged)

---

## Appendix: Implementation Sequencing Suggestion

While implementation ordering is out of scope for this design, the following sequencing minimizes risk:

1. **Phase 1**: Sequential-send interface + RunTurn enhancement (pure addition, no breaking changes)
2. **Phase 2**: CLI process satisfies interface + MCP server/example cleanup (proves the abstraction)
3. **Phase 3**: MessageContextWindow synthesis in CLI engine (independent of Phase 1-2)
4. **Phase 4**: ContextFill helper (independent of all above)

Phases 1-2 and 3-4 can proceed in parallel.
