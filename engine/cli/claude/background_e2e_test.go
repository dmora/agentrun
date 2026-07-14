//go:build !windows

package claude_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
)

// resultTypes maps msgs to their Type, for a compact failure message when a
// result-bearing sequence's SHAPE (not just its Pending() counts) matters:
// this file's fixtures interleave MessageResult and MessageTaskResult, and
// mistaking one for the other at the wrong position is exactly the #61
// hazard (a follow-up turn's own result vs. a background task's completion).
func resultTypes(msgs []agentrun.Message) []agentrun.MessageType {
	types := make([]agentrun.MessageType, len(msgs))
	for i, m := range msgs {
		types[i] = m.Type
	}
	return types
}

// TestE2E_BackgroundTask_Streaming_FirstResultCatchesLiveTask replays the
// live-capture streaming.jsonl transcript (phase-0 probe) with
// WithSubagentTools+WithShellTracking. This is the issue-#61 moment itself:
// the parent's OWN result (event 14) arrives while its backgrounded bash
// command (bfg5749vm, started at event 9) is still running, so the FIRST
// MessageResult must stamp Shell Pending()==1 — not the false quiescence a
// bash-filtered snapshot used to produce. The drain snapshot (15), demoted
// completion notification (17, MessageTaskResult), Claude's SAME-session
// auto-resume (18), and the follow-up turn's own result (20) must then
// settle Shell to quiescent {Started:1 Finished:1}.
func TestE2E_BackgroundTask_Streaming_FirstResultCatchesLiveTask(t *testing.T) {
	msgs := replayFixtureMessages(t, "backgroundtask", "streaming.jsonl",
		cli.WithSubagentTools("Task", "Agent"), cli.WithShellTracking())

	results := resultBearing(msgs)
	wantTypes := []agentrun.MessageType{
		agentrun.MessageResult,     // event 14: interim result, task still live
		agentrun.MessageTaskResult, // event 17: demoted "completed" notification
		agentrun.MessageResult,     // event 20: auto-resume follow-up turn
	}
	if len(results) != len(wantTypes) {
		t.Fatalf("result-bearing types = %v, want %v", resultTypes(results), wantTypes)
	}
	for i, want := range wantTypes {
		if results[i].Type != want {
			t.Errorf("result-bearing[%d].Type = %q, want %q", i, results[i].Type, want)
		}
	}

	t.Logf("shell Pending() sequence: %v", kindPendingSeq(msgs, agentrun.BackgroundShell))
	assertPendingSeq(t, msgs, agentrun.BackgroundShell, []int{1, 0, 0})
	assertFinalCounts(t, msgs, agentrun.BackgroundShell, agentrun.TaskCounts{Started: 1, Finished: 1})
}

// TestE2E_BackgroundTask_Streaming_SnapshotCarriesDescription pins ADR-3's
// unconditional-parse contract at the wire level, independent of tracking
// state: the FIRST MessageBackgroundTasks snapshot must carry the bash
// task's backend-supplied Description — the field that used to be
// impossible to see at all, because a bash-only snapshot parsed to an EMPTY
// Message.Tasks (wire-indistinguishable from "no background work").
func TestE2E_BackgroundTask_Streaming_SnapshotCarriesDescription(t *testing.T) {
	msgs := replayFixtureMessages(t, "backgroundtask", "streaming.jsonl",
		cli.WithSubagentTools("Task", "Agent"), cli.WithShellTracking())

	var snapshot *agentrun.Message
	for i := range msgs {
		if msgs[i].Type == agentrun.MessageBackgroundTasks {
			snapshot = &msgs[i]
			break
		}
	}
	if snapshot == nil {
		t.Fatal("no MessageBackgroundTasks snapshot observed")
	}
	if len(snapshot.Tasks) != 1 {
		t.Fatalf("first snapshot Tasks = %+v, want exactly 1 entry", snapshot.Tasks)
	}
	task := snapshot.Tasks[0]
	if task.ID != "bfg5749vm" {
		t.Errorf("Tasks[0].ID = %q, want %q", task.ID, "bfg5749vm")
	}
	if task.Kind != agentrun.BackgroundShell {
		t.Errorf("Tasks[0].Kind = %q, want %q", task.Kind, agentrun.BackgroundShell)
	}
	const wantDescription = "Sleep 15 seconds then print marker"
	if task.Description != wantDescription {
		t.Errorf("Tasks[0].Description = %q, want %q", task.Description, wantDescription)
	}
}

