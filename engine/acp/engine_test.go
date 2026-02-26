//go:build !windows

package acp_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/acp"
	"github.com/dmora/agentrun/filter"
)

var (
	mockBuildOnce  sync.Once
	mockBinaryPath string
	errMockBuild   error
)

const (
	integrationTimeout = 10 * time.Second
	mockTextContent    = "Hello world"
)

func buildMockBinary() {
	dir, err := os.MkdirTemp("", "mock-acp-*")
	if err != nil {
		errMockBuild = fmt.Errorf("tmpdir: %w", err)
		return
	}
	mockBinaryPath = filepath.Join(dir, "mock-acp")
	cmd := exec.Command("go", "build", "-o", mockBinaryPath, "./testdata/mock-acp/main.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		errMockBuild = fmt.Errorf("build mock: %w: %s", err, out)
		os.RemoveAll(dir)
	}
}

func mustBuild(t *testing.T) {
	t.Helper()
	mockBuildOnce.Do(buildMockBinary)
	if errMockBuild != nil {
		t.Fatalf("mock binary build failed: %v", errMockBuild)
	}
}

// writeScript creates an executable wrapper script that sets ACP_MOCK_MODE
// and execs the mock binary. Returns the script path.
func writeScript(t *testing.T, envMode string) string {
	t.Helper()
	mustBuild(t)
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "mock-acp-wrapper")
	script := fmt.Sprintf("#!/bin/sh\nexport ACP_MOCK_MODE=%s\nexec %s \"$@\"\n", envMode, mockBinaryPath)
	if err := os.WriteFile(wrapper, []byte(script), 0o600); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatalf("chmod wrapper: %v", err)
	}
	return wrapper
}

func newEngine(t *testing.T, opts ...acp.EngineOption) *acp.Engine {
	t.Helper()
	mustBuild(t)
	defaults := []acp.EngineOption{acp.WithBinary(mockBinaryPath)}
	return acp.NewEngine(append(defaults, opts...)...)
}

func startProc(t *testing.T) (agentrun.Process, context.Context) {
	t.Helper()
	engine := newEngine(t)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	t.Cleanup(cancel)

	session := agentrun.Session{CWD: t.TempDir()}
	proc, err := engine.Start(ctx, session)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })
	return proc, ctx
}

// collectUntilResult reads from ch until MessageResult or channel close.
// For persistent subprocesses, the channel stays open between turns.
func collectUntilResult(ch <-chan agentrun.Message) []agentrun.Message {
	var msgs []agentrun.Message
	for msg := range ch {
		msgs = append(msgs, msg)
		if msg.Type == agentrun.MessageResult {
			return msgs
		}
	}
	return msgs
}

func collectMessages(ch <-chan agentrun.Message) []agentrun.Message {
	var msgs []agentrun.Message
	for msg := range ch {
		msgs = append(msgs, msg)
	}
	return msgs
}

func concatContent(msgs []agentrun.Message, mt agentrun.MessageType) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Type == mt {
			b.WriteString(m.Content)
		}
	}
	return b.String()
}

// --- Tests ---

