//go:build !windows

package cli

import (
	"sort"

	"github.com/dmora/agentrun"
)

// Bounds on tracker memory and stamped output, applied PER KIND rather than
// shared across kinds: maxPendingPerKind caps each BackgroundKind's pending
// set independently (a spawn that never reports completion cannot grow it
// without limit); maxPendingIDsStampPerKind caps the id slice copied onto
// each kind's stamped TaskCounts. A shared cap would let an accumulation of
// never-completing shell tasks (a dev server) starve the subagent kind's
// slots and silently fake quiescence for it — the reason ADR-5 makes the
// cap per-kind.
const (
	maxPendingPerKind         = 128
	maxPendingIDsStampPerKind = 32
)

// kindCounts is one BackgroundKind's cumulative Started/Finished bookkeeping.
// A zero value means "tracked, no activity observed yet" — see stats, which
// distinguishes that from the kind's outright absence from
// backgroundTracker.counts (untracked).
type kindCounts struct {
	started  int
	finished int
}

// backgroundTracker tracks a parent turn's outstanding background work,
// broken out per BackgroundKind, across one subprocess lifetime (a single
// scanLines pass). It is loop-local — created per subprocess and threaded
// through enrichMessage like maxCallFill — and stamps a per-kind
// agentrun.BackgroundStats onto MessageResult and MessageTaskResult.
//
// A nil *backgroundTracker is the fully inert state (neither
// WithSubagentTools nor WithShellTracking configured): every method is a
// no-op and Message.Background is never stamped, preserving today's
// behavior byte-for-byte. The two options are independent opt-in switches —
// either, both, or neither may be set — see enabled.
//
// Two counting modes, reconciled by the native latch (unchanged from the
// single-kind tracker this replaces):
//
//   - Native (authoritative): once a MessageBackgroundTasks snapshot
//     arrives, the pending set is replaced from Message.Tasks — every kind
//     this tracker is configured to observe, not subagent alone — on every
//     subsequent one. This is the load-bearing path for Claude: the CLI
//     reports the true set of in-flight tasks on every transition,
//     including a task revived under a fresh id after an auto-resume, which
//     name-based counting cannot see (the revive is a SendMessage, not a
//     spawn tool). The first snapshot latches native mode and discards
//     provisional name-based counts.
//
//   - Fallback (name-based): before any native snapshot, a parent-level
//     tool_use whose name is in the tracked set adds a pending
//     BackgroundSubagent id, and a MessageTaskResult removes it. Fallback is
//     subagent-only (see enabled) — backgroundness is a tool argument
//     (run_in_background), not a tool identity, so name-matching a shell
//     tool would count every foreground invocation.
//
// Invariants (the design doc's I1-I10; property-tested in
// background_test.go):
//   - per-kind books: for every kind k, Started_k - Finished_k equals the
//     count of pending ids currently stored under k, and is never negative
//   - totals: the sum of every kind's Started-Finished equals len(pending)
//   - counters move ONLY on a genuine set mutation — never on a
//     notification for an id nothing ever added (the common case:
//     foreground-agent notifications reference ids no snapshot ever
//     declared), a duplicate add, or a cap-refused add
//   - a pending id's kind is first-observation-wins and immutable: a later
//     snapshot re-declaring a different kind for a still-live id is a
//     no-op (counter-neutral), and the id's eventual removal charges the
//     STORED kind, not whatever a later snapshot claimed
type backgroundTracker struct {
	names      map[string]struct{} // WithSubagentTools: enables + fallback-names BackgroundSubagent
	trackShell bool                // WithShellTracking: enables every non-subagent kind

	pending map[string]agentrun.BackgroundKind      // id -> first-observed kind, live entries only
	counts  map[agentrun.BackgroundKind]*kindCounts // per-kind Started/Finished

	native       bool
	lastResumeID string
}

// newBackgroundTracker returns a tracker configured from the two independent
// opt-in switches, or nil (fully inert) when neither is set. names is
// read-only and shared with the engine options; the tracker never mutates
// it.
func newBackgroundTracker(names map[string]struct{}, trackShell bool) *backgroundTracker {
	if len(names) == 0 && !trackShell {
		return nil
	}
	t := &backgroundTracker{names: names, trackShell: trackShell}
	t.resetState()
	return t
}

// enabled reports whether kind is one this tracker folds into its books.
// BackgroundSubagent is gated by WithSubagentTools; every other kind
// (BackgroundShell and any raw pass-through tag alike — ADR-6's
// "non-subagent kinds") is gated by WithShellTracking. A disabled kind's
// snapshot entries are ignored: never added, so they can never move a
// counter, but they remain visible on Message.Tasks for consumers who want
// them regardless of tracking configuration.
func (t *backgroundTracker) enabled(kind agentrun.BackgroundKind) bool {
	if kind == agentrun.BackgroundSubagent {
		return len(t.names) > 0
	}
	return t.trackShell
}

// observe folds a parsed message into the pending set. No-op on a nil tracker.
func (t *backgroundTracker) observe(msg *agentrun.Message) {
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
		t.applySnapshot(msg.Tasks)
	case agentrun.MessageTaskResult:
		// In native mode the snapshot is authoritative for the set; a
		// completion notification only carries the stamp (removal already
		// happened via the drained snapshot — I7). In fallback mode it
		// decrements.
		if !t.native {
			t.remove(msg.ParentToolUseID)
		}
	default:
		t.countNameBased(msg)
	}
}

