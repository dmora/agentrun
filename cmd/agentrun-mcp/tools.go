//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
	"github.com/dmora/agentrun/engine/cli/codex"
	"github.com/dmora/agentrun/engine/cli/opencode"
)

// sem limits concurrent tool calls.
var sem = make(chan struct{}, 5)

const (
	maxTimeout = 300

	backendClaude   = "claude"
	backendCodex    = "codex"
	backendOpenCode = "opencode"
	backendACP      = "acp"
)

// acquireSem acquires the concurrency semaphore or returns ctx.Err().
func acquireSem(ctx context.Context) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseSem() { <-sem }

// clampTimeout applies default (60s) and cap (300s) to a timeout value.
func clampTimeout(timeout int) time.Duration {
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	return time.Duration(timeout) * time.Second
}

// newCLIBackend creates a fresh CLI backend by name.
func newCLIBackend(name string) (cli.Backend, error) {
	switch name {
	case backendClaude:
		return claude.New(), nil
	case backendCodex:
		return codex.New(), nil
	case backendOpenCode:
		return opencode.New(), nil
	default:
		return nil, fmt.Errorf("unknown CLI backend %q (valid: %s, %s, %s)", name, backendClaude, backendCodex, backendOpenCode)
	}
}

// stopProcess is a deferred helper for clean process shutdown.
func stopProcess(proc agentrun.Process) {
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = proc.Stop(stopCtx)
}

// --- Tool 1: list_message_types ---

type listMessageTypesInput struct{}

type messageTypeEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func handleListMessageTypes(_ context.Context, _ *mcp.CallToolRequest, _ listMessageTypesInput) (*mcp.CallToolResult, any, error) {
	catalog := []messageTypeEntry{
		{"text", "Complete text content from the agent"},
		{"tool_use", "Agent is invoking a tool"},
		{"tool_result", "Result of a tool invocation"},
		{"error", "Error from the agent or engine"},
		{"system", "System-level message"},
		{"init", "Session initialization (contains ResumeID, InitMeta, ProcessMeta)"},
		{"result", "Turn complete (contains Usage, StopReason, Denials)"},
		{"context_window", "Mid-turn context window fill state"},
		{"eof", "End of stream marker"},
		{"thinking", "Complete thinking/reasoning content"},
		{"text_delta", "Streaming text token"},
		{"tool_use_delta", "Streaming tool use token"},
		{"thinking_delta", "Streaming thinking token"},
	}
	return nil, struct {
		Types []messageTypeEntry `json:"types"`
	}{catalog}, nil
}

// --- Tool 2: list_backends ---

type listBackendsInput struct{}

type backendInfo struct {
	Name         string   `json:"name"`
	Path         string   `json:"path,omitempty"`
	Available    bool     `json:"available"`
	Capabilities []string `json:"capabilities"`
}

func handleListBackends(_ context.Context, _ *mcp.CallToolRequest, _ listBackendsInput) (*mcp.CallToolResult, any, error) {
	slog.Info("listing backends")

	results := make([]backendInfo, 0, len(validBackends))
	for _, name := range validBackends {
		info := backendInfo{Name: name}
		if name == backendACP {
			info.Capabilities = []string{"persistent"}
			results = append(results, info)
			continue
		}

		path, err := exec.LookPath(name)
		if err == nil {
			info.Available = true
			info.Path = path
		}
		info.Capabilities = detectCapabilities(name)
		results = append(results, info)
	}
	return nil, struct {
		Backends []backendInfo `json:"backends"`
	}{results}, nil
}

func detectCapabilities(name string) []string {
	backend, err := newCLIBackend(name)
	if err != nil {
		return nil
	}
	var caps []string
	if _, ok := backend.(cli.Resumer); ok {
		caps = append(caps, "resumer")
	}
	if _, ok := backend.(cli.Streamer); ok {
		caps = append(caps, "streamer")
	}
	if _, ok := backend.(cli.InputFormatter); ok {
		caps = append(caps, "input_formatter")
	}
	return caps
}

// --- Tool 3: parse_line ---

type parseLineInput struct {
	Backend string `json:"backend" jsonschema:"Backend name (claude or codex or opencode)"`
	Line    string `json:"line" jsonschema:"Raw JSONL line to parse"`
}

