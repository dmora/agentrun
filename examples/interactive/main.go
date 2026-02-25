//go:build !windows

// Command interactive demonstrates multi-turn conversations with agentrun.
// It supports Claude, OpenCode, and ACP backends via the --backend flag.
//
// Claude uses streaming stdin; OpenCode uses spawn-per-turn with --session.
// ACP uses a persistent JSON-RPC 2.0 subprocess — turns are instant after
// the first MCP cold boot.
//
// Session resume: the session ID is captured from MessageInit.Content and
// printed at session start. Pass --resume <id> to resume a saved session.
//
// Run via:
//
//	cd examples && go run ./interactive/ --backend claude
//	cd examples && go run ./interactive/ --backend opencode
//	cd examples && go run ./interactive/ --backend acp
//	cd examples && go run ./interactive/ --backend acp --binary gemini
//	cd examples && go run ./interactive/ --backend acp --binary goose
//	cd examples && go run ./interactive/ --backend acp --binary myagent --args serve,--acp
//	cd examples && go run ./interactive/ --backend claude --resume conv-abc123
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
	"github.com/dmora/agentrun/engine/acp"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
	"github.com/dmora/agentrun/engine/cli/opencode"
	"github.com/dmora/agentrun/examples/internal/display"
)

const (
	stopTimeout     = 5 * time.Second
	backendOpenCode = "opencode"
)

func main() {
	backendFlag := flag.String("backend", "claude", "backend to use: claude, opencode, or acp")
	binaryFlag := flag.String("binary", "", "ACP agent binary (used with --backend acp)")
	argsFlag := flag.String("args", "", "comma-separated args for ACP binary (e.g. \"acp\" or \"--experimental-acp\")")
	resumeFlag := flag.String("resume", "", "session ID to resume (from previous MessageInit)")
	flag.Parse()

	if err := run(*backendFlag, *binaryFlag, *argsFlag, *resumeFlag); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(backendName, binaryName, argsStr, resumeID string) error {
	engine, spawnPerTurn, err := makeEngine(backendName, binaryName, argsStr)
	if err != nil {
		return err
	}
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
	if resumeID != "" {
		fmt.Printf("resuming session: %s\n", resumeID)
	}
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

	proc, err := engine.Start(ctx, buildSession(cwd, firstPrompt, resumeID))
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		_ = proc.Stop(stopCtx)
	}()

	// Build turn function based on backend mode.
	turn := streamingTurn
	if spawnPerTurn {
		turn = spawnTurn
	}

	// First turn: spawn-per-turn bakes prompt into args; streaming sends explicitly.
	if spawnPerTurn {
		if err := drainTurn(ctx, proc); err != nil {
			return err
		}
	} else {
		if err := sendAndDrain(ctx, proc, firstPrompt); err != nil {
			return fmt.Errorf("send first prompt: %w", err)
		}
	}

	return repl(ctx, proc, scanner, turn)
}

// buildSession creates a session with optional resume support.
func buildSession(cwd, prompt, resumeID string) agentrun.Session {
	opts := map[string]string{
		agentrun.OptionMode: string(agentrun.ModePlan),
	}
	if resumeID != "" {
		opts[agentrun.OptionResumeID] = resumeID
	}
	return agentrun.Session{CWD: cwd, Prompt: prompt, Options: opts}
}

// makeEngine creates an engine by name. Returns the engine and whether it
// uses spawn-per-turn semantics (vs streaming).
func makeEngine(name, binary, argsStr string) (agentrun.Engine, bool, error) {
	switch name {
	case "claude":
		return cli.NewEngine(claude.New()), false, nil
	case backendOpenCode:
		return cli.NewEngine(opencode.New()), true, nil
	case "acp":
		if binary == "" {
			binary = backendOpenCode
		}
		args := acpArgs(binary, argsStr)
		return acp.NewEngine(acp.WithBinary(binary), acp.WithArgs(args...)), false, nil
	default:
		return nil, false, fmt.Errorf("unknown backend %q (valid: claude, opencode, acp)", name)
	}
}

// acpArgs resolves CLI arguments for an ACP binary.
// Explicit --args override built-in defaults for known binaries.
func acpArgs(binary, argsStr string) []string {
	if argsStr != "" {
		return strings.Split(argsStr, ",")
	}
	switch binary {
	case backendOpenCode:
		return []string{"acp"}
	case "gemini":
		return []string{"--experimental-acp"}
	default:
		return nil
	}
}

// repl runs the read-eval-print loop, reading user input from stdin
// and sending it to the process until exit, quit, or EOF.
func repl(ctx context.Context, proc agentrun.Process, scanner *bufio.Scanner, turn turnFunc) error {
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
		if err := turn(ctx, proc, line); err != nil {
			return err
		}
	}

	fmt.Println("\nbye")
	return nil
}

// turnFunc executes one conversation turn: send the message and drain output.
type turnFunc func(ctx context.Context, proc agentrun.Process, message string) error

// streamingTurn sends a message and drains output concurrently (ACP, Claude).
func streamingTurn(ctx context.Context, proc agentrun.Process, message string) error {
	return sendAndDrain(ctx, proc, message)
}

// spawnTurn sends a message then drains until the subprocess exits (OpenCode).
func spawnTurn(ctx context.Context, proc agentrun.Process, message string) error {
	if err := proc.Send(ctx, message); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return drainTurn(ctx, proc)
}

// sendAndDrain runs Send() and drainTurn() concurrently for streaming backends.
// Output is printed as it arrives while the RPC blocks. If Send() returns an
// error (e.g. auth failure), drainTurn stops immediately.
func sendAndDrain(ctx context.Context, proc agentrun.Process, message string) error {
	drainCtx, drainCancel := context.WithCancel(ctx)
	defer drainCancel()

	// Drain output in background — prints messages as they arrive.
	drainCh := make(chan error, 1)
	go func() {
		drainCh <- drainStreamingTurn(drainCtx, proc)
	}()

	// Send blocks until RPC response.
	sendErr := proc.Send(ctx, message)

	if sendErr != nil {
		// RPC failed (auth error, etc.) — stop draining.
		drainCancel()
		<-drainCh // wait for drain goroutine to exit
		return sendErr
	}

	// RPC succeeded — drain goroutine will see MessageResult and exit.
	return <-drainCh
}

// drainTurn reads messages until a spawn-per-turn subprocess exits.
func drainTurn(ctx context.Context, proc agentrun.Process) error {
	var sawDelta bool
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("interrupted: %w", ctx.Err())
		case msg, ok := <-proc.Output():
			if !ok {
				if err := proc.Err(); err != nil {
					return fmt.Errorf("process exited: %w", err)
				}
				return nil // expected: subprocess exited, turn complete
			}
			sawDelta = handleStreamingMessage(msg, sawDelta)
		}
	}
}

// drainStreamingTurn reads messages until MessageResult or context cancellation.
func drainStreamingTurn(ctx context.Context, proc agentrun.Process) error {
	var sawDelta bool
	for {
		select {
		case <-ctx.Done():
			return nil // cancelled by sendAndDrain on Send() error
		case msg, ok := <-proc.Output():
			if !ok {
				if err := proc.Err(); err != nil {
					return fmt.Errorf("process exited: %w", err)
				}
				return errors.New("process exited unexpectedly")
			}
			sawDelta = handleStreamingMessage(msg, sawDelta)
			if msg.Type == agentrun.MessageResult {
				return nil // turn complete
			}
		}
	}
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