func TestEngine_Start_Handshake(t *testing.T) {
	proc, _ := startProc(t)

	msg, ok := <-proc.Output()
	if !ok {
		t.Fatal("output channel closed before MessageInit")
	}
	if msg.Type != agentrun.MessageInit {
		t.Errorf("first message type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	if msg.ResumeID == "" {
		t.Error("MessageInit.ResumeID (session ID) is empty")
	}
}

func TestEngine_Start_InitializeError(t *testing.T) {
	wrapper := writeScript(t, "init-error")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	_, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err == nil {
		t.Fatal("expected error from initialize")
	}
	if !strings.Contains(err.Error(), "initialize") {
		t.Errorf("error = %v, want to contain 'initialize'", err)
	}
}

func TestEngine_Start_HandshakeCrash(t *testing.T) {
	wrapper := writeScript(t, "handshake-crash")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	_, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err == nil {
		t.Fatal("expected error from handshake crash")
	}
}

func TestEngine_Send_StreamsUpdates(t *testing.T) {
	proc, ctx := startProc(t)

	// Drain init.
	<-proc.Output()

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := collectUntilResult(proc.Output())
	if len(msgs) == 0 {
		t.Fatal("no messages received after Send")
	}

	typeSet := make(map[agentrun.MessageType]bool)
	for _, m := range msgs {
		typeSet[m.Type] = true
	}
	// ACP streams delta-only — no completed aggregates (MessageThinking, MessageText).
	for _, want := range []agentrun.MessageType{
		agentrun.MessageThinkingDelta,
		agentrun.MessageTextDelta,
		agentrun.MessageToolUse,
		agentrun.MessageToolResult,
		agentrun.MessageResult,
	} {
		if !typeSet[want] {
			t.Errorf("missing message type %q", want)
		}
	}

	deltaText := concatContent(msgs, agentrun.MessageTextDelta)
	if deltaText != mockTextContent {
		t.Errorf("text deltas = %q, want %q", deltaText, mockTextContent)
	}

	thinkingDeltas := concatContent(msgs, agentrun.MessageThinkingDelta)
	if thinkingDeltas != "Let me think" {
		t.Errorf("thinking deltas = %q, want %q", thinkingDeltas, "Let me think")
	}
}

func TestEngine_Send_MultiTurn(t *testing.T) {
	proc, ctx := startProc(t)

	// Drain init.
	<-proc.Output()

	// Turn 1.
	if err := proc.Send(ctx, "turn 1"); err != nil {
		t.Fatalf("send turn 1: %v", err)
	}
	msgs1 := collectUntilResult(proc.Output())

	// Turn 2.
	if err := proc.Send(ctx, "turn 2"); err != nil {
		t.Fatalf("send turn 2: %v", err)
	}
	msgs2 := collectUntilResult(proc.Output())

	hasResult := func(msgs []agentrun.Message) bool {
		for _, m := range msgs {
			if m.Type == agentrun.MessageResult {
				return true
			}
		}
		return false
	}
	if !hasResult(msgs1) {
		t.Error("turn 1 missing MessageResult")
	}
	if !hasResult(msgs2) {
		t.Error("turn 2 missing MessageResult")
	}
}

func TestEngine_Stop_Graceful(t *testing.T) {
	proc, _ := startProc(t)
	<-proc.Output()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := proc.Stop(ctx)
	if err != nil && !errors.Is(err, agentrun.ErrTerminated) {
		t.Errorf("Stop: %v", err)
	}
}

func TestEngine_CompletedFilter(t *testing.T) {
	proc, ctx := startProc(t)
	<-proc.Output()

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Stop the process so the filtered channel closes.
	_ = proc.Stop(context.Background())

	msgs := collectMessages(filter.Completed(ctx, proc.Output()))
	for _, m := range msgs {
		if filter.IsDelta(m.Type) {
			t.Errorf("delta message %q should not pass Completed filter", m.Type)
		}
	}

	// ACP streams delta-only — Completed filter drops text/thinking content.
	// Only non-delta types pass through: MessageToolUse, MessageToolResult,
	// MessageSystem, MessageResult.
	typeSet := make(map[agentrun.MessageType]bool)
	for _, m := range msgs {
		typeSet[m.Type] = true
	}
	for _, want := range []agentrun.MessageType{
		agentrun.MessageToolUse,
		agentrun.MessageToolResult,
		agentrun.MessageResult,
	} {
		if !typeSet[want] {
			t.Errorf("missing %q in Completed output", want)
		}
	}
}

func TestEngine_ResumeID_SessionLoad(t *testing.T) {
	engine := newEngine(t)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD: t.TempDir(),
		Options: map[string]string{
			agentrun.OptionResumeID: "existing-session-123",
		},
	}

	proc, err := engine.Start(ctx, session)
	if err != nil {
		t.Fatalf("start with resume: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	msg := <-proc.Output()
	if msg.Type != agentrun.MessageInit {
		t.Errorf("type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	// LoadSessionResult has no sessionId — uses resumeID directly.
	if msg.ResumeID != "existing-session-123" {
		t.Errorf("session ID = %q, want %q", msg.ResumeID, "existing-session-123")
	}
}

func TestEngine_ResumeID_SessionNotFound(t *testing.T) {
	wrapper := writeScript(t, "session-not-found")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD: t.TempDir(),
		Options: map[string]string{
			agentrun.OptionResumeID: "nonexistent-session",
		},
	}

	_, err := engine.Start(ctx, session)
	if err == nil {
		t.Fatal("expected error for session-not-found")
	}
	if !errors.Is(err, agentrun.ErrSessionNotFound) {
		t.Errorf("error = %v, want ErrSessionNotFound", err)
	}
}

func TestEngine_ResumeID_InvalidFormat(t *testing.T) {
	engine := newEngine(t)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD: t.TempDir(),
		Options: map[string]string{
			agentrun.OptionResumeID: "invalid session!!",
		},
	}

	_, err := engine.Start(ctx, session)
	if err == nil {
		t.Fatal("expected error for invalid resume ID")
	}
}

func TestEngine_CWDPassthrough(t *testing.T) {
	wrapper := writeScript(t, "echo-cwd")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	cwd := t.TempDir()
	proc, err := engine.Start(ctx, agentrun.Session{CWD: cwd})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	msg := <-proc.Output()
	if msg.Type != agentrun.MessageInit {
		t.Fatalf("type = %q, want %q", msg.Type, agentrun.MessageInit)
	}
	// Session ID should contain sanitized CWD.
	if !strings.HasPrefix(msg.ResumeID, "cwd-") {
		t.Errorf("session ID = %q, want prefix %q", msg.ResumeID, "cwd-")
	}
}

func TestEngine_SetMode_Fatal(t *testing.T) {
	wrapper := writeScript(t, "set-mode-fail")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD: t.TempDir(),
		Options: map[string]string{
			agentrun.OptionMode: "plan",
		},
	}

	_, err := engine.Start(ctx, session)
	if err == nil {
		t.Fatal("expected error from set_mode failure")
	}
	if !strings.Contains(err.Error(), "set_mode") {
		t.Errorf("error = %v, want to contain 'set_mode'", err)
	}
}

func TestEngine_SetConfigOption_NonFatal(t *testing.T) {
	wrapper := writeScript(t, "set-config-fail")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD:   t.TempDir(),
		Model: "some-model",
	}

	proc, err := engine.Start(ctx, session)
	if err != nil {
		t.Fatalf("start should succeed despite config fail: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	// Should get MessageInit followed by MessageError about config option.
	msg := <-proc.Output()
	if msg.Type != agentrun.MessageInit {
		t.Fatalf("first message type = %q, want %q", msg.Type, agentrun.MessageInit)
	}

	// The MessageError about config option failure may be next.
	msg2 := <-proc.Output()
	if msg2.Type != agentrun.MessageError {
		t.Errorf("second message type = %q, want %q", msg2.Type, agentrun.MessageError)
	}
	if !strings.Contains(msg2.Content, "set_config_option") {
		t.Errorf("error content = %q, want to contain 'set_config_option'", msg2.Content)
	}
}

func TestEngine_Permission_HITLOff(t *testing.T) {
	wrapper := writeScript(t, "permission")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD: t.TempDir(),
		Options: map[string]string{
			agentrun.OptionHITL: string(agentrun.HITLOff),
		},
	}
	proc, err := engine.Start(ctx, session)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	<-proc.Output() // drain init

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := collectUntilResult(proc.Output())
	hasResult := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageResult {
			hasResult = true
		}
	}
	if !hasResult {
		t.Error("expected MessageResult after permission flow")
	}
}

