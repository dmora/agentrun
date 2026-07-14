//go:build !windows

package cli

import (
	"fmt"
	"testing"

	"github.com/dmora/agentrun"
)

// --- helpers ---

func trackerFor(names ...string) *subagentTracker {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return newSubagentTracker(set)
}

func msgInit(resumeID string) agentrun.Message {
	return agentrun.Message{Type: agentrun.MessageInit, ResumeID: resumeID}
}

// msgSpawn is a parent-level assistant message carrying one or more tool_use
// blocks of the given name (mirrors what the claude parser produces).
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

// msgSnapshot is an authoritative background-task snapshot (already filtered to
// subagents by the parser). No ids => drained (non-nil empty).
func msgSnapshot(ids ...string) agentrun.Message {
	m := agentrun.Message{Type: agentrun.MessageBackgroundTasks, Tools: []*agentrun.ToolCall{}}
	for _, id := range ids {
		m.Tools = append(m.Tools, &agentrun.ToolCall{ID: id})
	}
	return m
}

func msgSubagentDone(parentToolUseID string) agentrun.Message {
	return agentrun.Message{Type: agentrun.MessageTaskResult, ParentToolUseID: parentToolUseID}
}

func msgResult() agentrun.Message { return agentrun.Message{Type: agentrun.MessageResult} }