func handleParseLine(_ context.Context, _ *mcp.CallToolRequest, input parseLineInput) (*mcp.CallToolResult, any, error) {
	slog.Info("parsing line", "backend", input.Backend)

	backend, err := newCLIBackend(input.Backend)
	if err != nil {
		return nil, nil, err
	}
	msg, parseErr := backend.ParseLine(input.Line)
	if parseErr != nil {
		return nil, nil, fmt.Errorf("parse error: %w", parseErr)
	}
	return nil, msg, nil
}

// --- Tool 4: explain_session ---

type explainSessionInput struct {
	Model   string            `json:"model,omitempty" jsonschema:"Model override"`
	CWD     string            `json:"cwd,omitempty" jsonschema:"Working directory"`
	Options map[string]string `json:"options,omitempty" jsonschema:"Session options"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"Environment variables"`
}

type explainOutput struct {
	Backend    string          `json:"backend"`
	SpawnArgs  json.RawMessage `json:"spawn_args,omitempty"`
	ResumeArgs json.RawMessage `json:"resume_args,omitempty"`
	StreamArgs json.RawMessage `json:"stream_args,omitempty"`
}

type argsDetail struct {
	Binary string   `json:"binary"`
	Args   []string `json:"args"`
}

func handleExplainSession(_ context.Context, _ *mcp.CallToolRequest, input explainSessionInput) (*mcp.CallToolResult, any, error) {
	slog.Info("explaining session")

	if err := validateEnvKeys(input.Env); err != nil {
		return nil, nil, err
	}
	if err := validateOptions(input.Options); err != nil {
		return nil, nil, err
	}

	session := agentrun.Session{
		CWD:     input.CWD,
		Model:   input.Model,
		Prompt:  "example prompt",
		Options: input.Options,
		Env:     input.Env,
	}

	results := make([]explainOutput, 0, len(cliBackendNames))
	for _, name := range cliBackendNames {
		backend, err := newCLIBackend(name)
		if err != nil {
			continue
		}
		results = append(results, explainBackend(name, backend, session))
	}
	return nil, struct {
		Backends []explainOutput `json:"backends"`
	}{results}, nil
}

