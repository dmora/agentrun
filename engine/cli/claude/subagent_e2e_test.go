//go:build !windows

package claude_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
)

var (
	replayBuildOnce sync.Once
	replayBinary    string
	errReplayBuild  error
)

func buildReplayBinary() {
	dir, err := os.MkdirTemp("", "mock-replay-*")
	if err != nil {
		errReplayBuild = fmt.Errorf("tmpdir: %w", err)
		return
	}
	replayBinary = filepath.Join(dir, "mock-replay")
	cmd := exec.Command("go", "build", "-o", replayBinary, "./testdata/mock-replay/main.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		errReplayBuild = fmt.Errorf("build mock-replay: %w: %s", err, out)
		os.RemoveAll(dir)
	}
}

// replayFixtureMessages drives the real cli engine + claude parser (and,
// when opts enable it, the background tracker) over a captured transcript at
// testdata/<dir>/<fixture> and returns every message emitted on Output(), in
// order. The mock-replay binary waits for one stdin line (like real Claude in
// stream-json mode), then dumps the fixture verbatim and exits.
func replayFixtureMessages(t *testing.T, dir, fixture string, opts ...cli.EngineOption) []agentrun.Message {
	t.Helper()
	replayBuildOnce.Do(buildReplayBinary)
	if errReplayBuild != nil {
		t.Fatalf("mock-replay build failed: %v", errReplayBuild)
	}
	abs, err := filepath.Abs(filepath.Join("testdata", dir, fixture))
	if err != nil {
		t.Fatalf("abs fixture path: %v", err)
	}

	backend := claude.New(claude.WithBinary(replayBinary))
	engine := cli.NewEngine(backend, opts...)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cwd, _ := os.Getwd()
	proc, err := engine.Start(ctx, agentrun.Session{
		CWD: cwd,
		Env: map[string]string{"MOCK_FIXTURE": abs},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = proc.Stop(context.Background()) }()

	if err := proc.Send(ctx, "go"); err != nil {
		t.Fatalf("send: %v", err)
	}

	var msgs []agentrun.Message
	for {
		select {
		case msg, ok := <-proc.Output():
			if !ok {
				return msgs
			}
			msgs = append(msgs, msg)
		case <-ctx.Done():
			t.Fatalf("timed out draining output; collected %d messages", len(msgs))
		}
	}
}

// resultBearing filters msgs down to the two result-bearing types
// (MessageResult, MessageTaskResult) — the only types a background tracker
// ever stamps.
func resultBearing(msgs []agentrun.Message) []agentrun.Message {
	var out []agentrun.Message
	for _, m := range msgs {
		if m.Type == agentrun.MessageResult || m.Type == agentrun.MessageTaskResult {
			out = append(out, m)
		}
	}
	return out
}

// kindPendingSeq extracts kind's Pending() from every result-bearing message
// in msgs, in order. A kind absent from a given stamp contributes 0 to the
// sequence — callers checking the tracked-vs-untracked distinction should use
// assertKindAbsentThroughout instead (mirrors engine/cli's pendingSeq test
// helper, replayed here over real parser output instead of synthetic
// messages).
func kindPendingSeq(msgs []agentrun.Message, kind agentrun.BackgroundKind) []int {
	results := resultBearing(msgs)
	out := make([]int, 0, len(results))
	for _, m := range results {
		tc, _ := m.Background.Kind(kind)
		out = append(out, tc.Pending())
	}
	return out
}

// assertPendingSeq fails the test unless kind's Pending() sequence across
// every result-bearing message matches want exactly.
func assertPendingSeq(t *testing.T, msgs []agentrun.Message, kind agentrun.BackgroundKind, want []int) {
	t.Helper()
	got := kindPendingSeq(msgs, kind)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("kind %q Pending() sequence = %v, want %v", kind, got, want)
	}
}

// assertKindAbsentThroughout fails if kind is present (Background.Kind
// ok=true) on ANY result-bearing message — the untracked contract, distinct
// from a tracked-but-quiescent {0,0} entry (see agentrun.BackgroundStats).
func assertKindAbsentThroughout(t *testing.T, msgs []agentrun.Message, kind agentrun.BackgroundKind) {
	t.Helper()
	for _, m := range resultBearing(msgs) {
		if tc, ok := m.Background.Kind(kind); ok {
			t.Errorf("%s: kind %q should be absent (untracked) without WithShellTracking, got %+v", m.Type, kind, tc)
		}
	}
}

// assertFinalCounts fails unless the LAST result-bearing message reports
// exactly want's Started/Finished for kind (PendingIDs ignored).
func assertFinalCounts(t *testing.T, msgs []agentrun.Message, kind agentrun.BackgroundKind, want agentrun.TaskCounts) {
	t.Helper()
	results := resultBearing(msgs)
	if len(results) == 0 {
		t.Fatal("no result-bearing message to check final counts against")
	}
	last := results[len(results)-1]
	tc, ok := last.Background.Kind(kind)
	if !ok {
		t.Errorf("kind %q missing from final stamp, want %+v", kind, want)
		return
	}
	if tc.Started != want.Started || tc.Finished != want.Finished {
		t.Errorf("kind %q final Started/Finished = %d/%d, want %d/%d",
			kind, tc.Started, tc.Finished, want.Started, want.Finished)
	}
}

