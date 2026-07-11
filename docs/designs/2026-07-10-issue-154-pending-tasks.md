# Design: Issue #154 (agentrun v0.8.0) — Subagent Pending-Task Tracking

## Context

### Problem Statement

Crucible finalizes a station's dispatch on the station's **first CLI turn-end**. A
Claude Code station that spawns a background subagent (the Agent tool is
background-by-default) ends its turn *before* that subagent finishes → the
consumer settles `verdict=done, artifact=""` while the real work lands minutes
later (the #153 "completion-turn glitch"). Crucible needs a **quiescence signal**
from agentrun: "did this turn end with background subagents still in flight?"

agentrun has no idle/timeout machinery by design (`RunTurn` returns on the first
parent result). This change adds the *signal* only — a nullable `SubagentStats`
stamped on result messages. The wait/linger loop lives entirely in the consumer.

### Phase-0 wire probe (the load-bearing finding)

Nothing in the corpus had ever observed a *background* subagent's completion on
the wire. A probe (three live runs against `claude` 2.1.204, transcripts in
`engine/cli/claude/testdata/subagent/`) established:

- The subagent tool is emitted as **`Agent`** (not `Task`), background by default.
- A background subagent **never emits its own `type:"result"` line** (0 of 7
  results carried `parent_tool_use_id`). Its completion arrives as a separate
  `system` / `task_notification` (terminal `status`, carrying `tool_use_id`).
- The CLI reports the live in-flight set on **`system` / `background_tasks_changed`**
  (each task has `task_id` + `task_type`), draining to `[]` at quiescence.
- After tasks drain the CLI **auto-resumes** the parent (a fresh top-level
  `type:"result"`, same `session_id`, `origin.kind="task-notification"`).

**Decisive subtlety (falsifies name-based counting).** In *both* background runs
the subagent completed once with the artifact still absent (snapshot drained to
`[]`), the parent auto-resumed and **revived** the same subagent, and the real
artifact landed ~2–12 s *after* the auto-resume result. The revive is a
`SendMessage`, not a spawn tool — so name-based tool_use counting quiesces to
`Pending==0` exactly when the auto-resume result arrives → the consumer would
finalize prematurely, re-introducing #153 inside its own fix. Critically, in run3
the revive produced **no `task_started` line at all** — its only pre-result
signal was `background_tasks_changed` re-adding the `local_agent` task. Therefore
`background_tasks_changed` (not `task_started`, not name matching) is the only
signal present across all runs, and it is the authoritative pending set.

## Decisions

- **ADR-1 — Nullable `SubagentStats` on the message, not a bare int.** `nil`
  means "backend does not track subagents" (default, clean degradation); non-nil
  with `Pending()==0` means "tracked and quiescent". A bare `PendingTasks int`
  could not distinguish the two. Stamped on `MessageResult` and
  `MessageSubagentResult`.

- **ADR-2 — Native pending set primary; name-based fallback; empty default.**
  The tracker's authoritative pending set is driven by `background_tasks_changed`
  (filtered to `local_agent`). Name-based counting (a parent-level tool_use whose
  name is in `WithSubagentTools`) is the fallback for backends without a native
  feed, and is superseded the moment the first native snapshot latches. Both key
  a pending-**id set** so a duplicate signal cannot skew the count. `WithSubagentTools`
  **defaults empty** → tracker inert → `Subagents` stays nil: a backend that
  coincidentally names a tool "Task" is not tracked unless the caller opts in.

- **ADR-3 — Two new generic message shapes carry the native events.** The Claude
  parser maps `background_tasks_changed` → **`MessageBackgroundTasks`** (new type;
  `Tools` holds one entry per in-flight subagent, `ID`=task id, non-nil even when
  empty) and terminal `task_notification` → **`MessageSubagentResult`**
  (`ParentToolUseID`=the notification's `tool_use_id`). The generic tracker in
  `engine/cli` stays backend-agnostic — it reasons only over these shapes.
  `local_bash` background tasks are filtered out at parse time (a station may
  leave a server task running, which would never quiesce).

- **ADR-4 — Reset on a genuinely new session only.** Auto-resume re-inits the
  same `session_id`; those must be idempotent (background tasks are exactly the
  state that must survive turn/auto-resume boundaries). The tracker resets only
  when the init's `ResumeID` differs from the last seen one. A subprocess
  replacement already gets a fresh tracker (correct — replacement kills the
  in-process subagents). Never reset on `MessageResult`.

- **ADR-5 — Multi-spawn via `Message.Tools`.** `parseAssistantContent` was
  documented last-one-wins (`Message.Tool` is a single `*ToolCall`), so a message
  spawning several subagents at once exposed only one → the tracker would
  undercount the headline fan-out case. `Message.Tools []*ToolCall` (additive;
  `Tool` stays = last) captures every block; the tracker iterates `Tools`.

## Component Specification

### Root (`message.go`, additive)

- `ToolCall.ID string` — the tool_use block id (Claude `toolu_...`), the spawn↔completion correlation key.
- `Message.Tools []*ToolCall` — every tool_use block; on `MessageBackgroundTasks`, the in-flight task ids.
- `Message.ParentToolUseID string` — the sidechain/completion correlation id.
- `Message.Subagents *SubagentStats` — stamped on result-bearing messages when tracking is on.
- `SubagentStats{Started, Finished int; PendingIDs []string}` + `Pending() int` (nil-safe) — `Started-Finished == len(pending)`, `PendingIDs` capped at 32.
- New `MessageBackgroundTasks` type; extended `MessageSubagentResult` doc (notification source).

### Engine (`engine/cli`)

- `WithSubagentTools(names...)` → `EngineOptions.SubagentTools map[string]struct{}` (empty default; the opt-in gate).
- `subagentTracker` (`subagent.go`): loop-local in `scanLines`, threaded through `enrichMessage` like `maxCallFill`. `observe` folds each message into the pending id set (native snapshot authoritative once latched; name-based add + `MessageSubagentResult` remove otherwise); `stamp` attaches `SubagentStats` to `MessageResult`/`MessageSubagentResult`. Pending set capped at 128. Nil tracker = fully inert.

### Claude parser (`engine/cli/claude/parse.go`)

- `extractToolCall` captures `id`; `parseAssistantContent` appends to `Tools`.
- `ParseLine` stamps `ParentToolUseID` (overridden by `task_notification`).
- `parseBackgroundTasks` (subagent-filtered snapshot) and `parseTaskNotification` (terminal → subagent result).

## Blast Radius

- **Additive root fields** — all `omitempty`; the happy path (no subagents,
  tracking off) serializes byte-identically.
- **Changed claude output (unconditional):** `background_tasks_changed` now parses
  to `MessageBackgroundTasks` and terminal `task_notification` to
  `MessageSubagentResult` (previously both `MessageSystem`). Neither terminates a
  turn; `filter.ResultOnly`/`Completed` are unaffected. Consumers with default
  switches ignore them.
- **No behavior change without opt-in:** the tracker is inert unless
  `WithSubagentTools` is set (crucible passes it on the claude branch only).

## Verification

- `go build ./... && go vet ./... && go test ./... -race` — green.
- Parser unit tests (`subagent_parse_test.go`): id capture, N-way `Tools`,
  `ParentToolUseID` stamping, snapshot subagent-filtering + non-nil empty,
  terminal vs non-terminal notification.
- Tracker unit tests (`subagent_test.go`): opt-in gate / inert nil; fallback
  (fg nets out, bg outstanding, parallel N, nested excluded); native (revive
  tracked across auto-resume, snapshot supersedes provisional, no double-decrement);
  stamps on both types; reset-on-new-session-only; never-on-result; cap.
- **End-to-end** (`subagent_e2e_test.go`, real engine+parser+tracker over the
  probe transcripts via `testdata/mock-replay`): `bg-revive.jsonl` (run3) yields
  the result-bearing `Pending()` sequence `[1 0 0 1 0 0]` — the auto-resume
  result reports **1** (revive tracked; name-based alone would report 0 → the
  #153 premature-finalize bug); `fg.jsonl` (run2) yields all `0` (no linger).

### Known limitation (deferred)

A spawn tool_use that errors *at spawn* under name-based fallback increments
started forever (no matching completion) → `Pending()` stays positive. Rare under
`bypassPermissions`, and the native `background_tasks_changed` path (the real
claude case) never exhibits it because the snapshot is authoritative. Bounded by
the consumer's linger budget; a future version can parse spawn tool_result errors.
