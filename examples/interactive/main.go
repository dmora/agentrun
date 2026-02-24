//go:build !windows

// Command interactive demonstrates multi-turn conversations with agentrun.
// It supports both Claude and OpenCode backends via the --backend flag.
//
// Claude uses streaming stdin; OpenCode uses spawn-per-turn with --session.
// The engine handles this distinction automatically — the example detects
// the backend's capabilities to manage turn-completion semantics.
//
// Run via:
//
//	cd examples && go run ./interactive/ --backend claude
//	cd examples && go run ./interactive/ --backend opencode
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
	"github.com/dmora/agentrun/engine/cli/opencode"
	"github.com/dmora/agentrun/examples/internal/display"
)

const stopTimeout = 5 * time.Second

func main() {
	backendFlag := flag.String("backend", "claude", "backend to use: claude or opencode")
	flag.Parse()

	if err := run(*backendFlag); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(backendName string) error {
	backend, err := makeBackend(backendName)
	if err != nil {
		return err
	}
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

	// Read first prompt before Start() — spawn backends need it in args.
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("agentrun interactive example (type 'exit' to quit)")
	fmt.Print("\nyou> ")
	if !scanner.Scan() {
		fmt.Println("\nbye")
		return nil
	}
	firstPrompt := strings.TrimSpace(scanner.Text())
	if firstPrompt == "" || firstPrompt == "exit" || firstPrompt == "quit" {
		fmt.Println("\nbye")
		return nil
	}

	session := agentrun.Session{
		CWD:    cwd,
		Prompt: firstPrompt,
		Options: map[string]string{
			agentrun.OptionMode: string(agentrun.ModePlan),
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

	// Determine turn-completion mode.
	// Streaming backends (Claude): one MessageResult per turn.
	// Spawn-per-turn backends (OpenCode): multiple step_finish events per turn;
	// the turn is complete when the subprocess exits (channel closes).
	_, isStreamer := backend.(cli.Streamer)
	spawnPerTurn := !isStreamer

	// Streaming backends need first prompt sent explicitly via stdin.
	// Spawn backends already have the prompt baked into the command args.
	if isStreamer {
		if err := proc.Send(ctx, firstPrompt); err != nil {
			return fmt.Errorf("send first prompt: %w", err)
		}
	}

	// Drain first turn.
	if err := drainTurn(ctx, proc, spawnPerTurn); err != nil {
		return err
	}

	return repl(ctx, proc, scanner, spawnPerTurn)
}

// makeBackend creates a CLI backend by name.
func makeBackend(name string) (cli.Backend, error) {
	switch name {
	case "claude":
		return claude.New(), nil
	case "opencode":
		return opencode.New(), nil
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: claude, opencode)", name)
	}
}

// repl runs the read-eval-print loop, reading user input from stdin
// and sending it to the process until exit, quit, or EOF.
func repl(ctx context.Context, proc agentrun.Process, scanner *bufio.Scanner, spawnPerTurn bool) error {
	for {
		fmt.Print("\nyou> ")
		if !scanner.Scan() {
			break // EOF or Ctrl+D
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

		if err := drainTurn(ctx, proc, spawnPerTurn); err != nil {
			return err
		}
	}

	fmt.Println("\nbye")
	return nil
}

// drainTurn reads messages until the turn is complete.
//
// For streaming backends (spawnPerTurn=false), a turn ends with a single
// MessageResult event. For spawn-per-turn backends (spawnPerTurn=true),
// a turn may contain multiple step_finish (MessageResult) events — one per
// agent step — so the turn is complete when the output channel closes
// (the subprocess exits after producing all events).
func drainTurn(ctx context.Context, proc agentrun.Process, spawnPerTurn bool) error {
	var sawDelta bool
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("interrupted: %w", ctx.Err())
		case msg, ok := <-proc.Output():
			if !ok {
				if spawnPerTurn {
					return nil // expected: subprocess exited, turn complete
				}
				return channelClosed(proc)
			}
			sawDelta = handleStreamingMessage(msg, sawDelta)
			if !spawnPerTurn && msg.Type == agentrun.MessageResult {
				return nil // turn complete
			}
		}
	}
}

// channelClosed returns an appropriate error when the output channel closes.
func channelClosed(proc agentrun.Process) error {
	if err := proc.Err(); err != nil {
		return fmt.Errorf("process exited: %w", err)
	}
	return errors.New("process exited unexpectedly")
}

// handleStreamingMessage prints a message with delta-aware formatting.
// Returns the updated sawDelta state.
func handleStreamingMessage(msg agentrun.Message, sawDelta bool) bool {
	switch msg.Type {
	case agentrun.MessageTextDelta, agentrun.MessageThinkingDelta, agentrun.MessageToolUseDelta:
		fmt.Print(msg.Content) // live token, no newline
		return true
	case agentrun.MessageText:
		if sawDelta {
			fmt.Println() // newline to cap delta stream
		} else {
			display.PrintMessage(msg) // no deltas — print full text
		}
		return false
	case agentrun.MessageResult, agentrun.MessageError:
		if sawDelta {
			fmt.Println() // newline to cap delta stream
		}
		display.PrintMessage(msg)
		return false
	default:
		display.PrintMessage(msg)
		return sawDelta
	}
}
