//go:build !windows

package cli_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// TestRunFirstTurn_SpawnPerTurnDrainsWithoutResume is the regression test for
// the run_turn/session_start double-prompt bug: for a spawn-per-turn backend,
// the first turn is initiated by Start (prompt baked into the spawn), so
// RunFirstTurn must drain it WITHOUT calling Send/ResumeArgs.
//
// ResumeArgs here always errors, mimicking a resume-only backend whose session
// ID has not been captured yet (Codex thread.started, OpenCode step_start, agy
// log file). Before the fix, the harness called RunTurn → Send → ResumeArgs and
// failed with exactly this error. RunFirstTurn must not trigger it.
func TestRunFirstTurn_SpawnPerTurnDrainsWithoutResume(t *testing.T) {
	resumeCalled := false
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binPrintf, []string{"%s\\n__RESULT__\\n", s.Prompt}
			},
			parseFn: resultParser,
		},
		resumeFn: func(_ agentrun.Session, _ string) (string, []string, error) {
			resumeCalled = true
			return "", nil, errors.New("no session ID captured yet")
		},
	}
	const prompt = "hello"
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: prompt})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	var msgs []agentrun.Message
	err = agentrun.RunFirstTurn(testCtx(t), p, prompt, func(m agentrun.Message) error {
		msgs = append(msgs, m)
		return nil
	})
	if err != nil {
		t.Fatalf("RunFirstTurn: %v (resume must not be called for the first turn)", err)
	}
	if resumeCalled {
		t.Error("ResumeArgs was called for the first turn; RunFirstTurn should drain only")
	}
	if len(msgs) != 2 || msgs[0].Content != prompt || msgs[1].Type != agentrun.MessageResult {
		t.Fatalf("first turn messages = %+v; want [text:hello, result]", msgs)
	}
}

// TestRunFirstTurn_SpawnPerTurnThenResume verifies the full lifecycle: the
// first turn is drained (no resume), and the SECOND turn resumes normally.
func TestRunFirstTurn_SpawnPerTurnThenResume(t *testing.T) {
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
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "turn1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	var first []agentrun.Message
	if err := agentrun.RunFirstTurn(testCtx(t), p, "turn1", func(m agentrun.Message) error {
		first = append(first, m)
		return nil
	}); err != nil {
		t.Fatalf("RunFirstTurn: %v", err)
	}
	if len(first) != 2 || first[0].Content != "turn1" {
		t.Fatalf("first turn = %+v; want [turn1, result]", first)
	}

	var second []agentrun.Message
	if err := agentrun.RunTurn(testCtx(t), p, "turn2", func(m agentrun.Message) error {
		second = append(second, m)
		return nil
	}); err != nil {
		t.Fatalf("RunTurn turn2: %v", err)
	}
	if len(second) != 2 || second[0].Content != "turn2" {
		t.Fatalf("second turn = %+v; want [turn2, result]", second)
	}
}
