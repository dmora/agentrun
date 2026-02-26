//go:build !windows

// Command custom-backend demonstrates implementing a custom agentrun backend.
//
// It defines an "echo" backend that spawns printf to echo the user's prompt
// back as JSON messages, simulating a simple agent. This shows the minimum
// viable implementation: Spawner + Parser + Resumer.
//
// Run via:
//
//	cd examples && go run ./custom-backend/
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// echoBackend implements cli.Backend (Spawner + Parser) plus Resumer
// to enable multi-turn conversation. It spawns printf to output JSON
// lines to stdout, which the CLIEngine reads via bufio.Scanner.
type echoBackend struct{}

// SpawnArgs returns a printf command that outputs JSON messages.
func (b *echoBackend) SpawnArgs(session agentrun.Session) (string, []string) {
	return "printf", printfArgs(session.Prompt)
}

// ParseLine converts a JSON stdout line into an agentrun.Message.
func (b *echoBackend) ParseLine(line string) (agentrun.Message, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return agentrun.Message{}, cli.ErrSkipLine
	}
	var ev struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return agentrun.Message{}, err
	}
	return agentrun.Message{
		Type:    agentrun.MessageType(ev.Type),
		Content: ev.Content,
	}, nil
}

// ResumeArgs enables multi-turn by re-spawning printf with the new message.
// This is the spawn-per-turn pattern used by backends like Codex and OpenCode.
func (b *echoBackend) ResumeArgs(_ agentrun.Session, message string) (string, []string, error) {
	return "printf", printfArgs(message), nil
}

// printfArgs builds printf arguments that output two JSON lines:
// a text message echoing the prompt, and a result message.
func printfArgs(prompt string) []string {
	text := fmt.Sprintf(`{"type":"text","content":%s}`, mustJSON("Echo: "+prompt))
	result := `{"type":"result","content":"done"}`
	return []string{"%s", text + "\n" + result + "\n"}
}

// mustJSON marshals a string to a JSON value (with proper escaping).
func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	engine := cli.NewEngine(&echoBackend{})
	if err := engine.Validate(); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	proc, err := engine.Start(context.Background(), agentrun.Session{
		CWD:    cwd,
		Prompt: "Hello, agentrun!",
	})
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	defer func() { _ = proc.Stop(context.Background()) }()

	// Drain the first turn — spawn-per-turn backends bake the prompt
	// into args, so output arrives immediately without calling Send.
	fmt.Println("--- turn 1 ---")
	for msg := range proc.Output() {
		fmt.Printf("[%s] %s\n", msg.Type, msg.Content)
	}

	// Second turn via Send — demonstrates the Resumer (spawn-per-turn) pattern.
	fmt.Println("--- turn 2 ---")
	if err := proc.Send(context.Background(), "How are you?"); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	for msg := range proc.Output() {
		fmt.Printf("[%s] %s\n", msg.Type, msg.Content)
	}

	return nil
}
