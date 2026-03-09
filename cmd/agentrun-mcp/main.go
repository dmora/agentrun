//go:build !windows

// Command agentrun-mcp is an MCP diagnostic server for debugging agentrun
// backends. It provides tools for listing backends, parsing JSONL output,
// running agent turns, and probing backend availability.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server-side configuration set via flags / env vars.
var (
	workspaceRoot string
	acpBinary     string
	acpArgs       stringSlice
	sessions      *sessionStore
)

// stringSlice implements flag.Value for repeatable string flags.
type stringSlice []string

func (s *stringSlice) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	flag.StringVar(&workspaceRoot, "workspace", "", "workspace root for CWD scope restriction (required)")
	flag.StringVar(&acpBinary, "acp-binary", os.Getenv("AGENTRUN_ACP_BINARY"), "ACP agent binary path")
	flag.Var(&acpArgs, "acp-arg", "additional argument for ACP binary (repeatable)")
	flag.Parse()

	if workspaceRoot == "" {
		slog.Error("--workspace flag is required")
		os.Exit(1)
	}

	// Startup assertion: sessions must not consume all semaphore capacity.
	if maxLiveSessions >= cap(sem) {
		panic(fmt.Sprintf("maxLiveSessions (%d) must be < semaphore capacity (%d)", maxLiveSessions, cap(sem)))
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	sessions = newSessionStore(maxLiveSessions)
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	startReaper(reaperCtx, sessions, sessionIdleTime, reaperInterval)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "agentrun-diag",
		Version: "0.1.0",
	}, nil)
	registerTools(server)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}

	// Shutdown: stop reaper and all live sessions.
	reaperCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	sessions.stopAll(shutdownCtx)
}

func registerTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_message_types",
		Description: "List all agentrun MessageType constants with descriptions",
	}, handleListMessageTypes)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_backends",
		Description: "List available agentrun backends with binary availability and capabilities",
	}, handleListBackends)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "parse_line",
		Description: "Parse a raw JSONL line using a specific backend's parser",
	}, handleParseLine)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "explain_session",
		Description: "Show the spawn/resume/stream args each backend would generate for a session",
	}, handleExplainSession)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "run_turn",
		Description: "Run a single agent turn with a backend and return all messages, stderr, and exit info",
	}, handleRunTurn)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_start",
		Description: "Start a persistent multi-turn session with a backend and run the first turn",
	}, handleSessionStart)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_send",
		Description: "Send a message to an existing session and return the agent's response",
	}, handleSessionSend)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_stop",
		Description: "Stop and remove a persistent session",
	}, handleSessionStop)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_sessions",
		Description: "List active sessions with metadata (truncated IDs)",
	}, handleListSessions)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "probe_backend",
		Description: "Probe a backend: spawn, check init, report capabilities",
	}, handleProbeBackend)
}