// countNameBased is the fallback started-side: a parent-level tool_use whose
// name is in the tracked set adds a pending BackgroundSubagent id (fallback
// is subagent-only, see enabled). Skipped once native mode has latched (the
// snapshot is authoritative) and for a subagent's own nested spawns
// (ParentToolUseID != ""), which are the subagent's business, not the
// parent's outstanding count.
func (t *backgroundTracker) countNameBased(msg *agentrun.Message) {
	if t.native || msg.ParentToolUseID != "" {
		return
	}
	for _, tc := range msg.Tools {
		if tc == nil || tc.ID == "" {
			continue
		}
		if _, ok := t.names[tc.Name]; ok {
			t.add(tc.ID, agentrun.BackgroundSubagent)
		}
	}
}

// stamp attaches a BackgroundStats snapshot to a result-bearing message. No-op
// on a nil tracker, which leaves Message.Background nil ("backend tracks
// nothing").
func (t *backgroundTracker) stamp(msg *agentrun.Message) {
	if t == nil {
		return
	}
	msg.Background = t.stats()
}

// applySnapshot replaces the pending set from an authoritative
// MessageBackgroundTasks snapshot (Message.Tasks) — the ADR-3 replace-set,
// now carrying every kind rather than subagent-only. The first snapshot
// latches native mode and discards any provisional name-based counts so the
// counters reflect only authoritative data.
//
// A disabled kind's entries are never added (see enabled) — as if absent
// from the snapshot entirely. An id already pending stays present in the
// diff regardless of what kind THIS snapshot reports for it: a live id's
// kind is immutable (add is a no-op once an id is in the map), so a
// re-declared kind can neither move a counter nor trigger a spurious
// removal.
func (t *backgroundTracker) applySnapshot(tasks []agentrun.BackgroundTask) {
	if !t.native {
		t.native = true
		t.resetState() // I6: latch discards all provisional (fallback) state atomically
	}
	present := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.ID == "" {
			continue
		}
		present[task.ID] = struct{}{}
		if _, ok := t.pending[task.ID]; ok {
			continue // already tracked; stored kind wins (I4), whatever this snapshot says
		}
		if t.enabled(task.Kind) {
			t.add(task.ID, task.Kind)
		}
	}
	for id := range t.pending {
		if _, ok := present[id]; !ok {
			t.remove(id)
		}
	}
}

// add inserts a new pending id under kind (bumping that kind's Started once),
// bounded by the per-kind cap. A no-op when id is already pending — this is
// also how kind immutability holds: the stored kind from the first
// observation is never overwritten by a later, differing observation.
func (t *backgroundTracker) add(id string, kind agentrun.BackgroundKind) {
	if id == "" {
		return
	}
	if _, ok := t.pending[id]; ok {
		return
	}
	kc, ok := t.counts[kind]
	if !ok {
		kc = &kindCounts{}
	}
	if kc.started-kc.finished >= maxPendingPerKind {
		return
	}
	t.pending[id] = kind
	kc.started++
	t.counts[kind] = kc
}

// remove deletes a pending id, charging Finished to its STORED kind (the
// kind recorded when it was added, never whatever a later snapshot might
// claim), idempotently. A lookup miss — the common case for a foreground
// agent's completion notification, which references an id no snapshot ever
// declared — moves no counter.
func (t *backgroundTracker) remove(id string) {
	if id == "" {
		return
	}
	kind, ok := t.pending[id]
	if !ok {
		return
	}
	delete(t.pending, id)
	t.counts[kind].finished++ // set on add; always present for a pending id
}

// reset clears all counting state for a genuinely new session (a different,
// non-empty ResumeID — v0.8.0 ADR-4, unchanged: I8). lastResumeID is left to
// the caller (it identifies the session being switched to).
func (t *backgroundTracker) reset() {
	t.resetState()
	t.native = false
}

// resetState clears the pending set and per-kind counters, re-seeding a
// {0,0} entry for every kind this tracker is CONFIGURED (not merely
// observed) to track. Construction and both reset paths (new session,
// native latch) share this so a consumer sees "tracked but quiescent" the
// moment tracking is enabled, not only after the first real observation.
func (t *backgroundTracker) resetState() {
	t.pending = make(map[string]agentrun.BackgroundKind)
	t.counts = make(map[agentrun.BackgroundKind]*kindCounts)
	if len(t.names) > 0 {
		t.counts[agentrun.BackgroundSubagent] = &kindCounts{}
	}
	if t.trackShell {
		t.counts[agentrun.BackgroundShell] = &kindCounts{}
	}
}

// stats snapshots the current per-kind counters and a capped, per-kind view
// of pending ids into a BackgroundStats (I9: always a fresh copy, never
// aliasing tracker-owned memory). Kinds holds one TaskCounts per kind this
// tracker knows about — the seeded kinds (if enabled) plus any other kind
// actually observed through the native feed — sorted by Kind for
// deterministic output. A kind's absence from Kinds means untracked;
// presence with {0,0} means tracked but quiescent (see
// agentrun.BackgroundStats).
func (t *backgroundTracker) stats() *agentrun.BackgroundStats {
	kinds := make([]agentrun.TaskCounts, 0, len(t.counts))
	for kind, kc := range t.counts {
		tc := agentrun.TaskCounts{Kind: kind, Started: kc.started, Finished: kc.finished}
		if ids := t.pendingIDs(kind); len(ids) > 0 {
			tc.PendingIDs = ids
		}
		kinds = append(kinds, tc)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Kind < kinds[j].Kind })
	return &agentrun.BackgroundStats{Kinds: kinds}
}

// pendingIDs returns a capped (maxPendingIDsStampPerKind) view of kind's
// currently-outstanding ids, or nil when none are pending.
func (t *backgroundTracker) pendingIDs(kind agentrun.BackgroundKind) []string {
	var ids []string
	for id, k := range t.pending {
		if k != kind {
			continue
		}
		if len(ids) >= maxPendingIDsStampPerKind {
			break
		}
		ids = append(ids, id)
	}
	return ids
}