// runTracker feeds messages through the tracker exactly as enrichMessage does
// (observe, then stamp the two result-bearing types) and returns the stamped
// copies for inspection.
func runTracker(tr *subagentTracker, msgs ...agentrun.Message) []agentrun.Message {
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

// subagentCounts extracts the BackgroundSubagent TaskCounts stamped on a
// message, defaulting to the zero value when untracked (Background nil) —
// mirrors the nil-safety the deleted SubagentStats.Pending gave call sites.
func subagentCounts(m agentrun.Message) agentrun.TaskCounts {
	tc, _ := m.Background.Kind(agentrun.BackgroundSubagent)
	return tc
}

// resultPendings extracts Pending() from every stamped result-bearing message.
func resultPendings(msgs []agentrun.Message) []int {
	var out []int
	for _, m := range msgs {
		if m.Type == agentrun.MessageResult || m.Type == agentrun.MessageTaskResult {
			out = append(out, subagentCounts(m).Pending())
		}
	}
	return out
}

// --- opt-in gate ---

func TestTracker_EmptySet_IsInert(t *testing.T) {
	// Empty WithSubagentTools => nil tracker => Background never stamped.
	tr := newSubagentTracker(nil)
	if tr != nil {
		t.Fatal("empty name set must yield a nil (inert) tracker")
	}
	out := runTracker(tr, msgInit("s1"), msgSpawn("Task", "toolu_1"), msgResult())
	for _, m := range out {
		if m.Background != nil {
			t.Errorf("inert tracker must leave Background nil, got %+v on %s", m.Background, m.Type)
		}
	}
}

func TestTracker_NonSubagentBackend_ToolNamedTask_StaysNilWhenNotOptedIn(t *testing.T) {
	// A backend whose tool happens to be named "Task" must not be tracked
	// unless the caller opted in — the whole point of the empty default.
	tr := newSubagentTracker(nil)
	out := runTracker(tr, msgInit("s1"), msgSpawn("Task", "toolu_x"), msgResult())
	last := out[len(out)-1]
	if last.Background != nil {
		t.Errorf("Background should stay nil for a non-opted-in backend, got %+v", last.Background)
	}
}

// --- fallback (name-based) path ---

func TestTracker_Fallback_ForegroundNetsOut(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	out := runTracker(tr,
		msgInit("s1"),
		msgSpawn("Agent", "toolu_a"), // start
		msgSubagentDone("toolu_a"),   // finish before the result
		msgResult(),
	)
	if got := resultPendings(out); len(got) != 2 || got[1] != 0 {
		t.Errorf("foreground should net to 0 at the result, pendings=%v", got)
	}
}

func TestTracker_Fallback_BackgroundOutstandingAtResult(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	out := runTracker(tr,
		msgInit("s1"),
		msgSpawn("Agent", "toolu_a"), // start, never finishes before result
		msgResult(),
	)
	if got := resultPendings(out); len(got) != 1 || got[0] != 1 {
		t.Errorf("background subagent should be outstanding (1) at the result, pendings=%v", got)
	}
}

func TestTracker_Fallback_ParallelSpawnsCountN(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	// Three subagents spawned in ONE assistant message.
	out := runTracker(tr,
		msgInit("s1"),
		msgSpawn("Agent", "toolu_1", "toolu_2", "toolu_3"),
		msgResult(),
	)
	if got := resultPendings(out); len(got) != 1 || got[0] != 3 {
		t.Errorf("parallel fan-out should count 3, pendings=%v", got)
	}
}

func TestTracker_Fallback_NestedSpawnsExcluded(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	// A subagent's own nested spawn (ParentToolUseID set) is not the parent's
	// outstanding work.
	out := runTracker(tr,
		msgInit("s1"),
		msgSpawn("Agent", "toolu_parent"), // +1
		msgSidechainSpawn("toolu_parent", "Agent", "toolu_nested"), // excluded
		msgResult(),
	)
	if got := resultPendings(out); len(got) != 1 || got[0] != 1 {
		t.Errorf("nested spawn must be excluded (want 1), pendings=%v", got)
	}
}

func TestTracker_Fallback_UnknownToolNotCounted(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	out := runTracker(tr,
		msgInit("s1"),
		msgSpawn("Read", "toolu_read"), // not a subagent tool
		msgResult(),
	)
	if got := resultPendings(out); len(got) != 1 || got[0] != 0 {
		t.Errorf("non-subagent tool must not count, pendings=%v", got)
	}
}

// --- native (authoritative snapshot) path: the revive fix ---

func TestTracker_Native_ReviveTrackedAcrossAutoResume(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	// Mirrors run3: an Agent spawn, then the CLI's authoritative snapshots.
	// The subagent completes (snapshot drains), the parent auto-resumes and
	// REVIVES it (a fresh snapshot re-adds it) BEFORE the auto-resume result —
	// which name-based counting cannot see (the revive is a SendMessage).
	out := runTracker(tr,
		msgInit("s1"),
		msgSpawn("Agent", "toolu_spawn"), // provisional name-based +1
		msgSnapshot("a6b3f55a"),          // latch native, authoritative {a6b3f55a}
		msgResult(),                      // result#1: outstanding
		msgSnapshot(),                    // drained (premature quiesce)
		msgSubagentDone("toolu_spawn"),   // completion notification
		msgSnapshot("a6b3f55a"),          // REVIVE
		msgResult(),                      // result#2: MUST still be outstanding
		msgSnapshot(),                    // drained (work really done)
		msgSubagentDone("toolu_revive"),  // completion notification
		msgResult(),                      // result#3: quiescent
	)
	got := resultPendings(out)
	// result#1=1, subagent-done#1=0, result#2=1 (the fix), subagent-done#2=0, result#3=0.
	want := []int{1, 0, 1, 0, 0}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("pendings=%v, want %v (result#2 must be 1 — revive tracked)", got, want)
	}
	// The load-bearing assertion, called out explicitly: the auto-resume
	// result (index 2 among result-bearing messages) is NOT falsely quiescent.
	if got[2] != 1 {
		t.Fatalf("auto-resume result reported Pending=%d, want 1 — this is the #153 premature-finalize bug", got[2])
	}
}

