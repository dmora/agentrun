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
			claude.OptionPermissionMode: string(claude.PermissionPlan),
			claude.OptionMaxTurns:       "1",
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

	return drainMessages(proc)
}

// drainMessages reads all messages from the process, printing each one,
// and returns an error if the agent reported errors or no response arrived.
func drainMessages(proc agentrun.Process) error {
	var gotResponse bool
	var agentErr string
	for msg := range proc.Output() {
		switch msg.Type {
		case agentrun.MessageInit:
			fmt.Println("[init]    (session started)")
		case agentrun.MessageText:
			fmt.Printf("[text]    %s\n", msg.Content)
			gotResponse = true
		case agentrun.MessageResult:
			fmt.Printf("[result]  %s\n", msg.Content)
			gotResponse = true
			// Result signals turn completion. In streaming mode Claude keeps
			// the stdin pipe open for follow-up messages, so we stop explicitly.
			_ = proc.Stop(context.Background())
		case agentrun.MessageError:
			fmt.Fprintf(os.Stderr, "[error]   %s\n", msg.Content)
			agentErr = msg.Content
		case agentrun.MessageToolResult:
			fmt.Printf("[tool]    %s\n", msg.Tool.Name)
		case agentrun.MessageSystem, agentrun.MessageEOF:
			// silent — system status and EOF are infrastructure signals
		default:
			fmt.Printf("[%s]  %s\n", msg.Type, msg.Content)
		}
	}

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
