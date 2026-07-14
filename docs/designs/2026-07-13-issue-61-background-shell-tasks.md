# Design: Issue #61 — Kind-Aware Background Work (shell task quiescence + follow-up turns)

## Context

### Problem Statement

A Claude Code station that backgrounds a long shell command (`run_in_background`)
ends its turn with an *interim* message. agentrun stamps `Subagents{0,0}` →
`Pending()==0` → the consumer concludes "quiesced" and settles the dispatch with
the interim payload. Minutes later the command exits, the CLI **auto-resumes the
same session** and emits a follow-up turn with the real result — but nobody is
draining `Output()`. The verdict is lost. This is the #153 completion-turn glitch
recurring one layer down: v0.8.0 fixed it for *sub-agents*; #61 is the same hole
for *shell* tasks.

The gap is deliberate: ADR-3 of `2026-07-10-issue-154-pending-tasks.md` filters
`background_tasks_changed` to `task_type == "local_agent"` because a station may
leave a **never-terminating** shell task (dev server) running — a linger gating
on a bash-inclusive `Pending()` would hang forever. #61 adds the competing
requirement: **bounded** shell work (test suites, builds) produces follow-up
turns that must not be dropped. Any design must serve both.

**The naive fix is a trap.** `parseBackgroundTasks`' output feeds
`subagentTracker.applySnapshot` as the authoritative pending set. Deleting the
filter puts bash tasks into `SubagentStats.Pending()` and every existing linger
hangs on daemons — regressing the exact hazard the filter prevents. The fix must
be *kind-aware* end to end.

### Phase-0 wire probes (claude CLI 2.1.204, two live runs, 2026-07-13)

Transcripts checked in at `engine/cli/claude/testdata/backgroundtask/`
(`streaming.jsonl`, `stdin-eof.jsonl`). Findings:

- `background_tasks_changed` includes bash tasks with kind and description:
  `{"task_id":"bfg5749vm","task_type":"local_bash","description":"…"}`
  (streaming.jsonl event 8). agentrun parses this to an **empty**
  `MessageBackgroundTasks` — wire-indistinguishable from "no background work".
- The first `result` arrives while the task still runs (event 13 vs 14-16):
  the moment today's stamp lies.
- On completion: drain snapshot `[]` → `task_updated` → `task_notification`
  (`status:"completed"`, carries `task_id` + `tool_use_id` + `summary` +
  `output_file`, **no task_type**) → fresh `init` (SAME session id) → follow-up
  turn → second `result` with `origin:{"kind":"task-notification"}` (events
  14-19). The follow-up is real and spontaneous — no stdin input.
- Stdin-EOF variant (`stdin-eof.jsonl`): the CLI **cancels** pending tasks at
  shutdown (`task_notification` `status:"stopped"`, now recognized as of
  v0.8.1) and exits. **No follow-up turn is possible off a dead subprocess** —
  the feature is only *live* on persistent (Streamer) transports, which is how
  agentrun always drives Claude.
- From the v0.8.0 probes: a revived subagent can appear with **no
  `task_started` line** — snapshots are the only universal signal. `task_started`
  must never be load-bearing.

### Wire assumptions (record; violations must be diagnosable, not silent)

- **A1** — a spawned background task's registration snapshot precedes that
  spawn's tool_result, hence the turn's result (streaming.jsonl 8 < 10 < 13;
  bg-revive.jsonl 3 < 4). Violation degrades to a transient undercount at stamp
  time (fail-unsafe window; property tests bound it).
- **A2** — the drain snapshot precedes the terminal notification (streaming 14
  → 16; bg-revive 15 → 16, 18 → 19; fg 9 → 10). Violation degrades to a
  transient overcount (fail-safe).

## Decisions

- **ADR-1 — Open kind vocabulary in root.** `BackgroundKind string` (StopReason
  pattern: constants + raw pass-through). `BackgroundSubagent = "subagent"`
  (godoc: expected to terminate), `BackgroundShell = "shell"` (godoc: **may
  never terminate — gate with a timeout**). The Claude parser maps
  `local_agent`→subagent, `local_bash`→shell; any other `task_type` passes
  through sanitized (`errfmt.SanitizeCode`) under its own tag — no identity-
  erasing "other" bucket, no root churn per new CLI task type.

