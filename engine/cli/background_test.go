//go:build !windows

package cli

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/dmora/agentrun"
)

// --- construction helpers ---

// trackerFor builds a tracker with only the subagent kind enabled (the
// pre-#61 shape): WithSubagentTools(names...), WithShellTracking unset.
func trackerFor(names ...string) *backgroundTracker {
	return trackerForShell(false, names...)
}

// trackerForShell builds a tracker with an explicit WithShellTracking
// setting alongside WithSubagentTools(names...). Either switch may be empty
// /false independently — they are independent opt-ins (ADR-6).
func trackerForShell(trackShell bool, names ...string) *backgroundTracker {
	var set map[string]struct{}
	if len(names) > 0 {
		set = make(map[string]struct{}, len(names))
		for _, n := range names {
			set[n] = struct{}{}
		}
	}
	return newBackgroundTracker(set, trackShell)
}

// --- message-building helpers (mirror what enrichMessage feeds the tracker) ---

func msgInit(resumeID string) agentrun.Message {
	return agentrun.Message{Type: agentrun.MessageInit, ResumeID: resumeID}
}

// msgSpawn is a parent-level assistant message carrying one or more tool_use
// blocks of the given name (mirrors what the claude parser produces). This is
// the fallback (name-based) path's input — subagent-only, see
// backgroundTracker.enabled.
func msgSpawn(name string, ids ...string) agentrun.Message {
	m := agentrun.Message{Type: agentrun.MessageText}
	for _, id := range ids {
		tc := &agentrun.ToolCall{Name: name, ID: id}
		m.Tool = tc
		m.Tools = append(m.Tools, tc)
	}
	return m
}

func msgSidechainSpawn(parent, name, id string) agentrun.Message {
	m := msgSpawn(name, id)
	m.ParentToolUseID = parent
	return m
}

// task builds one BackgroundTask entry for msgSnapshot.
func task(id string, kind agentrun.BackgroundKind) agentrun.BackgroundTask {
	return agentrun.BackgroundTask{ID: id, Kind: kind}
}

func sub(id string) agentrun.BackgroundTask       { return task(id, agentrun.BackgroundSubagent) }
func shellTask(id string) agentrun.BackgroundTask { return task(id, agentrun.BackgroundShell) }

// manyTasks builds n distinct BackgroundTask entries of kind, ids prefixed
// for readability in failures.
func manyTasks(kind agentrun.BackgroundKind, n int, prefix string) []agentrun.BackgroundTask {
	ts := make([]agentrun.BackgroundTask, n)
	for i := 0; i < n; i++ {
		ts[i] = task(fmt.Sprintf("%s_%d", prefix, i), kind)
	}
	return ts
}

// msgSnapshot is an authoritative background-task snapshot: post-ADR-3,
// Message.Tasks carries every kind unconditionally (no parser-side
// subagent filter). No tasks => drained (non-nil empty), matching the wire
// shape of a fully-quiesced snapshot.
func msgSnapshot(tasks ...agentrun.BackgroundTask) agentrun.Message {
	m := agentrun.Message{Type: agentrun.MessageBackgroundTasks, Tasks: []agentrun.BackgroundTask{}}
	m.Tasks = append(m.Tasks, tasks...)
	return m
}

func msgTaskDone(parentToolUseID string) agentrun.Message {
	return agentrun.Message{Type: agentrun.MessageTaskResult, ParentToolUseID: parentToolUseID}
}

func msgResult() agentrun.Message { return agentrun.Message{Type: agentrun.MessageResult} }

// runTracker feeds messages through the tracker exactly as enrichMessage does
// (observe, then stamp the two result-bearing types) and returns the stamped
// copies for inspection.
func runTracker(tr *backgroundTracker, msgs ...agentrun.Message) []agentrun.Message {
	out := make([]agentrun.Message, len(msgs))
	for i := range msgs {
		m := msgs[i]
		tr.observe(&m)
		if m.Type == agentrun.MessageResult || m.Type == agentrun.MessageTaskResult {
			tr.stamp(&m)
		}
		out[i] = m
	}
	return out
}