// TestE2E_BackgroundTask_StdinEOF_StoppedIsTerminalAndDrains replays the
// stdin-EOF transcript: the CLI cancels its one outstanding background task
// at shutdown (task_notification status "stopped") rather than letting it
// run to completion — no follow-up turn is possible off a dead subprocess.
// v0.8.1 already made "stopped" map to a terminal MessageTaskResult (not a
// progress update); this test pins that the tracker's Shell books actually
// drain because of it, end to end through the real parser.
func TestE2E_BackgroundTask_StdinEOF_StoppedIsTerminalAndDrains(t *testing.T) {
	msgs := replayFixtureMessages(t, "backgroundtask", "stdin-eof.jsonl",
		cli.WithSubagentTools("Task", "Agent"), cli.WithShellTracking())

	results := resultBearing(msgs)
	wantTypes := []agentrun.MessageType{
		agentrun.MessageResult,     // interim result, task still live
		agentrun.MessageTaskResult, // "stopped" notification, demoted terminal
	}
	if len(results) != len(wantTypes) {
		t.Fatalf("result-bearing types = %v, want %v", resultTypes(results), wantTypes)
	}
	for i, want := range wantTypes {
		if results[i].Type != want {
			t.Errorf("result-bearing[%d].Type = %q, want %q", i, results[i].Type, want)
		}
	}
	if !bytes.Contains(results[1].Raw, []byte(`"status":"stopped"`)) {
		t.Errorf("result-bearing[1].Raw does not contain the \"stopped\" notification: %s", results[1].Raw)
	}

	t.Logf("shell Pending() sequence: %v", kindPendingSeq(msgs, agentrun.BackgroundShell))
	assertPendingSeq(t, msgs, agentrun.BackgroundShell, []int{1, 0})
}

// TestE2E_BackgroundTask_Stop_UndrainedBuffer_NoDeadlock replays
// streaming.jsonl into a deliberately 1-slot output buffer and never drains
// Output(): the readLoop is certainly blocked on a channel send (see
// emitWithSynthesis in process.go) by the time Stop is called. Stop must
// still return within its grace period — cancelRead unblocks the blocked
// send — and, since Stop only returns after cmdDone is signaled by
// readLoop's own deferred exit, a bounded return time is itself proof the
// reader goroutine wound down (no leak). Run with -race: the tracker
// (background.go) observes/stamps messages on the readLoop goroutine while
// Stop tears down concurrently from the test goroutine.
func TestE2E_BackgroundTask_Stop_UndrainedBuffer_NoDeadlock(t *testing.T) {
	replayBuildOnce.Do(buildReplayBinary)
	if errReplayBuild != nil {
		t.Fatalf("mock-replay build failed: %v", errReplayBuild)
	}
	abs, err := filepath.Abs(filepath.Join("testdata", "backgroundtask", "streaming.jsonl"))
	if err != nil {
		t.Fatalf("abs fixture path: %v", err)
	}

	baseline := runtime.NumGoroutine()

	backend := claude.New(claude.WithBinary(replayBinary))
	engine := cli.NewEngine(backend,
		cli.WithSubagentTools("Task", "Agent"),
		cli.WithShellTracking(),
		cli.WithOutputBuffer(1),
		cli.WithGracePeriod(500*time.Millisecond),
	)

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
	// Safety net only: Stop is idempotent (sync.Once-guarded), so this is a
	// no-op once the explicit, timed Stop call below has already run. Guards
	// against leaking the subprocess if Send (next) fails.
	defer func() { _ = proc.Stop(context.Background()) }()

	if err := proc.Send(ctx, "go"); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Give the subprocess time to fill the 1-slot buffer and block on the
	// next send — Output() is deliberately never read in this test.
	time.Sleep(300 * time.Millisecond)

	stopDone := make(chan error, 1)
	start := time.Now()
	go func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		stopDone <- proc.Stop(stopCtx)
	}()

	var stopErr error
	select {
	case stopErr = <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop deadlocked with an undrained output buffer")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Stop took %v, want well within its grace period", elapsed)
	}
	if !errors.Is(stopErr, agentrun.ErrTerminated) {
		t.Errorf("Stop error = %v, want ErrTerminated", stopErr)
	}

	if !goroutinesSettle(baseline, 2*time.Second) {
		t.Errorf("goroutine count did not settle back near baseline (%d) after Stop; got %d", baseline, runtime.NumGoroutine())
	}
}

// goroutinesSettle polls runtime.NumGoroutine() until it returns to at most
// baseline+1 (small margin for test-runner/GC scheduling noise) or timeout
// elapses, returning the final comparison result either way.
func goroutinesSettle(baseline int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+1 {
			return true
		}
		if time.Now().After(deadline) {
			return runtime.NumGoroutine() <= baseline+1
		}
		time.Sleep(20 * time.Millisecond)
	}
}