- **ADR-2 — Kind-tagged stats list; absence = untracked; NO union method.**
  `Message.Subagents *SubagentStats` is **replaced** (greenfield) by
  `Message.Background *BackgroundStats`:

  ```go
  type TaskCounts struct {
      Kind       BackgroundKind `json:"kind"`
      Started    int            `json:"started"`
      Finished   int            `json:"finished"`
      PendingIDs []string       `json:"pending_ids,omitempty"` // capped 32 per kind
  }
  func (c TaskCounts) Pending() int // value receiver: Started - Finished

  type BackgroundStats struct {
      Kinds []TaskCounts `json:"kinds"` // only kinds this engine tracks appear
  }
  func (s *BackgroundStats) Kind(k BackgroundKind) (TaskCounts, bool) // comma-ok
  ```

  Nil `*BackgroundStats` keeps meaning "backend tracks nothing"; a kind's
  **absence from Kinds** means "this kind is not tracked here" — the ADR-1
  (v0.8.0) nil-vs-zero distinction, recursed per kind. A value `{0,0}` for an
  untrackable kind (Codex shell) is forbidden: it would read "tracked and
  quiescent". **Deliberately no union `Pending()`**: the shortest call on the
  type must not be the one that hangs on a dev server; a union requires an
  explicit loop over `Kinds` — the hazard becomes visible at the call site.