// pendingSeq extracts kind's Pending() from every result-bearing message
// (MessageResult/MessageTaskResult) in msgs, in order. A kind absent from a
// given stamp contributes 0 to the sequence — callers checking the
// tracked-vs-untracked distinction should use Background.Kind's ok directly
// instead (see wantAbsent in trackerScenario).
func pendingSeq(msgs []agentrun.Message, kind agentrun.BackgroundKind) []int {
	var out []int
	for _, m := range msgs {
		if m.Type != agentrun.MessageResult && m.Type != agentrun.MessageTaskResult {
			continue
		}
		tc, _ := m.Background.Kind(kind)
		out = append(out, tc.Pending())
	}
	return out
}

// --- table-driven scenarios ---

// trackerScenario is one tracker timeline: msgs replayed in observe-then-
// stamp order (mirroring enrichMessage), checked against expectations at the
// result-bearing (MessageResult/MessageTaskResult) messages.
type trackerScenario struct {
	name       string
	names      []string // WithSubagentTools names; empty = subagent kind disabled
	trackShell bool     // WithShellTracking

	msgs []agentrun.Message

	// wantPendings maps a kind to its expected Pending() sequence across
	// every result-bearing message, in stamp order. Only listed kinds are
	// checked.
	wantPendings map[agentrun.BackgroundKind][]int

	// wantFinal, when non-nil, checks Started/Finished exactly (ignoring
	// PendingIDs) against the LAST result-bearing message's stamp.
	wantFinal map[agentrun.BackgroundKind]agentrun.TaskCounts

	// wantAbsent lists kinds that must be ABSENT (Background.Kind ok=false)
	// from the last result-bearing message's stamp — the untracked
	// contract, distinct from a tracked-but-quiescent {0,0} entry.
	wantAbsent []agentrun.BackgroundKind
}

func (sc trackerScenario) run(t *testing.T) {
	t.Helper()
	tr := trackerForShell(sc.trackShell, sc.names...)
	out := runTracker(tr, sc.msgs...)

	for kind, want := range sc.wantPendings {
		if got := pendingSeq(out, kind); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("kind %q Pending() sequence = %v, want %v", kind, got, want)
		}
	}

	var last *agentrun.Message
	for i := range out {
		if out[i].Type == agentrun.MessageResult || out[i].Type == agentrun.MessageTaskResult {
			last = &out[i]
		}
	}
	if len(sc.wantFinal) > 0 || len(sc.wantAbsent) > 0 {
		if last == nil {
			t.Fatalf("no result-bearing message observed; cannot check wantFinal/wantAbsent")
		}
	}
	for kind, want := range sc.wantFinal {
		tc, ok := last.Background.Kind(kind)
		if !ok {
			t.Errorf("kind %q missing from final stamp, want %+v", kind, want)
			continue
		}
		if tc.Started != want.Started || tc.Finished != want.Finished {
			t.Errorf("kind %q final Started/Finished = %d/%d, want %d/%d",
				kind, tc.Started, tc.Finished, want.Started, want.Finished)
		}
	}
	for _, kind := range sc.wantAbsent {
		if _, ok := last.Background.Kind(kind); ok {
			t.Errorf("kind %q should be absent (untracked) from the final stamp", kind)
		}
	}
}