func TestEngine_Permission_WithHandler(t *testing.T) {
	wrapper := writeScript(t, "permission")

	var called sync.WaitGroup
	called.Add(1)
	handler := func(_ context.Context, req acp.PermissionRequest) (bool, error) {
		called.Done()
		if req.ToolName != "write_file" {
			t.Errorf("tool name = %q, want %q", req.ToolName, "write_file")
		}
		if req.ToolCallID != "call_perm_001" {
			t.Errorf("tool call ID = %q, want %q", req.ToolCallID, "call_perm_001")
		}
		if req.Description != "edit" {
			t.Errorf("description = %q, want %q", req.Description, "edit")
		}
		return true, nil
	}

	engine := acp.NewEngine(
		acp.WithBinary(wrapper),
		acp.WithPermissionHandler(handler),
	)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	proc, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	<-proc.Output() // drain init

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := collectUntilResult(proc.Output())
	called.Wait()

	hasResult := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageResult {
			hasResult = true
		}
	}
	if !hasResult {
		t.Error("expected MessageResult after permission flow")
	}
}

func TestEngine_Permission_NoHandler(t *testing.T) {
	wrapper := writeScript(t, "permission")
	// HITL on (default) + no handler → auto-deny.
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	proc, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	<-proc.Output() // drain init

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Turn should still complete — agent handles denial internally.
	msgs := collectUntilResult(proc.Output())
	hasResult := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageResult {
			hasResult = true
		}
	}
	if !hasResult {
		t.Error("expected MessageResult after auto-denied permission")
	}
}

