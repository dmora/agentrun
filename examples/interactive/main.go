//go:build !windows

// Command interactive demonstrates multi-turn conversations with agentrun.
// It supports Claude, OpenCode, Codex, and ACP backends via the --backend flag.
//
// All backends use [agentrun.RunTurn] for turn execution — spawn-per-turn
// backends (OpenCode, Codex) satisfy [agentrun.SequentialSender], so RunTurn
// handles them transparently.
//
// Session resume: the session ID is captured from MessageInit.ResumeID and
// printed at session start. Pass --resume <id> to resume a saved session.
//
// Run via:
//
//	cd examples && go run ./interactive/ --backend claude
//	cd examples && go run ./interactive/ --backend opencode
//	cd examples && go run ./interactive/ --backend codex
//	cd examples && go run ./interactive/ --backend acp
//	cd examples && go run ./interactive/ --backend acp --binary gemini
//	cd examples && go run ./interactive/ --backend acp --binary goose
//	cd examples && go run ./interactive/ --backend acp --binary myagent --args serve,--acp
//	cd examples && go run ./interactive/ --backend claude --resume conv-abc123
package main

import (
	"bufio"
	"context"
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
	"github.com/dmora/agentrun/engine/cli/codex"
	"github.com/dmora/agentrun/engine/cli/opencode"
	"github.com/dmora/agentrun/examples/internal/display"
)

const (
	stopTimeout     = 5 * time.Second
	backendOpenCode = "opencode"
)

func main() {
	backendFlag := flag.String("backend", "claude", "backend to use: claude, opencode, codex, or acp")
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
	engine, err := makeEngine(backendName, binaryName, argsStr)
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

	// First turn — RunTurn handles all engine types.
	if err := executeTurn(ctx, proc, firstPrompt); err != nil {
		return err
	}

	return repl(ctx, proc, scanner)
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

// makeEngine creates an engine by name.
func makeEngine(name, binary, argsStr string) (agentrun.Engine, error) {
	switch name {
	case "claude":
		return cli.NewEngine(claude.New()), nil
	case backendOpenCode:
		return cli.NewEngine(opencode.New()), nil
	case "codex":
		return cli.NewEngine(codex.New()), nil
	case "acp":
		if binary == "" {
			binary = backendOpenCode
		}
		args := acpArgs(binary, argsStr)
		return acp.NewEngine(acp.WithBinary(binary), acp.WithArgs(args...)), nil
	default:
		return nil, fmt.Errorf("unknown backend %q (valid: claude, opencode, codex, acp)", name)
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
func repl(ctx context.Context, proc agentrun.Process, scanner *bufio.Scanner) error {
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
		if err := executeTurn(ctx, proc, line); err != nil {
			return err
		}
	}

	fmt.Println("\nbye")
	return nil
}

// executeTurn runs one conversation turn via RunTurn, printing messages
// with delta-aware formatting.
func executeTurn(ctx context.Context, proc agentrun.Process, message string) error {
	var sawDelta bool
	return agentrun.RunTurn(ctx, proc, message, func(msg agentrun.Message) error {
		sawDelta = handleStreamingMessage(msg, sawDelta)
		return nil
	})
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
