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

// replaySubagentFixture drives the real cli engine + claude parser + subagent
// tracker over a captured transcript and returns Pending() for each stamped
// result-bearing message, in order.
func replaySubagentFixture(t *testing.T, fixture string) []int {
	t.Helper()
	replayBuildOnce.Do(buildReplayBinary)
	if errReplayBuild != nil {
		t.Fatalf("mock-replay build failed: %v", errReplayBuild)
	}
	abs, err := filepath.Abs(filepath.Join("testdata", "subagent", fixture))
	if err != nil {
		t.Fatalf("abs fixture path: %v", err)
	}

	backend := claude.New(claude.WithBinary(replayBinary))
	engine := cli.NewEngine(backend, cli.WithSubagentTools("Task", "Agent"))

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

	var pendings []int
	for {
		select {
		case msg, ok := <-proc.Output():
			if !ok {
				return pendings
			}
			switch msg.Type {
			case agentrun.MessageResult, agentrun.MessageSubagentResult:
				if msg.Subagents == nil {
					t.Errorf("%s should carry a Subagents stamp when tracking is enabled", msg.Type)
					pendings = append(pendings, -1)
					continue
				}
				pendings = append(pendings, msg.Subagents.Pending())
			}
		case <-ctx.Done():
			t.Fatalf("timed out draining output; collected %v", pendings)
		}
	}
}

// TestE2E_Subagent_BackgroundRevive is the load-bearing test: over the real
// run3 transcript, the auto-resume result must NOT report quiescence while the
// subagent is revived and still writing — that false "done" is the #153 bug.
func TestE2E_Subagent_BackgroundRevive(t *testing.T) {
	got := replaySubagentFixture(t, "bg-revive.jsonl")
	t.Logf("result-bearing Pending() sequence: %v", got)
	// Parent results: #1 outstanding, #2 outstanding (revive), #3 quiescent.
	// Interleaved MessageSubagentResult completions report the drained count.
	// Recover just the three MessageResult pendings by replaying the known
	// structure: results are the entries that follow snapshots draining/adding.
	// Simplest robust check: the sequence must CONTAIN a non-quiescent value
	// after the first drain (the revive), and END quiescent.
	parentResults := got
	if len(parentResults) < 3 {
		t.Fatalf("expected multiple result-bearing messages, got %v", got)
	}
	if got[0] != 1 {
		t.Errorf("first parent result should be outstanding (1), got %d (seq=%v)", got[0], got)
	}
	// The auto-resume parent result must be outstanding again (the revive).
	if !containsAfterFirstZero(got, 1) {
		t.Errorf("no outstanding (1) result after the premature quiesce — revive not tracked; seq=%v", got)
	}
	if got[len(got)-1] != 0 {
		t.Errorf("final result should be quiescent (0), got %d (seq=%v)", got[len(got)-1], got)
	}
}

// TestE2E_Subagent_Foreground confirms a foreground subagent nets out: the
// single result is quiescent, so no linger is triggered.
func TestE2E_Subagent_Foreground(t *testing.T) {
	got := replaySubagentFixture(t, "fg.jsonl")
	t.Logf("result-bearing Pending() sequence: %v", got)
	if len(got) == 0 {
		t.Fatal("expected at least one result-bearing message")
	}
	for i, p := range got {
		if p != 0 {
			t.Errorf("foreground result-bearing message %d reported Pending=%d, want 0 (seq=%v)", i, p, got)
		}
	}
}

// containsAfterFirstZero reports whether want appears after the first 0 in xs —
// i.e., an outstanding count returned after the subagent first drained.
func containsAfterFirstZero(xs []int, want int) bool {
	seenZero := false
	for _, x := range xs {
		if seenZero && x == want {
			return true
		}
		if x == 0 {
			seenZero = true
		}
	}
	return false
}
