//go:build !windows

package cli_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// drainToResult reads from the process output until MessageResult, returning
// without waiting for the channel to close. This mirrors what agentrun.RunTurn
// does: it returns as soon as the turn completes, then the caller sends the
// next turn. It is precisely this "send right after the result, before the
// subprocess's readLoop closes the channel" timing that exposes the
// turn-boundary race.
func drainToResult(ctx context.Context, p agentrun.Process) error {
	for {
		select {
		case msg, ok := <-p.Output():
			if !ok {
				return fmt.Errorf("output channel closed without a result: %w", p.Err())
			}
			if msg.Type == agentrun.MessageResult {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// TestResumeRace_SendImmediatelyAfterResult reproduces the spawn-per-turn
// turn-boundary race: Send is called right after MessageResult arrives, while
// the previous subprocess's readLoop is still running its exit defer. If Send's
// terminal-state check and the readLoop's finish() are not synchronized, the
// resume reuses an output channel that finish() just closed — dropping the
// resumed turn (closed channel, no result) or tripping the race detector.
//
// Run with -race. Many iterations because the bad interleaving is timing
// dependent; with printf's near-instant turns it reproduces readily.
func TestResumeRace_SendImmediatelyAfterResult(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binPrintf, []string{"%s\\n__RESULT__\\n", s.Prompt}
			},
			parseFn: resultParser,
		},
		resumeFn: func(_ agentrun.Session, prompt string) (string, []string, error) {
			return binPrintf, []string{"%s\\n__RESULT__\\n", prompt}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "turn0"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	ctx := testCtx(t)
	const turns = 300
	for i := 0; i < turns; i++ {
		// Drain the current turn up to its result (channel stays open), then
		// immediately resume — the exact RunTurn timing that triggers the race.
		if err := drainToResult(ctx, p); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if err := p.Send(ctx, fmt.Sprintf("turn%d", i+1)); err != nil {
			t.Fatalf("turn %d send: %v", i, err)
		}
	}
}

// TestReplaceFallback_CrashedTurnNotSilentlyResumed covers the regression in the
// replaceSubprocess done-closed fallback: when a turn finalizes with an error
// WHILE a Send is mid-flight (after the outer SendBlocks check, before the inner
// locked re-check), the fallback must surface ErrTerminated — not silently
// resume the crashed session (which would also swallow the exit error).
//
// The interleaving is made deterministic: turn 1 emits its result, then stays
// alive briefly before exiting non-zero (so the outer check sees done open and
// routes to replaceSubprocess); resumeFn then blocks long enough for turn 1 to
// finalize with the exit error, so the inner locked re-check observes done
// closed with a non-nil termErr — exactly the fallback path.
func TestReplaceFallback_CrashedTurnNotSilentlyResumed(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) {
				// Emit result, linger 150ms (done stays open past the outer check),
				// then crash.
				return binBash, []string{"-c", `printf 'x\n__RESULT__\n'; sleep 0.15; exit 1`}
			},
			parseFn: resultParser,
		},
		resumeFn: func(_ agentrun.Session, _ string) (string, []string, error) {
			// Block past turn 1's crash so the inner re-check sees done closed.
			time.Sleep(400 * time.Millisecond)
			return binPrintf, []string{"resumed\\n__RESULT__\\n"}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "turn1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	if err := drainToResult(testCtx(t), p); err != nil {
		t.Fatalf("drain turn1: %v", err)
	}
	// Send mid-flight: outer check sees done open -> replaceSubprocess; resumeFn
	// blocks while turn 1 crashes; inner re-check must yield ErrTerminated.
	err = p.Send(testCtx(t), "turn2")
	if !errors.Is(err, agentrun.ErrTerminated) {
		t.Fatalf("Send after crashed turn = %v; want ErrTerminated (no silent resume)", err)
	}
}