- **ADR-3 — Dedicated snapshot payload; unconditional parse.**
  `MessageBackgroundTasks` carries ALL kinds via a new field
  `Message.Tasks []BackgroundTask{ID, Kind, Description}` (ID/Kind:
  `SanitizeCode`; Description: `Truncate(4096)`), ending the documented
  `Tools` overload. Parsing is **unconditional** (fixes the "empty snapshot
  lie" — a bash-only snapshot is currently indistinguishable from no work);
  *tracking/stamping* stays opt-in. Description rides the snapshot only, never
  the stats stamp (hot-path bloat; consumers wanting heuristics retain the
  last snapshot).

- **ADR-4 — Kind-neutral completion type.** `MessageSubagentResult` →
  `MessageTaskResult`. Terminal `task_notification` carries no `task_type`, so
  a kind-specific *type* is unknowable to the stateless parser; the demote path
  (issue #57) is provably subagent and may stamp `Tasks=[{ID, Kind:subagent}]`.
  The engine never rewrites parser-assigned types from tracker state
  (nondeterministic vocabulary — the same wire line would map to different
  types depending on snapshot arrival); it may enrich *fields* best-effort,
  and Kind stays empty when genuinely unknowable. Counters never react to
  notifications in native mode (see ADR-5) — verified essential: foreground
  agents' notifications reference ids never present in any snapshot
  (fg.jsonl:13 — the miss path is the *common* case, not an edge).

- **ADR-5 — One tracker, per-kind books, snapshots authoritative for all
  kinds.** `pending map[id]BackgroundKind`; per-kind Started/Finished; the
  first-snapshot native latch is unchanged and zeroes all provisional state
  atomically. Kind is **first-observation-wins and immutable** while pending;
  removal is charged to the *stored* kind. **Per-kind caps** (128 pending +
  32 stamped ids per kind) — a shared cap lets accumulated daemons starve
  subagent slots and silently fake quiescence. Name-based fallback stays
  **subagent-only**: backgroundness is a tool *argument* (`run_in_background`),
  not a tool identity — name-matching would count every foreground Bash call.
  Shell is **native-feed-only**. Reset semantics unchanged (v0.8.0 ADR-4:
  different non-empty ResumeID only; auto-resume re-inits the same session and
  must preserve state — confirmed for shell by streaming.jsonl 14-19).

  Invariants (property-tested after every observe):
  - I1 per-kind books: `Started_k − Finished_k == |{id : pending[id]==k}| ≥ 0`
  - I2 totals: `Σ_k (Started_k − Finished_k) == |pending|`
  - I3 counters move only with genuine set mutations (never on lookup miss,
    duplicate, or cap-refused add)
  - I4 kind immutability; I5 every pending id has a sanitized kind;
  - I6 latch atomicity; fallback inserts subagent only
  - I7 native mode: only snapshots mutate the set; notifications are stamp-only
  - I8 reset per v0.8.0 ADR-4; fresh subprocess ⇒ fresh tracker
  - I9 stamps are copies; per-kind id caps; per-kind presence ≠ `{0,0}`
  - I10 feed coverage is declared by the backend, never inferred from a
    snapshot's contents (an empty snapshot is authoritative for covered kinds)

- **ADR-6 — Opt-in surface.** `WithSubagentTools(names...)` unchanged (enables
  the subagent kind + supplies fallback names). New `WithShellTracking()`
  enables non-subagent kinds from the native feed (named for *tracking*, not
  execution). Defaults off; parse-side `Message.Tasks` is unconditional either
  way. Inert on backends without a native feed (documented, not silently zero).

- **ADR-7 — Consumer contract: per-kind wait policy (the fix is incomplete
  without it).** The kind tag is necessary but not sufficient; the linger must
  differentiate:
  - **Subagents (bounded):** keep today's behavior — wait until
    `Kind(BackgroundSubagent).Pending()` drains to zero.
  - **Shell (maybe unbounded):** never gate quiescence on it. Watch with a
    **timeout**; if a task exits within the window (drain snapshot → terminal
    `MessageTaskResult` → auto-resume follow-up turn), drain and relay that
    follow-up turn. `make test` finishing → the verdict is captured; a dev
    server → the timeout lapses and the consumer finishes cleanly. No hang
    either way.
  - **The named subtlety:** within "shell", bounded-vs-unbounded is unknowable
    to agentrun (a test suite and a daemon are the same wire shape). The
    consumer's timeout is the pragmatic arbiter — long enough for a real
    suite, bounded enough not to wait on a daemon. `Description` on the
    snapshot exists to inform that policy, not to decide it.
  - **Drain to quiescence before the next Send.** Buffered follow-up events
    otherwise misattribute: the queued follow-up `MessageResult` is returned
    as the answer to the *next* prompt (deterministic when the task completes
    before the Send). A result arriving while no Send is outstanding is by
    construction a follow-up turn.
  - **Lifecycle:** shell stamps are actionable only on persistent (Streamer)
    subprocesses. Stop/clean-exit/subprocess-replacement with `Pending>0`
    abandons the work (stdin-eof.jsonl: the CLI cancels tasks with
    `status:"stopped"`); OS processes may be orphaned (no process-group kill).

## Per-Backend Matrix

| Backend | Subagent kind | Shell kind | Follow-up turns |
|---|---|---|---|
| Claude (streaming) | native feed | native feed | yes (auto-resume, same session) |
| Codex / OpenCode / agy | fallback n/a–n/a | not populated | structurally impossible (spawn-per-turn; CLI cancels at EOF) |
| ACP | not populated | not populated | vocabulary ready if terminal state surfaces |

## Blast Radius

Breaking (v0.9.0, one release): `Message.Subagents` → `Message.Background`
(`SubagentStats` deleted, `TaskCounts` supersedes); `MessageSubagentResult` →
`MessageTaskResult`; `MessageBackgroundTasks` payload moves `Tools` → `Tasks`
and is no longer subagent-filtered. Unconditional parse changes Claude output
for all consumers (precedent: v0.8.0 did the same). Downstream (crucible):
`msg.Subagents.Pending()` → `msg.Background.Kind(agentrun.BackgroundSubagent)`
— the migration forces the per-kind policy choice to be explicit at the call
site, which is the point.

## Verification

- Per-kind assertions on existing fixtures — **mandatory**: bg-revive.jsonl
  contains an interleaved bash task (`bkft2fbrh`), so the stamped union
  sequence shifts `[1 0 0 1 0 0]` → `[1 1 0 1 0 0]` while current assertions
  still pass (silent drift). Assert `Sub [1 0 0 1 0 0]` / `Shell [0 1 0 0 0 0]`
  per stamp; fg.jsonl: union zeros, final `Shell{1,1}`.
- New e2e replays over `testdata/backgroundtask/{streaming,stdin-eof}.jsonl`:
  first result stamps `Shell Pending()==1`; drain + `MessageTaskResult` +
  follow-up result stamps quiescent; "stopped" path terminal.
- Tracker scenarios: mixed-kind first latch (bash-only snapshot discards
  provisional subagent adds — A1 window), notification-for-unknown-id moves no
  counters, per-kind cap non-starvation (128 shell + 1 agent), unknown-kind
  counted under own tag, kind-flip counter-neutral, id reuse = new work,
  auto-resume same-session survival, session-switch reset + snapshot self-heal,
  per-kind PendingIDs caps, Stop-safety with undrained buffer (no goroutine
  leak under -race).
- Property/fuzz: random interleavings of init/spawn/snapshot/notification/
  result → I1/I2 hold after every observe; all `Pending() ≥ 0`.

## Deferred

- `origin:{kind:"task-notification"}` follow-up-turn marker as normalized
  vocabulary (turn provenance, not quiescence) — companion issue; the
  operational need is covered by ADR-7's "no Send outstanding ⇒ follow-up"
  inference.
- `task_started` stays unparsed (revives can omit it; `Raw` suffices).
- A union helper (`PendingTotal()`) only if a real consumer demands it, with
  the never-quiesce hazard in its godoc.
