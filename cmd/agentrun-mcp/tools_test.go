//go:build !windows

package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmora/agentrun"
)

// ---------------------------------------------------------------------------
// parse_line tests
// ---------------------------------------------------------------------------

func TestParseLine_Claude(t *testing.T) {
	line := `{"type":"assistant","message":{"type":"text","text":"hello"}}`
	backend, err := newCLIBackend(backendClaude)
	if err != nil {
		t.Fatalf("newCLIBackend: %v", err)
	}
	msg, err := backend.ParseLine(line)
	if err != nil {
		t.Fatalf("ParseLine: %v", err)
	}
	if msg.Type != agentrun.MessageText {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageText)
	}
}

func TestParseLine_UnknownBackend(t *testing.T) {
	_, err := newCLIBackend("unknown")
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

// ---------------------------------------------------------------------------
// explain_session tests
// ---------------------------------------------------------------------------

func TestExplainSession_StructuredOutput(t *testing.T) {
	session := agentrun.Session{
		CWD:    "/tmp",
		Model:  "test-model",
		Prompt: "example",
	}

	backends := []struct {
		name    string
		backend func() interface {
			SpawnArgs(agentrun.Session) (string, []string)
		}
	}{
		{backendClaude, func() interface {
			SpawnArgs(agentrun.Session) (string, []string)
		} {
			b, _ := newCLIBackend(backendClaude)
			return b
		}},
	}

	for _, bd := range backends {
		t.Run(bd.name, func(t *testing.T) {
			b := bd.backend()
			bin, args := b.SpawnArgs(session)
			if bin == "" {
				t.Error("binary should not be empty")
			}
			if len(args) == 0 {
				t.Error("args should not be empty")
			}
			_ = args
		})
	}
}

func TestExplainSession_ValidatesOptions(t *testing.T) {
	_, _, err := handleExplainSession(context.Background(), nil, explainSessionInput{
		Options: map[string]string{"mode": "act"},
	})
	if err == nil {
		t.Fatal("expected error for blocked option")
	}
}

func TestExplainSession_ValidatesEnv(t *testing.T) {
	_, _, err := handleExplainSession(context.Background(), nil, explainSessionInput{
		Env: map[string]string{"PATH": "/bin"},
	})
	if err == nil {
		t.Fatal("expected error for blocked env var")
	}
}

// ---------------------------------------------------------------------------
// makeEngine tests
// ---------------------------------------------------------------------------

func TestMakeEngine_ValidBackends(t *testing.T) {
	tests := []struct {
		name         string
		spawnPerTurn bool
	}{
		{backendClaude, false},
		{backendCodex, true},
		{backendOpenCode, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, spt, err := makeEngine(tt.name, nil, "", nil)
			if err != nil {
				t.Fatalf("makeEngine: %v", err)
			}
			if engine == nil {
				t.Fatal("engine should not be nil")
			}
			if spt != tt.spawnPerTurn {
				t.Errorf("spawnPerTurn = %v, want %v", spt, tt.spawnPerTurn)
			}
		})
	}
}

func TestMakeEngine_ACP_NoBinary(t *testing.T) {
	_, _, err := makeEngine(backendACP, nil, "", nil)
	if err == nil {
		t.Fatal("expected error when ACP binary is empty")
	}
}

func TestMakeEngine_ACP_WithBinary(t *testing.T) {
	engine, spt, err := makeEngine(backendACP, nil, "/usr/bin/echo", nil)
	if err != nil {
		t.Fatalf("makeEngine: %v", err)
	}
	if engine == nil {
		t.Fatal("engine should not be nil")
	}
	if spt {
		t.Error("ACP should not be spawn-per-turn")
	}
}