func TestEngine_Send_ContextCancel(t *testing.T) {
	wrapper := writeScript(t, "slow-prompt")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	proc, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	<-proc.Output() // drain init

	// Send with a short deadline — mock delays 2s, so 100ms will cancel first.
	sendCtx, sendCancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer sendCancel()

	err = proc.Send(sendCtx, "should timeout")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Send = %v, want context.DeadlineExceeded", err)
	}
}

func TestEngine_Send_AfterStop(t *testing.T) {
	proc, _ := startProc(t)
	<-proc.Output() // drain init

	_ = proc.Stop(context.Background())

	err := proc.Send(context.Background(), "should fail")
	if !errors.Is(err, agentrun.ErrTerminated) {
		t.Errorf("Send after Stop = %v, want ErrTerminated", err)
	}
}

func TestEngine_Wait(t *testing.T) {
	proc, _ := startProc(t)
	<-proc.Output() // drain init

	// Stop triggers shutdown; Wait should return.
	_ = proc.Stop(context.Background())

	err := proc.Wait()
	if err != nil && !errors.Is(err, agentrun.ErrTerminated) {
		t.Errorf("Wait: %v", err)
	}
}

func TestEngine_Send_Concurrent(t *testing.T) {
	proc, ctx := startProc(t)
	<-proc.Output() // drain init

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	// Fire two concurrent sends — turnMu should serialize them.
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := proc.Send(ctx, fmt.Sprintf("concurrent-%d", i)); err != nil {
				errs <- err
			}
		}()
	}

	// Drain output in parallel so sends don't block.
	done := make(chan struct{})
	go func() {
		defer close(done)
		results := 0
		for msg := range proc.Output() {
			if msg.Type == agentrun.MessageResult {
				results++
				if results >= 2 {
					return
				}
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Send error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(integrationTimeout):
		t.Fatal("timed out waiting for concurrent results")
	}
}

func TestEngine_Permission_PanickingHandler(t *testing.T) {
	wrapper := writeScript(t, "permission")
	engine := acp.NewEngine(
		acp.WithBinary(wrapper),
		acp.WithPermissionHandler(func(_ context.Context, _ acp.PermissionRequest) (bool, error) {
			panic("handler exploded")
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	proc, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	<-proc.Output() // drain init

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := collectUntilResult(proc.Output())

	// Should see a MessageError about the panic.
	hasError := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageError && strings.Contains(m.Content, "panic") {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected MessageError about panic from permission handler")
	}

	// Turn should still complete.
	hasResult := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageResult {
			hasResult = true
		}
	}
	if !hasResult {
		t.Error("expected MessageResult after panicking handler")
	}
}

func TestEngine_Permission_HandlerError(t *testing.T) {
	wrapper := writeScript(t, "permission")
	engine := acp.NewEngine(
		acp.WithBinary(wrapper),
		acp.WithPermissionHandler(func(_ context.Context, _ acp.PermissionRequest) (bool, error) {
			return false, fmt.Errorf("access denied by policy")
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	proc, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	<-proc.Output() // drain init

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs := collectUntilResult(proc.Output())

	hasError := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageError && strings.Contains(m.Content, "access denied by policy") {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected MessageError about handler error")
	}

	hasResult := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageResult {
			hasResult = true
		}
	}
	if !hasResult {
		t.Error("expected MessageResult after handler error")
	}
}

// TestEngine_Send_ProcessExitDuringPrompt exercises the Send() done+errCh race:
// the process exits immediately after responding to a prompt. Send() should
// return nil (not panic on closed output channel) regardless of whether errCh
// or done wins the outer select.
func TestEngine_Send_ProcessExitDuringPrompt(t *testing.T) {
	wrapper := writeScript(t, "prompt-then-exit")
	engine := acp.NewEngine(acp.WithBinary(wrapper))

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	proc, err := engine.Start(ctx, agentrun.Session{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	<-proc.Output() // drain init

	// Drain output concurrently — process exits after prompt response,
	// so the output channel will close shortly after.
	go func() {
		//nolint:revive // drain all messages until channel closes
		for range proc.Output() {
		}
	}()

	err = proc.Send(ctx, "test")
	// The RPC response was sent before exit, so Send() should succeed.
	// In rare scheduling cases, done may win the select before errCh is
	// ready and the inner drain misses it — ErrTerminated is acceptable.
	if err != nil && !errors.Is(err, agentrun.ErrTerminated) {
		t.Fatalf("Send = %v, want nil or ErrTerminated", err)
	}
}

func TestEngine_Start_RelativeCWD(t *testing.T) {
	engine := newEngine(t)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	_, err := engine.Start(ctx, agentrun.Session{CWD: "relative/path"})
	if err == nil {
		t.Fatal("expected error for relative CWD")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("error = %v, want to contain 'absolute path'", err)
	}
}

func TestEngine_Start_InvalidHITL(t *testing.T) {
	engine := newEngine(t)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	session := agentrun.Session{
		CWD: t.TempDir(),
		Options: map[string]string{
			agentrun.OptionHITL: "invalid-value",
		},
	}

	_, err := engine.Start(ctx, session)
	if err == nil {
		t.Fatal("expected error for invalid HITL value")
	}
	if !strings.Contains(err.Error(), "invalid HITL") {
		t.Errorf("error = %v, want to contain 'invalid HITL'", err)
	}
}

func TestEngine_Start_ValidateEnv_RejectsInvalid(t *testing.T) {
	engine := newEngine(t)

	tests := []struct {
		name string
		env  map[string]string
	}{
		{"empty_key", map[string]string{"": "val"}},
		{"equals_in_key", map[string]string{"A=B": "val"}},
		{"null_in_key", map[string]string{"A\x00B": "val"}},
		{"null_in_value", map[string]string{"KEY": "val\x00ue"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Start(ctx, agentrun.Session{
				CWD: t.TempDir(),
				Env: tt.env,
			})
			if err == nil {
				t.Fatal("expected error for invalid env")
			}
		})
	}
}

func TestEngine_Start_InvalidEffort(t *testing.T) {
	engine := newEngine(t)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTimeout)
	defer cancel()

	_, err := engine.Start(ctx, agentrun.Session{
		CWD:     t.TempDir(),
		Options: map[string]string{agentrun.OptionEffort: "xhigh"},
	})
	if err == nil {
		t.Fatal("expected error for invalid effort")
	}
	if !strings.Contains(err.Error(), "unknown effort") {
		t.Errorf("error = %v, want to contain 'unknown effort'", err)
	}
}

func TestEngine_Validate(t *testing.T) {
	t.Run("valid binary", func(t *testing.T) {
		engine := newEngine(t)
		if err := engine.Validate(); err != nil {
			t.Errorf("Validate: %v", err)
		}
	})

	t.Run("no binary configured", func(t *testing.T) {
		engine := acp.NewEngine()
		err := engine.Validate()
		if err == nil {
			t.Fatal("expected error for empty binary")
		}
		if !errors.Is(err, agentrun.ErrUnavailable) {
			t.Errorf("error = %v, want ErrUnavailable", err)
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		engine := acp.NewEngine(acp.WithBinary("nonexistent-binary-xyz"))
		err := engine.Validate()
		if err == nil {
			t.Fatal("expected error for missing binary")
		}
	})
}
