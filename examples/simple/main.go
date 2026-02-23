//go:build !windows

// Command simple demonstrates the full agentrun lifecycle:
// validate the engine, start a session, stream messages, and stop.
//
// It doubles as a smoke test — exits 0 on success, 1 on failure.
// Run via: make smoke (requires the claude CLI on PATH).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
	"github.com/dmora/agentrun/examples/internal/display"
	"github.com/dmora/agentrun/filter"
)

func main() {
	fmt.Println("agentrun simple example")
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("ok: received response from Claude")
}

const smokeTimeout = 60 * time.Second

func run() error {
	backend := claude.New()
	engine := cli.NewEngine(backend)
	if err := engine.Validate(); err != nil {
		return fmt.Errorf("engine unavailable: %w", err)
	}

	dir, err := os.MkdirTemp("", "agentrun-example-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD:    dir,
		Prompt: "What is 2+2? Reply with only the number.",
		Options: map[string]string{
			// Cross-cutting options (root vocabulary).
			agentrun.OptionMaxTurns: "1",
			// Claude-specific options (backend dialect).
			claude.OptionPermissionMode: string(claude.PermissionPlan),
		},
	}

	proc, err := engine.Start(ctx, session)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	defer func() { _ = proc.Stop(context.Background()) }()

	// Claude backend uses streaming mode (stdin pipe). The initial prompt
	// must be sent explicitly — StreamArgs omits the trailing prompt arg.
	if err := proc.Send(ctx, session.Prompt); err != nil {
		return fmt.Errorf("send prompt: %w", err)
	}

	return drainMessages(ctx, proc)
}

// drainMessages reads all messages from the process, printing each one,
// and returns an error if the agent reported errors, no response arrived,
// or the context deadline is exceeded. Uses filter.Completed to drop
// streaming deltas — this example only cares about complete messages.
func drainMessages(ctx context.Context, proc agentrun.Process) error {
	completed := filter.Completed(ctx, proc.Output())
	var gotResponse bool
	var agentErr string
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for response: %w", ctx.Err())
		case msg, ok := <-completed:
			if !ok {
				return checkResult(proc, agentErr, gotResponse)
			}
			display.PrintMessage(msg)
			switch msg.Type {
			case agentrun.MessageText:
				gotResponse = true
			case agentrun.MessageResult:
				gotResponse = true
				_ = proc.Stop(context.Background())
			case agentrun.MessageError:
				agentErr = msg.Content
			default:
			}
		}
	}
}

// checkResult validates the final process state after all messages are drained.
func checkResult(proc agentrun.Process, agentErr string, gotResponse bool) error {
	// Check for process-level errors (e.g., non-zero exit before result).
	// Filter ErrTerminated — that's the expected outcome of our explicit Stop.
	if err := proc.Err(); err != nil && !errors.Is(err, agentrun.ErrTerminated) {
		return fmt.Errorf("process exited with error: %w", err)
	}
	if agentErr != "" {
		return fmt.Errorf("agent error: %s", agentErr)
	}
	if !gotResponse {
		return errors.New("no response received")
	}
	return nil
}