func explainBackend(name string, backend cli.Backend, session agentrun.Session) explainOutput {
	out := explainOutput{Backend: name}

	bin, args := backend.SpawnArgs(session)
	out.SpawnArgs = mustJSON(argsDetail{Binary: bin, Args: args})

	if r, ok := backend.(cli.Resumer); ok {
		rBin, rArgs, err := r.ResumeArgs(session, "continue")
		if err == nil {
			out.ResumeArgs = mustJSON(argsDetail{Binary: rBin, Args: rArgs})
		}
	}
	if s, ok := backend.(cli.Streamer); ok {
		sBin, sArgs := s.StreamArgs(session)
		out.StreamArgs = mustJSON(argsDetail{Binary: sBin, Args: sArgs})
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// --- Tool 5: run_turn ---

type runTurnInput struct {
	Backend string            `json:"backend" jsonschema:"Backend name (claude or codex or opencode or acp)"`
	Prompt  string            `json:"prompt" jsonschema:"User prompt"`
	Model   string            `json:"model,omitempty" jsonschema:"Model override"`
	CWD     string            `json:"cwd,omitempty" jsonschema:"Working directory (must be under workspace)"`
	Options map[string]string `json:"options,omitempty" jsonschema:"Session options (allowlisted subset)"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"Environment variables (blocklisted keys rejected)"`
	Timeout int               `json:"timeout,omitempty" jsonschema:"Timeout in seconds (default 60; max 300)"`
}

type runTurnOutput struct {
	Messages []agentrun.Message `json:"messages"`
	ExitCode *int               `json:"exit_code,omitempty"`
	Stderr   string             `json:"stderr,omitempty"`
	Duration string             `json:"duration"`
	Error    string             `json:"error,omitempty"`
}

func handleRunTurn(ctx context.Context, _ *mcp.CallToolRequest, input runTurnInput) (*mcp.CallToolResult, any, error) {
	if err := acquireSem(ctx); err != nil {
		return nil, nil, err
	}
	defer releaseSem()

	ctx, cancel := context.WithTimeout(ctx, clampTimeout(input.Timeout))
	defer cancel()

	out, err := doRunTurn(ctx, input)
	if err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func buildRunSession(input runTurnInput) (agentrun.Session, error) {
	if len(input.Prompt) > maxMessageBytes {
		return agentrun.Session{}, fmt.Errorf("prompt too large (%d bytes, max %d)", len(input.Prompt), maxMessageBytes)
	}
	cwd, err := validateCWD(input.CWD, workspaceRoot)
	if err != nil {
		return agentrun.Session{}, err
	}
	if err := validateEnvKeys(input.Env); err != nil {
		return agentrun.Session{}, err
	}
	if err := validateOptions(input.Options); err != nil {
		return agentrun.Session{}, err
	}

	opts := make(map[string]string)
	for k, v := range input.Options {
		opts[k] = v
	}
	opts[agentrun.OptionHITL] = string(agentrun.HITLOn)
	opts[agentrun.OptionMode] = string(agentrun.ModePlan)

	return agentrun.Session{
		CWD:     cwd,
		Model:   input.Model,
		Prompt:  input.Prompt,
		Options: opts,
		Env:     input.Env,
	}, nil
}

func collectTerminalState(proc agentrun.Process, messages []agentrun.Message, start time.Time, stderrW *cappedWriter) *runTurnOutput {
	out := &runTurnOutput{
		Messages: messages,
		Duration: time.Since(start).Round(time.Millisecond).String(),
		Stderr:   scrubStderr(stderrW.Bytes(), 2048),
	}
	if termErr := proc.Err(); termErr != nil {
		out.Error = termErr.Error()
		if code, ok := agentrun.ExitCode(termErr); ok {
			out.ExitCode = &code
		}
	}
	return out
}

func doRunTurn(ctx context.Context, input runTurnInput) (*runTurnOutput, error) {
	log := slog.With("tool", "run_turn", "backend", input.Backend)
	log.Info("starting run_turn")
	start := time.Now()

	session, err := buildRunSession(input)
	if err != nil {
		return nil, err
	}

	stderrW := &cappedWriter{max: 1 << 20}
	engine, err := makeEngine(input.Backend, stderrW, acpBinary, []string(acpArgs))
	if err != nil {
		return nil, err
	}
	if err := engine.Validate(); err != nil {
		return nil, fmt.Errorf("engine unavailable: %w", err)
	}

	proc, err := engine.Start(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	defer stopProcess(proc)

	var messages []agentrun.Message
	handler := func(msg agentrun.Message) error {
		messages = append(messages, msg)
		return nil
	}

	turnErr := agentrun.RunTurn(ctx, proc, input.Prompt, handler)
	if turnErr != nil {
		log.Warn("RunTurn error", "error", turnErr)
	}

	out := collectTerminalState(proc, messages, start, stderrW)
	if turnErr != nil && out.Error == "" {
		out.Error = turnErr.Error()
	}
	if out.Error != "" {
		log.Warn("process error", "error", out.Error)
	}
	log.Info("run_turn complete", "messages", len(messages), "duration", out.Duration)
	return out, nil
}

// --- Tool 6: session_start ---

type sessionStartInput struct {
	Backend string            `json:"backend" jsonschema:"Backend name (claude or codex or opencode or acp)"`
	Prompt  string            `json:"prompt" jsonschema:"Initial prompt"`
	Model   string            `json:"model,omitempty" jsonschema:"Model override"`
	CWD     string            `json:"cwd,omitempty" jsonschema:"Working directory (must be under workspace)"`
	Options map[string]string `json:"options,omitempty" jsonschema:"Session options (allowlisted subset)"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"Environment variables (blocklisted keys rejected)"`
	Timeout int               `json:"timeout,omitempty" jsonschema:"Timeout in seconds (default 60; max 300)"`
}

type sessionStartOutput struct {
	SessionID string             `json:"session_id"`
	Messages  []agentrun.Message `json:"messages"`
	Duration  string             `json:"duration"`
	Stderr    string             `json:"stderr,omitempty"`
	Error     string             `json:"error,omitempty"`
}

func handleSessionStart(ctx context.Context, _ *mcp.CallToolRequest, input sessionStartInput) (*mcp.CallToolResult, any, error) {
	if err := acquireSem(ctx); err != nil {
		return nil, nil, err
	}
	defer releaseSem()

	ctx, cancel := context.WithTimeout(ctx, clampTimeout(input.Timeout))
	defer cancel()

	out, err := doSessionStart(ctx, input)
	if err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func doSessionStart(ctx context.Context, input sessionStartInput) (*sessionStartOutput, error) {
	log := slog.With("tool", "session_start", "backend", input.Backend)
	log.Info("starting session")
	start := time.Now()

	rtInput := runTurnInput{
		Backend: input.Backend, Prompt: input.Prompt, Model: input.Model,
		CWD: input.CWD, Options: input.Options, Env: input.Env,
	}
	session, err := buildRunSession(rtInput)
	if err != nil {
		return nil, err
	}

	stderrW := &cappedWriter{max: 1 << 20}
	engine, err := makeEngine(input.Backend, stderrW, acpBinary, []string(acpArgs))
	if err != nil {
		return nil, err
	}
	if err := engine.Validate(); err != nil {
		return nil, fmt.Errorf("engine unavailable: %w", err)
	}

	proc, err := engine.Start(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	var messages []agentrun.Message
	handler := func(msg agentrun.Message) error {
		messages = append(messages, msg)
		return nil
	}
	turnErr := agentrun.RunTurn(ctx, proc, input.Prompt, handler)
	if turnErr != nil {
		log.Warn("first turn error", "error", turnErr)
	}

	duration := time.Since(start).Round(time.Millisecond).String()
	stderr := scrubStderr(stderrW.Bytes(), 2048)
	if proc.Err() != nil {
		stopProcess(proc)
		out := &sessionStartOutput{
			Messages: messages, Duration: duration,
			Stderr: stderr, Error: proc.Err().Error(),
		}
		if turnErr != nil && out.Error == "" {
			out.Error = turnErr.Error()
		}
		return out, nil
	}

	sessionID, err := registerSession(proc, input.Backend, stderrW)
	if err != nil {
		stopProcess(proc)
		return nil, err
	}
	log.Info("session created", "session", maskID(sessionID), "duration", duration)
	out := &sessionStartOutput{
		SessionID: sessionID, Messages: messages,
		Duration: duration, Stderr: stderr,
	}
	if turnErr != nil {
		out.Error = turnErr.Error()
	}
	return out, nil
}

// registerSession generates a session ID, builds the entry, and inserts it
// into the global session store. Returns the session ID on success.
func registerSession(proc agentrun.Process, backend string, stderrW *cappedWriter) (string, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return "", err
	}
	now := time.Now()
	entry := &sessionEntry{
		id:           sessionID,
		proc:         proc,
		backend:      backend,
		stderrW:      stderrW,
		createdAt:    now,
		lastActivity: now,
	}
	if err := sessions.create(entry); err != nil {
		return "", err
	}
	return sessionID, nil
}

// --- Tool 7: session_send ---

type sessionSendInput struct {
	SessionID string `json:"session_id" jsonschema:"Session ID from session_start"`
	Message   string `json:"message" jsonschema:"User message to send"`
	Timeout   int    `json:"timeout,omitempty" jsonschema:"Timeout in seconds (default 60; max 300)"`
}

type sessionSendOutput struct {
	Messages []agentrun.Message `json:"messages"`
	Duration string             `json:"duration"`
	Stderr   string             `json:"stderr,omitempty"`
	Error    string             `json:"error,omitempty"`
}

func handleSessionSend(ctx context.Context, _ *mcp.CallToolRequest, input sessionSendInput) (*mcp.CallToolResult, any, error) {
	// Validate message size before acquiring semaphore.
	if len(input.Message) > maxMessageBytes {
		return nil, nil, fmt.Errorf("message too large (%d bytes, max %d)", len(input.Message), maxMessageBytes)
	}

	if err := acquireSem(ctx); err != nil {
		return nil, nil, err
	}
	defer releaseSem()

	ctx, cancel := context.WithTimeout(ctx, clampTimeout(input.Timeout))
	defer cancel()

	out, err := doSessionSend(ctx, input)
	if err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func doSessionSend(ctx context.Context, input sessionSendInput) (*sessionSendOutput, error) {
	log := slog.With("tool", "session_send", "session", maskID(input.SessionID))
	log.Info("sending to session")
	start := time.Now()

	entry, ok := sessions.get(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session %q not found", maskID(input.SessionID))
	}

	// Serialize per-session sends.
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Track active turn.
	atomic.AddInt32(&entry.activeTurns, 1)
	defer atomic.AddInt32(&entry.activeTurns, -1)

	entry.lastActivity = time.Now()
	entry.stderrW.Reset()

	var messages []agentrun.Message
	handler := func(msg agentrun.Message) error {
		messages = append(messages, msg)
		return nil
	}

	turnErr := agentrun.RunTurn(ctx, entry.proc, input.Message, handler)

	stderr := scrubStderr(entry.stderrW.Bytes(), 2048)
	entry.lastActivity = time.Now()

	out := &sessionSendOutput{
		Messages: messages,
		Duration: time.Since(start).Round(time.Millisecond).String(),
		Stderr:   stderr,
	}
	if turnErr != nil {
		out.Error = turnErr.Error()
		log.Warn("turn error", "error", turnErr)
	}

	// If process died, auto-remove session.
	if entry.proc.Err() != nil {
		if out.Error == "" {
			out.Error = entry.proc.Err().Error()
		}
		sessions.remove(input.SessionID)
		stopProcess(entry.proc)
		log.Info("session auto-removed (process error)", "error", entry.proc.Err())
	}

	log.Info("send complete", "messages", len(messages), "duration", out.Duration)
	return out, nil
}

// --- Tool 8: session_stop ---

type sessionStopInput struct {
	SessionID string `json:"session_id" jsonschema:"Session ID to stop"`
}

type sessionStopOutput struct {
	Stopped bool `json:"stopped"`
}

func handleSessionStop(_ context.Context, _ *mcp.CallToolRequest, input sessionStopInput) (*mcp.CallToolResult, any, error) {
	log := slog.With("tool", "session_stop", "session", maskID(input.SessionID))

	entry, ok := sessions.remove(input.SessionID)
	if !ok {
		return nil, nil, fmt.Errorf("session %q not found", maskID(input.SessionID))
	}

	stopProcess(entry.proc)
	log.Info("session stopped")
	return nil, &sessionStopOutput{Stopped: true}, nil
}

// --- Tool 9: list_sessions ---

type listSessionsInput struct{}

type listSessionsOutput struct {
	Sessions []sessionInfo `json:"sessions"`
}

func handleListSessions(_ context.Context, _ *mcp.CallToolRequest, _ listSessionsInput) (*mcp.CallToolResult, any, error) {
	return nil, listSessionsOutput{sessions.list()}, nil
}

// --- Tool 10: probe_backend ---

type probeBackendInput struct {
	Backend string `json:"backend" jsonschema:"Backend name to probe"`
}

type probeOutput struct {
	Binary    string `json:"binary,omitempty"`
	SpawnsOK  bool   `json:"spawns_ok"`
	EmitsInit bool   `json:"emits_init"`
	InitMeta  any    `json:"init_meta,omitempty"`
	ResumeID  string `json:"resume_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

func handleProbeBackend(ctx context.Context, _ *mcp.CallToolRequest, input probeBackendInput) (*mcp.CallToolResult, any, error) {
	if err := acquireSem(ctx); err != nil {
		return nil, nil, err
	}
	defer releaseSem()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	out, err := doProbe(ctx, input.Backend)
	if err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

func doProbe(ctx context.Context, backend string) (*probeOutput, error) {
	log := slog.With("tool", "probe_backend", "backend", backend)
	log.Info("probing backend")

	stderrW := &cappedWriter{max: 1 << 20}
	engine, err := makeEngine(backend, stderrW, acpBinary, []string(acpArgs))
	if err != nil {
		return &probeOutput{Error: err.Error()}, nil
	}
	if err := engine.Validate(); err != nil {
		return &probeOutput{Error: err.Error()}, nil
	}

	proc, err := engine.Start(ctx, probeSession())
	if err != nil {
		return &probeOutput{Error: fmt.Sprintf("start: %v", err)}, nil
	}
	defer stopProcess(proc)

	out := &probeOutput{SpawnsOK: true}
	turnErr := agentrun.RunTurn(ctx, proc, "Say 'hello'.", func(msg agentrun.Message) error {
		applyProbeInit(msg, out)
		return nil
	})
	if turnErr != nil && out.Error == "" {
		out.Error = fmt.Sprintf("turn: %v", turnErr)
	}

	log.Info("probe complete", "spawns_ok", out.SpawnsOK, "emits_init", out.EmitsInit)
	return out, nil
}

func probeSession() agentrun.Session {
	return agentrun.Session{
		CWD:    workspaceRoot,
		Prompt: "Say 'hello'.",
		Options: map[string]string{
			agentrun.OptionHITL:     string(agentrun.HITLOn),
			agentrun.OptionMode:     string(agentrun.ModePlan),
			agentrun.OptionMaxTurns: "1",
		},
	}
}

func applyProbeInit(msg agentrun.Message, out *probeOutput) {
	if msg.Type != agentrun.MessageInit {
		return
	}
	out.EmitsInit = true
	out.ResumeID = msg.ResumeID
	if msg.Init != nil {
		out.InitMeta = msg.Init
	}
	if msg.Process != nil {
		out.Binary = msg.Process.Binary
	}
}
