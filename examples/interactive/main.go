//go:build !windows

// Command interactive demonstrates multi-turn streaming conversations
// with agentrun. It starts a Claude session and runs a REPL loop,
// sending user input and printing streamed responses.
//
// Requires the claude CLI on PATH.
// Run via: cd examples && CLAUDECODE= go run ./interactive/
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
)

const stopTimeout = 5 * time.Second

func main() {
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

	fmt.Println("agentrun interactive example (type 'exit' to quit)")
	return repl(ctx, proc)
}

// repl runs the read-eval-print loop, reading user input from stdin
// and sending it to the process until exit, quit, or EOF.
func repl(ctx context.Context, proc agentrun.Process) error {
	scanner := bufio.NewScanner(os.Stdin)
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

		if err := drainTurn(ctx, proc); err != nil {
			return err
		}
	}

	fmt.Println("\nbye")
	return nil
}

// drainTurn reads messages until MessageResult (turn complete).
// Handles streaming deltas for live token display. MessageError is
// printed but does not terminate the REPL.
func drainTurn(ctx context.Context, proc agentrun.Process) error {
	var sawDelta bool
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("interrupted: %w", ctx.Err())
		case msg, ok := <-proc.Output():
			if !ok {
				return channelClosed(proc)
			}
			sawDelta = handleStreamingMessage(msg, sawDelta)
			if msg.Type == agentrun.MessageResult {
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
	case agentrun.MessageTextDelta:
		fmt.Print(msg.Content) // live token, no newline
		return true
	case agentrun.MessageText:
		if sawDelta {
			fmt.Println() // newline to cap delta stream
		} else {
			printMessage(msg) // no deltas — print full text
		}
		return false
	case agentrun.MessageResult, agentrun.MessageError:
		if sawDelta {
			fmt.Println() // newline to cap delta stream
		}
		printMessage(msg)
		return false
	default:
		printMessage(msg)
		return sawDelta
	}
}

// printMessage formats and prints a single message.
// keep in sync with examples/simple/main.go:printMessage
func printMessage(msg agentrun.Message) {
	switch msg.Type {
	case agentrun.MessageInit:
		fmt.Println("[init]    (session started)")
	case agentrun.MessageText:
		fmt.Printf("[text]    %s\n", msg.Content)
	case agentrun.MessageResult:
		fmt.Printf("[result]  %s\n", msg.Content)
	case agentrun.MessageError:
		fmt.Fprintf(os.Stderr, "[error]   %s\n", msg.Content)
	case agentrun.MessageToolResult:
		fmt.Printf("[tool]    %s\n", msg.Tool.Name)
	case agentrun.MessageSystem, agentrun.MessageEOF:
		// silent
	default:
		fmt.Printf("[%s]  %s\n", msg.Type, msg.Content)
	}
}