func TestBackgroundTracker_Scenarios(t *testing.T) {
	const kindSub, kindShell = agentrun.BackgroundSubagent, agentrun.BackgroundShell

	tests := []trackerScenario{
		// --- fallback (name-based) path, ported from the single-kind tracker ---
		{
			name:  "fallback_foreground_nets_out",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Agent", "toolu_a"), // start
				msgTaskDone("toolu_a"),       // finish before the result
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {0, 0}},
		},
		{
			name:  "fallback_background_outstanding_at_result",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Agent", "toolu_a"), // never finishes before the result
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1}},
		},
		{
			name:  "fallback_parallel_spawns_count_n",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Agent", "toolu_1", "toolu_2", "toolu_3"), // one message, 3 spawns
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {3}},
		},
		{
			name:  "fallback_nested_spawns_excluded",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Agent", "toolu_parent"), // +1
				msgSidechainSpawn("toolu_parent", "Agent", "toolu_nested"), // excluded: a subagent's own nested spawn
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1}},
		},
		{
			name:  "fallback_unknown_tool_not_counted",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Read", "toolu_read"), // not a tracked tool name
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {0}},
		},

		// --- native (authoritative snapshot) path: the #153 revive fix ---
		{
			name:  "native_revive_tracked_across_auto_resume",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Agent", "toolu_spawn"), // provisional name-based +1
				msgSnapshot(sub("a6b3f55a")),     // latch native, authoritative {a6b3f55a}
				msgResult(),                      // result#1: outstanding
				msgSnapshot(),                    // drained (premature quiesce)
				msgTaskDone("toolu_spawn"),       // completion notification
				msgSnapshot(sub("a6b3f55a")),     // REVIVE
				msgResult(),                      // result#2: MUST still be outstanding
				msgSnapshot(),                    // drained (work really done)
				msgTaskDone("toolu_revive"),
				msgResult(), // result#3: quiescent
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1, 0, 1, 0, 0}},
		},
		{
			name:  "native_supersedes_name_based_counts",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Agent", "toolu_1"),
				msgSpawn("Agent", "toolu_2"),
				msgSnapshot(sub("task_a")), // authoritative: exactly one subagent
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1}},
			wantFinal:    map[agentrun.BackgroundKind]agentrun.TaskCounts{kindSub: {Started: 1, Finished: 0}},
		},
		{
			name:  "native_task_result_does_not_double_decrement",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(sub("task_a")),
				msgSnapshot(),         // drained: Finished=1
				msgTaskDone("task_a"), // must NOT decrement again (I7: stamp-only in native mode)
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {0, 0}},
			wantFinal:    map[agentrun.BackgroundKind]agentrun.TaskCounts{kindSub: {Started: 1, Finished: 1}},
		},

		// --- reset semantics (v0.8.0 ADR-4 / I8, unchanged) ---
		{
			name:  "reset_same_session_reinit_is_idempotent",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(sub("task_a")),
				msgInit("s1"), // SAME session — must NOT reset
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1}},
		},
		{
			name:  "reset_new_session_resets",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(sub("task_a")),
				msgInit("s2"), // NEW session — resets
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {0}},
		},
		{
			name:  "never_resets_on_result",
			names: []string{"Task", "Agent"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(sub("task_a")),
				msgResult(), // turn boundary — must NOT clear the pending set
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1, 1}},
		},
		{
			name:       "session_switch_resets_then_snapshot_self_heals",
			names:      []string{"Task"},
			trackShell: false,
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(sub("task_a")),
				msgResult(),                // Pending=1
				msgInit("s2"),              // new session: reset (native -> false again)
				msgResult(),                // Pending=0
				msgSnapshot(sub("task_b")), // self-heal: re-latches native from scratch
				msgResult(),                // Pending=1
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1, 0, 1}},
		},

		// --- multi-kind behavior new in #61 ---
		{
			// The doc's A1-window callout (bg-revive.jsonl real shape): the
			// FIRST authoritative snapshot need not even mention subagents —
			// its mere arrival still latches native mode and wipes every
			// provisional (fallback) count, including subagent's.
			name:       "mixed_kind_first_latch_bash_only_snapshot_discards_provisional_subagent_adds",
			names:      []string{"Agent"},
			trackShell: true,
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Agent", "toolu_a"),     // provisional +1 subagent
				msgSnapshot(shellTask("bash_1")), // FIRST snapshot: bash-only, no subagent entry at all
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {0}, kindShell: {1}},
			wantFinal: map[agentrun.BackgroundKind]agentrun.TaskCounts{
				kindSub:   {Started: 0, Finished: 0},
				kindShell: {Started: 1, Finished: 0},
			},
		},
		{
			name:       "unknown_kind_counted_under_own_tag",
			trackShell: true,
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(task("custom_1", agentrun.BackgroundKind("web_fetch"))),
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{agentrun.BackgroundKind("web_fetch"): {1}},
		},
		{
			// A live id's kind is immutable: a later snapshot re-declaring
			// "x" under a different (here, also-enabled) kind must move no
			// counters and must not reclassify it.
			name:       "kind_flip_counter_neutral",
			names:      []string{"Agent"},
			trackShell: true,
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(shellTask("x")),
				msgResult(),
				msgSnapshot(sub("x")), // re-declare "x" as subagent — must be a no-op
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindShell: {1, 1}, kindSub: {0, 0}},
			wantFinal: map[agentrun.BackgroundKind]agentrun.TaskCounts{
				kindShell: {Started: 1, Finished: 0},
				kindSub:   {Started: 0, Finished: 0},
			},
		},
		{
			// A completed id, later reported again under the SAME string,
			// is new work — the tracker has no memory of finished ids.
			name:       "id_reuse_counts_as_new_work",
			trackShell: true,
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(shellTask("x")),
				msgResult(), // Pending=1
				msgSnapshot(),
				msgResult(),                 // Pending=0 (Finished=1)
				msgSnapshot(shellTask("x")), // same id string reused
				msgResult(),                 // Pending=1 again (Started=2)
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindShell: {1, 0, 1}},
			wantFinal:    map[agentrun.BackgroundKind]agentrun.TaskCounts{kindShell: {Started: 2, Finished: 1}},
		},
		{
			// fg.jsonl's actual common case (ADR-4): a foreground agent's
			// completion notification references an id that no snapshot
			// EVER declared (it started and finished between polls). Native
			// mode was already latched by an unrelated task, so this must
			// be a pure stamp-only no-op — no counter anywhere moves.
			name:  "native_notification_for_id_never_in_any_snapshot_moves_no_counters",
			names: []string{"Task"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSnapshot(sub("known_1")),
				msgTaskDone("fg_agent_never_snapshotted"), // itself result-bearing (stamped), moves nothing
				msgResult(),
			},
			// Both the notification's own stamp and the parent result see the
			// unrelated known_1 still outstanding — untouched.
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1, 1}},
			wantFinal:    map[agentrun.BackgroundKind]agentrun.TaskCounts{kindSub: {Started: 1, Finished: 0}},
		},
		{
			// Fallback-mode counterpart: a notification for an id that was
			// never spawned (typo'd id, race, or a foreign correlation id)
			// must not touch the real pending entry.
			name:  "fallback_notification_unknown_id_moves_no_counters",
			names: []string{"Task"},
			msgs: []agentrun.Message{
				msgInit("s1"),
				msgSpawn("Task", "toolu_a"),
				msgTaskDone("toolu_unknown"), // lookup miss — I3: no counter movement; itself result-bearing
				msgResult(),
			},
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {1, 1}},
			wantFinal:    map[agentrun.BackgroundKind]agentrun.TaskCounts{kindSub: {Started: 1, Finished: 0}},
		},

		// --- memory bound (single kind, ported) ---
		{
			name:  "fallback_pending_capped_per_kind",
			names: []string{"Agent"},
			msgs: func() []agentrun.Message {
				msgs := []agentrun.Message{msgInit("s1")}
				for i := 0; i < maxPendingPerKind+50; i++ {
					msgs = append(msgs, msgSpawn("Agent", fmt.Sprintf("toolu_%d", i)))
				}
				return append(msgs, msgResult())
			}(),
			wantPendings: map[agentrun.BackgroundKind][]int{kindSub: {maxPendingPerKind}},
			wantFinal:    map[agentrun.BackgroundKind]agentrun.TaskCounts{kindSub: {Started: maxPendingPerKind, Finished: 0}},
		},

		// --- applySnapshot ordering at the per-kind cap (review fix) ---
		{
			// A snapshot rotating a kind's ENTIRE live-id set at exactly the
			// per-kind cap must not let the about-to-be-removed old ids block
			// the new ids from being counted. Adding before removing would
			// find the cap still "full" of the old ids and refuse every new
			// one, then remove the old ids anyway — net Pending()==0 while
			// the snapshot just declared maxPendingPerKind live (different)
			// tasks: silent fake quiescence, the exact hazard the per-kind
			// cap exists to prevent.
			name:       "cap_rotation_does_not_fake_quiescence",
			trackShell: true,
			msgs: func() []agentrun.Message {
				msgs := make([]agentrun.Message, 0, 5)
				msgs = append(msgs, msgInit("s1"))
				msgs = append(msgs, msgSnapshot(manyTasks(kindShell, maxPendingPerKind, "shellA")...))
				msgs = append(msgs, msgResult())
				msgs = append(msgs, msgSnapshot(manyTasks(kindShell, maxPendingPerKind, "shellB")...))
				msgs = append(msgs, msgResult())
				return msgs
			}(),
			wantPendings: map[agentrun.BackgroundKind][]int{kindShell: {maxPendingPerKind, maxPendingPerKind}},
			wantFinal: map[agentrun.BackgroundKind]agentrun.TaskCounts{
				kindShell: {Started: 2 * maxPendingPerKind, Finished: maxPendingPerKind},
			},
		},
		{
			// Same hazard, "one out one in" churn shape: dropping one stale
			// id and introducing one new id in the same snapshot, while
			// already at the cap, must keep Pending() AT the cap — not one
			// under it (the new id must not be cap-refused).
			name:       "cap_churn_one_in_one_out_keeps_full_count",
			trackShell: true,
			msgs: func() []agentrun.Message {
				first := manyTasks(kindShell, maxPendingPerKind, "shell") // shell_0..shell_127
				rotated := make([]agentrun.BackgroundTask, maxPendingPerKind)
				for i := range rotated {
					rotated[i] = shellTask(fmt.Sprintf("shell_%d", i+1)) // shell_1..shell_128: drops _0, adds _128
				}
				msgs := make([]agentrun.Message, 0, 5)
				msgs = append(msgs, msgInit("s1"))
				msgs = append(msgs, msgSnapshot(first...))
				msgs = append(msgs, msgResult())
				msgs = append(msgs, msgSnapshot(rotated...))
				msgs = append(msgs, msgResult())
				return msgs
			}(),
			wantPendings: map[agentrun.BackgroundKind][]int{kindShell: {maxPendingPerKind, maxPendingPerKind}},
			wantFinal: map[agentrun.BackgroundKind]agentrun.TaskCounts{
				kindShell: {Started: maxPendingPerKind + 1, Finished: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// --- scenarios that don't reduce to a Pending()-sequence table row ---

func TestBackgroundTracker_NilWhenNoOptIn(t *testing.T) {
	// Neither WithSubagentTools nor WithShellTracking set => nil (fully
	// inert) tracker, and Message.Background is never stamped — even for a
	// backend whose tool happens to be named "Task" (must not be tracked
	// unless the caller opts in; the whole point of the empty default).
	tr := newBackgroundTracker(nil, false)
	if tr != nil {
		t.Fatal("no opt-in must yield a nil (inert) tracker")
	}
	out := runTracker(tr, msgInit("s1"), msgSpawn("Task", "toolu_1"), msgResult())
	for _, m := range out {
		if m.Background != nil {
			t.Errorf("inert tracker must leave Background nil, got %+v on %s", m.Background, m.Type)
		}
	}
}

func TestBackgroundTracker_StampsOnlyResultBearingTypes(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	out := runTracker(tr,
		msgInit("s1"),
		msgSnapshot(sub("task_a")),
		msgTaskDone("task_a"), // MessageTaskResult — must be stamped
		msgResult(),           // MessageResult — must be stamped
	)
	for _, m := range out {
		switch m.Type {
		case agentrun.MessageTaskResult, agentrun.MessageResult:
			if m.Background == nil {
				t.Errorf("%s must carry a non-nil Background stamp", m.Type)
			}
		default:
			if m.Background != nil {
				t.Errorf("%s must NOT be stamped, got %+v", m.Type, m.Background)
			}
		}
	}
}

// TestBackgroundTracker_ShellTrackingOff_TasksStillCarriesShellEntries pins
// the ADR-6 contract end to end: parsing (Message.Tasks) is unconditional
// regardless of tracker configuration; only the STAMP is gated.
func TestBackgroundTracker_ShellTrackingOff_TasksStillCarriesShellEntries(t *testing.T) {
	tr := trackerFor("Task") // subagent only; WithShellTracking NOT set

	snap := msgSnapshot(sub("a1"), shellTask("b1"))
	tr.observe(&snap)
	if len(snap.Tasks) != 2 {
		t.Fatalf("Message.Tasks must be untouched by tracker config, len=%d, want 2", len(snap.Tasks))
	}

	res := msgResult()
	tr.observe(&res)
	tr.stamp(&res)
	if _, ok := res.Background.Kind(agentrun.BackgroundShell); ok {
		t.Error("BackgroundShell must be absent from the stamp when WithShellTracking is off")
	}
	subTC, ok := res.Background.Kind(agentrun.BackgroundSubagent)
	if !ok || subTC.Pending() != 1 {
		t.Errorf("BackgroundSubagent should still be tracked (Pending=1), got ok=%v Pending=%d", ok, subTC.Pending())
	}
}

// containsID reports whether ids contains want.
func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestBackgroundTracker_KindFlipDoesNotReclassifyStoredKind goes one level
// past the table's kind_flip_counter_neutral case, which only checks
// aggregate Pending()/Started/Finished — those stay self-consistent even
// under a bug that silently relabels a live id's stored kind without
// touching any counter (verified: this is exactly the class of defect the
// property test's direct internal-state check catches and this one, driven
// only from public stamps, does not). This test asserts the id itself stays
// listed under its ORIGINAL kind's PendingIDs and never migrates to the
// flip target's.
func TestBackgroundTracker_KindFlipDoesNotReclassifyStoredKind(t *testing.T) {
	tr := trackerForShell(true, "Agent")
	snap1 := msgSnapshot(shellTask("x"))
	tr.observe(&snap1)
	snap2 := msgSnapshot(sub("x")) // re-declare "x" as subagent — must be a no-op
	tr.observe(&snap2)

	res := msgResult()
	tr.observe(&res)
	tr.stamp(&res)

	shellTC, _ := res.Background.Kind(agentrun.BackgroundShell)
	if !containsID(shellTC.PendingIDs, "x") {
		t.Errorf("shell PendingIDs = %v, want to still contain %q (stored kind is immutable)", shellTC.PendingIDs, "x")
	}
	subTC, _ := res.Background.Kind(agentrun.BackgroundSubagent)
	if containsID(subTC.PendingIDs, "x") {
		t.Errorf("subagent PendingIDs = %v, must NOT contain %q (kind must not be reclassified)", subTC.PendingIDs, "x")
	}
}

// TestBackgroundTracker_SeededKindsAppearQuiescentBeforeAnyActivity pins the
// "absence = untracked, presence = tracked (even at {0,0})" contract: a kind
// enabled at construction must appear in the very first stamp even if
// nothing of that kind ever happened during the turn.
func TestBackgroundTracker_SeededKindsAppearQuiescentBeforeAnyActivity(t *testing.T) {
	tr := trackerForShell(true, "Agent")
	out := runTracker(tr, msgInit("s1"), msgResult())
	last := out[len(out)-1]

	subTC, ok := last.Background.Kind(agentrun.BackgroundSubagent)
	if !ok || subTC.Started != 0 || subTC.Finished != 0 || len(subTC.PendingIDs) != 0 {
		t.Errorf("BackgroundSubagent = %+v, ok=%v; want tracked {0,0}", subTC, ok)
	}
	shellTC, ok := last.Background.Kind(agentrun.BackgroundShell)
	if !ok || shellTC.Started != 0 || shellTC.Finished != 0 || len(shellTC.PendingIDs) != 0 {
		t.Errorf("BackgroundShell = %+v, ok=%v; want tracked {0,0}", shellTC, ok)
	}
}

// TestBackgroundTracker_PerKindCaps covers the per-kind cap being
// independent per kind (non-starvation) and the per-kind PendingIDs stamp
// cap, both from a single large mixed-kind snapshot.
func TestBackgroundTracker_PerKindCaps(t *testing.T) {
	t.Run("non_starvation_128_shell_plus_1_agent", func(t *testing.T) {
		tr := trackerForShell(true, "Agent")
		tasks := manyTasks(agentrun.BackgroundShell, maxPendingPerKind, "shell")
		tasks = append(tasks, sub("agent_0"))
		m := msgSnapshot(tasks...)
		tr.observe(&m)

		res := msgResult()
		tr.observe(&res)
		tr.stamp(&res)

		shellTC, _ := res.Background.Kind(agentrun.BackgroundShell)
		if shellTC.Started != maxPendingPerKind || shellTC.Pending() != maxPendingPerKind {
			t.Errorf("shell Started/Pending = %d/%d, want %d/%d",
				shellTC.Started, shellTC.Pending(), maxPendingPerKind, maxPendingPerKind)
		}
		agentTC, ok := res.Background.Kind(agentrun.BackgroundSubagent)
		if !ok || agentTC.Pending() != 1 {
			t.Errorf("agent kind starved by shell's cap: ok=%v Pending=%d, want ok=true Pending=1", ok, agentTC.Pending())
		}
	})

	t.Run("cap_refuses_a_129th_shell_id", func(t *testing.T) {
		tr := trackerForShell(true)
		full := msgSnapshot(manyTasks(agentrun.BackgroundShell, maxPendingPerKind, "shell")...)
		tr.observe(&full)

		overflowTasks := manyTasks(agentrun.BackgroundShell, maxPendingPerKind, "shell")
		overflowTasks = append(overflowTasks, shellTask("shell_overflow"))
		overflow := msgSnapshot(overflowTasks...)
		tr.observe(&overflow)

		res := msgResult()
		tr.observe(&res)
		tr.stamp(&res)
		tc, _ := res.Background.Kind(agentrun.BackgroundShell)
		if tc.Started != maxPendingPerKind {
			t.Errorf("Started = %d, want cap %d (the 129th distinct id must be refused)", tc.Started, maxPendingPerKind)
		}
	})

	t.Run("pending_ids_capped_at_32_per_kind", func(t *testing.T) {
		tr := trackerForShell(true, "Agent")
		tasks := manyTasks(agentrun.BackgroundShell, maxPendingPerKind, "shell")
		tasks = append(tasks, sub("agent_0"))
		m := msgSnapshot(tasks...)
		tr.observe(&m)

		res := msgResult()
		tr.observe(&res)
		tr.stamp(&res)

		shellTC, _ := res.Background.Kind(agentrun.BackgroundShell)
		if len(shellTC.PendingIDs) != maxPendingIDsStampPerKind {
			t.Errorf("shell PendingIDs len = %d, want capped %d", len(shellTC.PendingIDs), maxPendingIDsStampPerKind)
		}
		agentTC, _ := res.Background.Kind(agentrun.BackgroundSubagent)
		if len(agentTC.PendingIDs) != 1 {
			t.Errorf("agent PendingIDs len = %d, want 1 (well under cap)", len(agentTC.PendingIDs))
		}
	})
}

// --- property test: random interleavings must never break I1/I2 ---

// checkBackgroundInvariants asserts I1 (per-kind books) and I2 (totals) hold
// for the tracker's CURRENT internal state, and that every kind's
// Started-Finished is non-negative. Called after every single observe() —
// the property test's core assertion.
func checkBackgroundInvariants(t *testing.T, tr *backgroundTracker) {
	t.Helper()
	if tr == nil {
		return
	}
	total := 0
	for kind, kc := range tr.counts {
		pending := kc.started - kc.finished
		if pending < 0 {
			t.Fatalf("I1 violated: kind %q Pending=%d < 0 (started=%d finished=%d)", kind, pending, kc.started, kc.finished)
		}
		live := 0
		for _, k := range tr.pending {
			if k == kind {
				live++
			}
		}
		if pending != live {
			t.Fatalf("I1 violated: kind %q started-finished=%d != live pending ids=%d", kind, pending, live)
		}
		total += pending
	}
	if total != len(tr.pending) {
		t.Fatalf("I2 violated: sum of per-kind pending=%d != len(pending)=%d", total, len(tr.pending))
	}
}

// bgFuzzIDs/bgFuzzKinds/bgFuzzSessions bound the property test's id/kind/
// session space deliberately small, to force collisions (id reuse, kind
// flips, duplicate adds, cross-session resets) a hand-written test would
// rarely hit by chance.
var (
	bgFuzzIDs      = []string{"id0", "id1", "id2", "id3", "id4", "id5"}
	bgFuzzKinds    = []agentrun.BackgroundKind{agentrun.BackgroundSubagent, agentrun.BackgroundShell, agentrun.BackgroundKind("custom")}
	bgFuzzSessions = []string{"s1", "s2", "s3"}
)

// randomBackgroundMsg builds one uniformly-random message from the
// init/spawn/snapshot/notification/result vocabulary the tracker's observe
// dispatches on.
func randomBackgroundMsg(rng *rand.Rand) agentrun.Message {
	switch rng.IntN(5) {
	case 0: // init — occasionally a session switch
		return msgInit(bgFuzzSessions[rng.IntN(len(bgFuzzSessions))])
	case 1: // fallback spawn (subagent-only, per ADR-5)
		return msgSpawn("Agent", bgFuzzIDs[rng.IntN(len(bgFuzzIDs))])
	case 2: // authoritative snapshot over a random subset of the id/kind pool
		n := rng.IntN(len(bgFuzzIDs) + 1)
		seen := make(map[string]bool, n)
		var tasks []agentrun.BackgroundTask
		for i := 0; i < n; i++ {
			id := bgFuzzIDs[rng.IntN(len(bgFuzzIDs))]
			if seen[id] {
				continue
			}
			seen[id] = true
			tasks = append(tasks, task(id, bgFuzzKinds[rng.IntN(len(bgFuzzKinds))]))
		}
		return msgSnapshot(tasks...)
	case 3: // completion notification — may or may not reference a live id
		return msgTaskDone(bgFuzzIDs[rng.IntN(len(bgFuzzIDs))])
	default: // parent turn boundary
		return msgResult()
	}
}

// TestBackgroundTracker_PropertyInvariants replays many random interleavings
// of init/spawn/snapshot/notification/result — the design doc's mandated
// property test — and checks I1/I2 hold, and every Pending() >= 0, after
// EVERY single observe. Fixed seed: deterministic and reproducible on
// failure, not a flaky live-random test.
func TestBackgroundTracker_PropertyInvariants(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic property-test PRNG, not security-sensitive
	const trials = 200
	const stepsPerTrial = 80

	for trial := 0; trial < trials; trial++ {
		tr := trackerForShell(true, "Agent") // subagent + shell (+ any raw tag) all enabled
		for step := 0; step < stepsPerTrial; step++ {
			msg := randomBackgroundMsg(rng)
			tr.observe(&msg)
			checkBackgroundInvariants(t, tr)

			if msg.Type == agentrun.MessageResult || msg.Type == agentrun.MessageTaskResult {
				tr.stamp(&msg)
				for _, tc := range msg.Background.Kinds {
					if tc.Pending() < 0 {
						t.Fatalf("trial %d step %d: stamped Pending() = %d < 0 for kind %q",
							trial, step, tc.Pending(), tc.Kind)
					}
				}
			}
		}
	}
}