// TestE2E_Subagent_BackgroundRevive is the load-bearing test: over the real
// run3 transcript, the auto-resume result must NOT report quiescence while
// the subagent is revived and still writing — that false "done" is the #153
// bug. bg-revive.jsonl also interleaves an unrelated bash task (bkft2fbrh)
// that starts, finishes, and gets superseded by the revive — the fixture
// issue #61's per-kind separation is verified against (a naive union count
// would misreport quiescence around the bash task's own completion).
func TestE2E_Subagent_BackgroundRevive(t *testing.T) {
	// Traced fixture sequence (both subtests): parent results #1 outstanding,
	// #2 outstanding (revive), #3 quiescent; the interleaved MessageTaskResult
	// completions report the drained count at that instant. Verified by
	// tracing the raw fixture line-by-line (see design doc §Verification);
	// matches the doc's mandated sequence exactly.
	wantSubagent := []int{1, 0, 0, 1, 0, 0}

	t.Run("subagent_only_pre_61_behavior_intact", func(t *testing.T) {
		// No WithShellTracking: today's behavior, byte-for-byte. The
		// interleaved bkft2fbrh bash task must not appear at all — absent,
		// not a tracked {0,0} — since this engine wasn't asked to track it.
		msgs := replayFixtureMessages(t, "subagent", "bg-revive.jsonl",
			cli.WithSubagentTools("Task", "Agent"))
		t.Logf("subagent Pending() sequence: %v", kindPendingSeq(msgs, agentrun.BackgroundSubagent))
		assertPendingSeq(t, msgs, agentrun.BackgroundSubagent, wantSubagent)
		assertKindAbsentThroughout(t, msgs, agentrun.BackgroundShell)
	})

	t.Run("per_kind_with_shell_tracking", func(t *testing.T) {
		// WithShellTracking on: the subagent sequence is UNCHANGED (the two
		// kinds' books are independent — I1/I2) while the interleaved bash
		// task now surfaces under its own kind: started when the bkft2fbrh
		// snapshot first appears (event 7), finished at the drain (event 15)
		// that precedes its "completed" notification (event 16).
		msgs := replayFixtureMessages(t, "subagent", "bg-revive.jsonl",
			cli.WithSubagentTools("Task", "Agent"), cli.WithShellTracking())
		t.Logf("subagent Pending() sequence: %v", kindPendingSeq(msgs, agentrun.BackgroundSubagent))
		t.Logf("shell Pending() sequence: %v", kindPendingSeq(msgs, agentrun.BackgroundShell))
		assertPendingSeq(t, msgs, agentrun.BackgroundSubagent, wantSubagent)
		assertPendingSeq(t, msgs, agentrun.BackgroundShell, []int{0, 1, 0, 0, 0, 0})
	})
}

// TestE2E_Subagent_Foreground confirms a foreground subagent nets out: every
// subagent stamp is quiescent, so no linger is triggered. fg.jsonl also
// backgrounds an unrelated bash sleep (bq1yaticf) that starts and finishes
// BEFORE the subagent's own completion notification arrives — the ADR-4
// "common case" where a foreground agent's notification references an id no
// snapshot ever declared (fg.jsonl:13) and must move no counters.
func TestE2E_Subagent_Foreground(t *testing.T) {
	t.Run("subagent_only_pre_61_behavior_intact", func(t *testing.T) {
		msgs := replayFixtureMessages(t, "subagent", "fg.jsonl",
			cli.WithSubagentTools("Task", "Agent"))
		got := kindPendingSeq(msgs, agentrun.BackgroundSubagent)
		t.Logf("subagent Pending() sequence: %v", got)
		if len(got) == 0 {
			t.Fatal("expected at least one result-bearing message")
		}
		for i, p := range got {
			if p != 0 {
				t.Errorf("result-bearing message %d reported subagent Pending=%d, want 0 (seq=%v)", i, p, got)
			}
		}
		assertKindAbsentThroughout(t, msgs, agentrun.BackgroundShell)
	})

	t.Run("per_kind_with_shell_tracking", func(t *testing.T) {
		msgs := replayFixtureMessages(t, "subagent", "fg.jsonl",
			cli.WithSubagentTools("Task", "Agent"), cli.WithShellTracking())
		got := kindPendingSeq(msgs, agentrun.BackgroundSubagent)
		t.Logf("subagent Pending() sequence: %v", got)
		for i, p := range got {
			if p != 0 {
				t.Errorf("result-bearing message %d reported subagent Pending=%d, want 0 (seq=%v)", i, p, got)
			}
		}
		// The backgrounded bash task nets out to quiescent at every stamp
		// (it starts and finishes within the same turn), but Started/Finished
		// must still show it happened rather than reading as untracked.
		assertPendingSeq(t, msgs, agentrun.BackgroundShell, []int{0, 0, 0})
		assertFinalCounts(t, msgs, agentrun.BackgroundShell, agentrun.TaskCounts{Started: 1, Finished: 1})
	})
}
