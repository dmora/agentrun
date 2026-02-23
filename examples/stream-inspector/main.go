//go:build !windows

// Command stream-inspector is a protocol debugging tool for agentrun.
// It prints every message from the output channel with timestamps,
// parsed types, and raw stdout lines — including system/lifecycle
// events that normal examples suppress.
//
// Useful for verifying which event types a CLI backend actually emits
// (e.g., thinking_delta, signature_delta) and for latency analysis.
//
// Requires the claude CLI on PATH.
// Run via: cd examples && CLAUDECODE= go run ./stream-inspector/
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
	"github.com/dmora/agentrun/examples/internal/display"
)

const stopTimeout = 5 * time.Second

func main() {
	fmt.Fprintln(os.Stderr, "WARNING: This tool prints all model output including thinking blocks.")
	fmt.Fprintln(os.Stderr, "         Do not use with sensitive prompts or in shared terminals.")
	fmt.Fprintln(os.Stderr, "")

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	backend := claude.New()
	engine := cli.NewEngine(backend)
	if err := engine.Validate(); err != nil {
		return fmt.Errorf("engine unavailable: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	session := agentrun.Session{
		CWD: cwd,
		Options: map[string]string{
			claude.OptionPermissionMode: string(claude.PermissionPlan),
		},
	}

	proc, err := engine.Start(ctx, session)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		_ = proc.Stop(stopCtx)
	}()

	fmt.Println("stream-inspector (type 'exit' to quit)")
	return repl(ctx, proc)
}

func repl(ctx context.Context, proc agentrun.Process) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nyou> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}

		if err := proc.Send(ctx, line); err != nil {
			return fmt.Errorf("send: %w", err)
		}

		if err := drainTurn(ctx, proc); err != nil {
			return err
		}
	}

	fmt.Println("\nbye")
	return nil
}

func drainTurn(ctx context.Context, proc agentrun.Process) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("interrupted: %w", ctx.Err())
		case msg, ok := <-proc.Output():
			if !ok {
				if err := proc.Err(); err != nil {
					return fmt.Errorf("process exited: %w", err)
				}
				return errors.New("process exited unexpectedly")
			}
			display.PrintRaw(msg)
			if msg.Type == agentrun.MessageResult {
				return nil
			}
		}
	}
}