func TestMakeEngine_Unknown(t *testing.T) {
	_, _, err := makeEngine("invalid", nil, "", nil)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

// ---------------------------------------------------------------------------
// list_backends tests
// ---------------------------------------------------------------------------

func TestListBackends_CapabilityDetection(t *testing.T) {
	// Claude should have streamer + input_formatter.
	caps := detectCapabilities(backendClaude)
	has := func(want string) bool {
		for _, c := range caps {
			if c == want {
				return true
			}
		}
		return false
	}
	if !has("streamer") {
		t.Error("claude should have streamer capability")
	}
	if !has("input_formatter") {
		t.Error("claude should have input_formatter capability")
	}

	// Codex should have resumer.
	codexCaps := detectCapabilities(backendCodex)
	hasCodex := func(want string) bool {
		for _, c := range codexCaps {
			if c == want {
				return true
			}
		}
		return false
	}
	if !hasCodex("resumer") {
		t.Error("codex should have resumer capability")
	}
}

// ---------------------------------------------------------------------------
// drainSpawnPerTurn tests
// ---------------------------------------------------------------------------

func TestDrainSpawnPerTurn_ChannelClose(t *testing.T) {
	ch := make(chan agentrun.Message, 3)
	ch <- agentrun.Message{Type: agentrun.MessageText, Content: "a"}
	ch <- agentrun.Message{Type: agentrun.MessageResult}
	close(ch)

	var msgs []agentrun.Message
	proc := &fakeProcess{output: ch}
	drainSpawnPerTurn(t.Context(), proc, func(msg agentrun.Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// buildRunSession tests
// ---------------------------------------------------------------------------

func TestBuildRunSession_ValidatesEnv(t *testing.T) {
	workspaceRoot = t.TempDir()
	_, err := buildRunSession(runTurnInput{
		Backend: backendClaude,
		Prompt:  "test",
		Env:     map[string]string{"PATH": "/usr/bin"},
	})
	if err == nil {
		t.Fatal("expected error for blocked env var")
	}
}

func TestBuildRunSession_ValidatesOptions(t *testing.T) {
	workspaceRoot = t.TempDir()
	_, err := buildRunSession(runTurnInput{
		Backend: backendClaude,
		Prompt:  "test",
		Options: map[string]string{"mode": "act"},
	})
	if err == nil {
		t.Fatal("expected error for blocked option")
	}
}

func TestBuildRunSession_ForcesHITLAndMode(t *testing.T) {
	workspaceRoot = t.TempDir()
	session, err := buildRunSession(runTurnInput{
		Backend: backendClaude,
		Prompt:  "test",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.Options[agentrun.OptionHITL] != string(agentrun.HITLOn) {
		t.Error("HITL should be forced to 'on'")
	}
	if session.Options[agentrun.OptionMode] != string(agentrun.ModePlan) {
		t.Error("Mode should be forced to 'plan'")
	}
}

// ---------------------------------------------------------------------------
// collectTerminalState tests
// ---------------------------------------------------------------------------

func TestCollectTerminalState_NoError(t *testing.T) {
	ch := make(chan agentrun.Message)
	close(ch)
	proc := &fakeProcess{output: ch}
	msgs := []agentrun.Message{{Type: agentrun.MessageText, Content: "hi"}}
	stderrW := &cappedWriter{max: 1024}
	out := collectTerminalState(proc, msgs, time.Now(), stderrW)
	if out.Error != "" {
		t.Errorf("expected no error, got %q", out.Error)
	}
	if len(out.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(out.Messages))
	}
}

// ---------------------------------------------------------------------------
// applyProbeInit tests
// ---------------------------------------------------------------------------

func TestApplyProbeInit_SetsFields(t *testing.T) {
	out := &probeOutput{}
	applyProbeInit(agentrun.Message{
		Type:     agentrun.MessageInit,
		ResumeID: "ses_abc123",
		Init:     &agentrun.InitMeta{Model: "test-model"},
		Process:  &agentrun.ProcessMeta{PID: 42, Binary: "/usr/bin/test"},
	}, out)
	if !out.EmitsInit {
		t.Error("EmitsInit should be true")
	}
	if out.ResumeID != "ses_abc123" {
		t.Errorf("ResumeID = %q, want ses_abc123", out.ResumeID)
	}
	if out.Binary != "/usr/bin/test" {
		t.Errorf("Binary = %q, want /usr/bin/test", out.Binary)
	}
}

func TestApplyProbeInit_IgnoresNonInit(t *testing.T) {
	out := &probeOutput{}
	applyProbeInit(agentrun.Message{Type: agentrun.MessageText, Content: "hi"}, out)
	if out.EmitsInit {
		t.Error("EmitsInit should be false for non-init message")
	}
}

// ---------------------------------------------------------------------------
// session_start tests
// ---------------------------------------------------------------------------

func TestSessionStart_ValidatesEnv(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)
	workspaceRoot = t.TempDir()

	_, _, err := handleSessionStart(context.Background(), nil, sessionStartInput{
		Backend: backendClaude,
		Prompt:  "test",
		Env:     map[string]string{"PATH": "/usr/bin"},
	})
	if err == nil {
		t.Fatal("expected error for blocked env var")
	}
}

func TestSessionStart_ValidatesOptions(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)
	workspaceRoot = t.TempDir()

	_, _, err := handleSessionStart(context.Background(), nil, sessionStartInput{
		Backend: backendClaude,
		Prompt:  "test",
		Options: map[string]string{"mode": "act"},
	})
	if err == nil {
		t.Fatal("expected error for blocked option")
	}
}

func TestSessionStart_AtCapacity(t *testing.T) {
	sessions = newSessionStore(1)
	workspaceRoot = t.TempDir()

	// Fill the store.
	id, _ := generateSessionID()
	sessions.create(&sessionEntry{
		id:           id,
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	})

	// doSessionStart should fail at capacity (after building session).
	// We can't call the full handler without a real engine, so test the
	// store capacity check directly.
	err := sessions.create(&sessionEntry{
		id:           "mcp_overflow",
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	})
	if err == nil {
		t.Fatal("expected capacity error")
	}
}

// ---------------------------------------------------------------------------
// session_send tests
// ---------------------------------------------------------------------------

func TestSessionSend_NotFound(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	_, _, err := handleSessionSend(context.Background(), nil, sessionSendInput{
		SessionID: "mcp_nonexistent0000000000000000",
		Message:   "hello",
	})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestSessionSend_MessageSizeCap(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	oversized := make([]byte, maxMessageBytes+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	_, _, err := handleSessionSend(context.Background(), nil, sessionSendInput{
		SessionID: "mcp_test",
		Message:   string(oversized),
	})
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
}

func TestSessionSend_TerminatedSession(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	ch := make(chan agentrun.Message, 1)
	close(ch) // simulate ended output
	proc := &fakeProcess{
		output: ch,
		err:    fmt.Errorf("process exited"),
	}

	id, _ := generateSessionID()
	sessions.create(&sessionEntry{
		id:           id,
		backend:      backendClaude,
		stderrW:      &cappedWriter{max: 1024},
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         proc,
	})

	out, err := doSessionSend(context.Background(), sessionSendInput{
		SessionID: id,
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("doSessionSend: %v", err)
	}
	if out.Error == "" {
		t.Fatal("expected error in output for terminated session")
	}

	// Session should be auto-removed.
	if _, ok := sessions.get(id); ok {
		t.Fatal("terminated session should be auto-removed")
	}
}

// ---------------------------------------------------------------------------
// session_stop tests
// ---------------------------------------------------------------------------

func TestSessionStop_NotFound(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	_, _, err := handleSessionStop(context.Background(), nil, sessionStopInput{
		SessionID: "mcp_nonexistent0000000000000000",
	})
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestSessionStop_Success(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	proc := &fakeProcess{output: make(chan agentrun.Message)}
	id, _ := generateSessionID()
	sessions.create(&sessionEntry{
		id:           id,
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         proc,
	})

	_, out, err := handleSessionStop(context.Background(), nil, sessionStopInput{
		SessionID: id,
	})
	if err != nil {
		t.Fatalf("handleSessionStop: %v", err)
	}
	stopOut, ok := out.(*sessionStopOutput)
	if !ok {
		t.Fatalf("unexpected output type: %T", out)
	}
	if !stopOut.Stopped {
		t.Error("Stopped should be true")
	}
	if proc.stopCount() == 0 {
		t.Error("proc.Stop should have been called")
	}
	if _, ok := sessions.get(id); ok {
		t.Fatal("session should be removed after stop")
	}
}

// ---------------------------------------------------------------------------
// session lifecycle tests
// ---------------------------------------------------------------------------

func TestSessionLifecycle(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	// Create a session entry manually (can't call doSessionStart without real engine).
	ch := make(chan agentrun.Message, 10)
	proc := &fakeProcess{
		output: ch,
		sendFunc: func(_ context.Context, _ string) error {
			// Simulate agent response on send.
			ch <- agentrun.Message{Type: agentrun.MessageText, Content: "response"}
			ch <- agentrun.Message{Type: agentrun.MessageResult}
			close(ch)
			return nil
		},
	}

	id, _ := generateSessionID()
	sessions.create(&sessionEntry{
		id:           id,
		backend:      backendClaude,
		spawnPerTurn: true,
		stderrW:      &cappedWriter{max: 1024},
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         proc,
	})

	// Send to the session (spawn-per-turn path).
	out, err := doSessionSend(context.Background(), sessionSendInput{
		SessionID: id,
		Message:   "follow-up",
	})
	if err != nil {
		t.Fatalf("doSessionSend: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Errorf("got %d messages, want 2", len(out.Messages))
	}

	// Stop the session.
	_, _, err = handleSessionStop(context.Background(), nil, sessionStopInput{
		SessionID: id,
	})
	if err != nil {
		t.Fatalf("handleSessionStop: %v", err)
	}
}

func TestSessionSend_SpawnPerTurn(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	ch := make(chan agentrun.Message, 10)
	proc := &fakeProcess{
		output: ch,
		sendFunc: func(_ context.Context, _ string) error {
			ch <- agentrun.Message{Type: agentrun.MessageText, Content: "reply"}
			ch <- agentrun.Message{Type: agentrun.MessageResult}
			close(ch)
			return nil
		},
	}

	id, _ := generateSessionID()
	sessions.create(&sessionEntry{
		id:           id,
		backend:      backendCodex,
		spawnPerTurn: true,
		stderrW:      &cappedWriter{max: 1024},
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         proc,
	})

	out, err := doSessionSend(context.Background(), sessionSendInput{
		SessionID: id,
		Message:   "test",
	})
	if err != nil {
		t.Fatalf("doSessionSend: %v", err)
	}
	if len(out.Messages) == 0 {
		t.Error("expected messages from spawn-per-turn send")
	}
	if out.Messages[0].Content != "reply" {
		t.Errorf("content = %q, want %q", out.Messages[0].Content, "reply")
	}
}

// ---------------------------------------------------------------------------
// list_sessions tests
// ---------------------------------------------------------------------------

func TestListSessions_ReturnsSnapshot(t *testing.T) {
	sessions = newSessionStore(maxLiveSessions)

	id, _ := generateSessionID()
	sessions.create(&sessionEntry{
		id:           id,
		backend:      backendClaude,
		createdAt:    time.Now(),
		lastActivity: time.Now(),
		proc:         &fakeProcess{output: make(chan agentrun.Message)},
	})

	_, out, err := handleListSessions(context.Background(), nil, listSessionsInput{})
	if err != nil {
		t.Fatalf("handleListSessions: %v", err)
	}
	wrapper, ok := out.(listSessionsOutput)
	if !ok {
		t.Fatalf("unexpected output type: %T", out)
	}
	if len(wrapper.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(wrapper.Sessions))
	}
	// Full ID should not be exposed.
	if len(wrapper.Sessions[0].IDPrefix) >= len(id) {
		t.Errorf("IDPrefix should be truncated, got %q", wrapper.Sessions[0].IDPrefix)
	}
}

// fakeProcess implements agentrun.Process for testing.
type fakeProcess struct {
	output     chan agentrun.Message
	sendFunc   func(ctx context.Context, msg string) error // nil → no-op
	stopCalled int32                                       // atomic counter
	err        error                                       // configurable Err() return
}

func (p *fakeProcess) Output() <-chan agentrun.Message { return p.output }

func (p *fakeProcess) Send(ctx context.Context, msg string) error {
	if p.sendFunc != nil {
		return p.sendFunc(ctx, msg)
	}
	return nil
}

func (p *fakeProcess) Stop(_ context.Context) error {
	atomic.AddInt32(&p.stopCalled, 1)
	return nil
}

func (p *fakeProcess) stopCount() int32 {
	return atomic.LoadInt32(&p.stopCalled)
}

func (p *fakeProcess) Wait() error { return nil }

func (p *fakeProcess) Err() error { return p.err }
