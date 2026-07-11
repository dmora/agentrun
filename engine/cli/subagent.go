//go:build !windows

package cli

import "github.com/dmora/agentrun"

// Bounds on tracker memory and stamped output. maxPendingSubagents caps the
// pending set (a spawn that never reports completion cannot grow it without
// limit); maxPendingIDsStamp caps the id slice copied onto each SubagentStats.
const (
	maxPendingSubagents = 128
	maxPendingIDsStamp  = 32
)

// subagentTracker tracks a parent turn's outstanding subagents across one
// subprocess lifetime (a single scanLines pass). It is loop-local — created
// per subprocess and threaded through enrichMessage like maxCallFill — and it
// stamps SubagentStats onto MessageResult and MessageSubagentResult.
//
// A nil *subagentTracker is the inert state (WithSubagentTools unset): every
// method is a no-op and Message.Subagents is never stamped, preserving
// today's behavior byte-for-byte.
//
// Two counting modes, reconciled by the native latch:
//
//   - Native (authoritative): once a MessageBackgroundTasks snapshot arrives,
//     the pending set is replaced from that snapshot on every subsequent one.
//     This is the load-bearing path for Claude: the CLI reports the true set
//     of in-flight subagents on every transition, including a subagent revived
//     under a fresh tool_use id after an auto-resume — which name-based
//     counting cannot see (the revive is a SendMessage, not a spawn tool).
//     The first snapshot latches native mode and discards provisional
//     name-based counts.
//
//   - Fallback (name-based): before any native snapshot, a parent-level
//     tool_use whose name is in the tracked set adds a pending id, and a
//     MessageSubagentResult removes it. This is the generic path for backends
//     that report completions but not a native task set.
//
// Invariant: Started-Finished == len(pending) == Pending(). Counters only move
// in lockstep with genuine set add/remove (idempotent), so a duplicate spawn
// signal or a completion for an unknown id cannot skew the count.
type subagentTracker struct {
	names        map[string]struct{}
	pending      map[string]struct{}
	started      int
	finished     int
	native       bool
	lastResumeID string
}

// newSubagentTracker returns a tracker for the given tracked tool names, or nil
// (inert) when the set is empty — the opt-in switch. The names map is read-only
// and shared with the engine options; the tracker never mutates it.
func newSubagentTracker(names map[string]struct{}) *subagentTracker {
	if len(names) == 0 {
		return nil
	}
	return &subagentTracker{
		names:   names,
		pending: make(map[string]struct{}),
	}
}

// observe folds a parsed message into the pending set. No-op on a nil tracker.
func (t *subagentTracker) observe(msg *agentrun.Message) {
	if t == nil {
		return
	}
	switch msg.Type {
	case agentrun.MessageInit:
		// Auto-resume re-inits the SAME session (same ResumeID) at quiescence
		// points; those must be idempotent — background tasks are exactly the
		// state that must survive turn/auto-resume boundaries. Reset only on a
		// genuinely new session (a different, non-empty ResumeID). A fresh
		// subprocess already gets a fresh tracker, so the first init is clean.
		if t.lastResumeID != "" && msg.ResumeID != "" && msg.ResumeID != t.lastResumeID {
			t.reset()
		}
		if msg.ResumeID != "" {
			t.lastResumeID = msg.ResumeID
		}
	case agentrun.MessageBackgroundTasks:
		t.applySnapshot(msg.Tools)
	case agentrun.MessageSubagentResult:
		// In native mode the snapshot is authoritative for the set; a
		// completion notification only carries the stamp (removal already
		// happened via the drained snapshot). In fallback mode it decrements.
		if !t.native {
			t.remove(msg.ParentToolUseID)
		}
	default:
		t.countNameBased(msg)
	}
}

// countNameBased is the fallback started-side: a parent-level tool_use whose
// name is in the tracked set adds a pending id. Skipped once native mode has
// latched (the snapshot is authoritative) and for a subagent's own nested
// spawns (ParentToolUseID != ""), which are the subagent's business, not the
// parent's outstanding count.
func (t *subagentTracker) countNameBased(msg *agentrun.Message) {
	if t.native || msg.ParentToolUseID != "" {
		return
	}
	for _, tc := range msg.Tools {
		if tc == nil || tc.ID == "" {
			continue
		}
		if _, ok := t.names[tc.Name]; ok {
			t.add(tc.ID)
		}
	}
}

// stamp attaches a SubagentStats snapshot to a result-bearing message. No-op on
// a nil tracker, which leaves Message.Subagents nil ("backend does not track").
func (t *subagentTracker) stamp(msg *agentrun.Message) {
	if t == nil {
		return
	}
	msg.Subagents = t.stats()
}

// applySnapshot replaces the pending set from an authoritative background-task
// snapshot. The first snapshot latches native mode and discards any provisional
// name-based counts so the counters reflect only authoritative data.
func (t *subagentTracker) applySnapshot(tasks []*agentrun.ToolCall) {
	if !t.native {
		t.native = true
		t.pending = make(map[string]struct{})
		t.started = 0
		t.finished = 0
	}
	next := make(map[string]struct{}, len(tasks))
	for _, tc := range tasks {
		if tc == nil || tc.ID == "" {
			continue
		}
		next[tc.ID] = struct{}{}
	}
	for id := range t.pending {
		if _, ok := next[id]; !ok {
			t.remove(id)
		}
	}
	for id := range next {
		t.add(id)
	}
}

// add inserts a new pending id (bumping Started once), bounded by the cap.
func (t *subagentTracker) add(id string) {
	if id == "" {
		return
	}
	if _, ok := t.pending[id]; ok {
		return
	}
	if len(t.pending) >= maxPendingSubagents {
		return
	}
	t.pending[id] = struct{}{}
	t.started++
}

// remove deletes a pending id (bumping Finished once), idempotently.
func (t *subagentTracker) remove(id string) {
	if id == "" {
		return
	}
	if _, ok := t.pending[id]; !ok {
		return
	}
	delete(t.pending, id)
	t.finished++
}

// reset clears all counting state for a genuinely new session. lastResumeID is
// left to the caller (it identifies the session being switched to).
func (t *subagentTracker) reset() {
	t.pending = make(map[string]struct{})
	t.started = 0
	t.finished = 0
	t.native = false
}

// stats snapshots the current counters and a capped view of the pending ids.
func (t *subagentTracker) stats() *agentrun.SubagentStats {
	s := &agentrun.SubagentStats{Started: t.started, Finished: t.finished}
	if len(t.pending) > 0 {
		ids := make([]string, 0, min(len(t.pending), maxPendingIDsStamp))
		for id := range t.pending {
			if len(ids) >= maxPendingIDsStamp {
				break
			}
			ids = append(ids, id)
		}
		s.PendingIDs = ids
	}
	return s
}