func TestTracker_Native_SupersedesNameBasedCounts(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	// Two name-based spawns are provisionally counted, then the first
	// authoritative snapshot (with a DIFFERENT id space — task ids) replaces
	// them wholesale. Started/Finished reset so counts reflect only truth.
	out := runTracker(tr,
		msgInit("s1"),
		msgSpawn("Agent", "toolu_1"),
		msgSpawn("Agent", "toolu_2"),
		msgSnapshot("task_a"), // authoritative: exactly one subagent
		msgResult(),
	)
	last := subagentCounts(out[len(out)-1])
	if last.Pending() != 1 {
		t.Errorf("Pending() = %d, want 1 (snapshot authoritative)", last.Pending())
	}
	if last.Started != 1 || last.Finished != 0 {
		t.Errorf("Started/Finished = %d/%d, want 1/0 (provisional counts discarded)",
			last.Started, last.Finished)
	}
}

func TestTracker_Native_SubagentResultDoesNotDoubleDecrement(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	// In native mode the snapshot already removed the id; the completion
	// notification must be stamp-only, not a second decrement into negatives.
	out := runTracker(tr,
		msgInit("s1"),
		msgSnapshot("task_a"),
		msgSnapshot(),             // drained: Finished=1
		msgSubagentDone("task_a"), // must NOT decrement again
		msgResult(),
	)
	last := subagentCounts(out[len(out)-1])
	if last.Finished != 1 {
		t.Errorf("Finished = %d, want 1 (no double-decrement)", last.Finished)
	}
	if last.Pending() != 0 {
		t.Errorf("Pending() = %d, want 0", last.Pending())
	}
}

// --- stamps present on both result-bearing types ---

func TestTracker_StampsOnResultAndSubagentResult(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	out := runTracker(tr,
		msgInit("s1"),
		msgSnapshot("task_a"),
		msgSubagentDone("task_a"), // MessageTaskResult — must be stamped
		msgResult(),               // MessageResult — must be stamped
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

// --- reset semantics ---

func TestTracker_ResetOnNewSessionInitOnly(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	// Same-session re-init (auto-resume) is idempotent — pending survives.
	out := runTracker(tr,
		msgInit("s1"),
		msgSnapshot("task_a"),
		msgInit("s1"), // SAME session — must NOT reset
		msgResult(),
	)
	if p := subagentCounts(out[len(out)-1]).Pending(); p != 1 {
		t.Errorf("same-session re-init must be idempotent, Pending=%d want 1", p)
	}

	// A genuinely new session id resets the tracker.
	tr2 := trackerFor("Task", "Agent")
	out2 := runTracker(tr2,
		msgInit("s1"),
		msgSnapshot("task_a"),
		msgInit("s2"), // NEW session — resets
		msgResult(),
	)
	if p := subagentCounts(out2[len(out2)-1]).Pending(); p != 0 {
		t.Errorf("new-session init must reset, Pending=%d want 0", p)
	}
}

func TestTracker_NeverResetsOnResult(t *testing.T) {
	tr := trackerFor("Task", "Agent")
	// Background work must survive turn boundaries (MessageResult).
	out := runTracker(tr,
		msgInit("s1"),
		msgSnapshot("task_a"),
		msgResult(), // turn boundary — must NOT clear the pending set
		msgResult(),
	)
	for _, m := range out {
		if m.Type == agentrun.MessageResult && subagentCounts(m).Pending() != 1 {
			t.Errorf("pending must survive the result boundary, Pending=%d want 1", subagentCounts(m).Pending())
		}
	}
}

// --- memory bound ---

func TestTracker_PendingSetCapped(t *testing.T) {
	tr := trackerFor("Agent")
	msgs := []agentrun.Message{msgInit("s1")}
	for i := 0; i < maxPendingSubagents+50; i++ {
		msgs = append(msgs, msgSpawn("Agent", fmt.Sprintf("toolu_%d", i)))
	}
	msgs = append(msgs, msgResult())
	out := runTracker(tr, msgs...)
	last := subagentCounts(out[len(out)-1])
	if last.Pending() != maxPendingSubagents {
		t.Errorf("Pending() = %d, want cap %d", last.Pending(), maxPendingSubagents)
	}
	if len(last.PendingIDs) > maxPendingIDsStamp {
		t.Errorf("PendingIDs len = %d, want <= %d", len(last.PendingIDs), maxPendingIDsStamp)
	}
}
