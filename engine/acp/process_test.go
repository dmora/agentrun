//go:build !windows

package acp

import (
	"context"
	"testing"

	"github.com/dmora/agentrun"
)

// newTestProcess creates a process with buffered output and updateCh for testing.
func newTestProcess(t *testing.T) *process {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &process{
		output:   make(chan agentrun.Message, 1),
		updateCh: make(chan agentrun.Message, 16),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// receiveMessage reads one message from the process output channel or fails.
func receiveMessage(t *testing.T, p *process) agentrun.Message {
	t.Helper()
	select {
	case msg := <-p.output:
		return msg
	default:
		t.Fatal("expected message on output channel")
		return agentrun.Message{}
	}
}

// receiveUpdate reads one message from the process updateCh or fails.
func receiveUpdate(t *testing.T, p *process) agentrun.Message {
	t.Helper()
	select {
	case msg := <-p.updateCh:
		return msg
	default:
		t.Fatal("expected message on updateCh")
		return agentrun.Message{}
	}
}

// TestHandlePromptResult_WithUsageNoContextFields verifies that MessageResult
// from handlePromptResult never populates ContextSizeTokens or ContextUsedTokens.
// Context window fields belong exclusively on MessageContextWindow
// (via parseUsageUpdate), not on turn-level MessageResult.
func TestHandlePromptResult_WithUsageNoContextFields(t *testing.T) {
	p := newTestProcess(t)
	result := &promptResult{
		StopReason: "end_turn",
		Usage: &acpUsage{
			InputTokens:  1000,
			OutputTokens: 200,
		},
	}
	td := &turnDenials{}
	ta := &turnAccumulator{}
	if err := p.handlePromptResult(nil, result, td, ta); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}
	msg := receiveUpdate(t, p)
	if msg.Usage == nil {
		t.Fatal("expected Usage on MessageResult")
	}
	if msg.Usage.ContextSizeTokens != 0 {
		t.Errorf("ContextSizeTokens = %d, want 0", msg.Usage.ContextSizeTokens)
	}
	if msg.Usage.ContextUsedTokens != 0 {
		t.Errorf("ContextUsedTokens = %d, want 0", msg.Usage.ContextUsedTokens)
	}
}

// TestHandlePromptResult_NilUsage verifies that nil promptResult.Usage
// produces a nil msg.Usage (no zero-value Usage struct).
func TestHandlePromptResult_NilUsage(t *testing.T) {
	p := newTestProcess(t)
	result := &promptResult{StopReason: "end_turn"}
	td := &turnDenials{}
	ta := &turnAccumulator{}
	if err := p.handlePromptResult(nil, result, td, ta); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}
	msg := receiveUpdate(t, p)
	if msg.Usage != nil {
		t.Errorf("expected nil Usage when promptResult.Usage is nil, got %+v", msg.Usage)
	}
}

// --- handlePromptResult synthesis tests ---

func TestHandlePromptResult_SynthesizesText(t *testing.T) {
	p := newTestProcess(t)
	ta := &turnAccumulator{}
	ta.observe(&agentrun.Message{Type: agentrun.MessageTextDelta, Content: "Hello "})
	ta.observe(&agentrun.Message{Type: agentrun.MessageTextDelta, Content: "world"})

	td := &turnDenials{}
	result := &promptResult{StopReason: "end_turn"}
	if err := p.handlePromptResult(nil, result, td, ta); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}

	msg1 := receiveUpdate(t, p)
	if msg1.Type != agentrun.MessageText {
		t.Errorf("msg1.Type = %q, want %q", msg1.Type, agentrun.MessageText)
	}
	if msg1.Content != "Hello world" {
		t.Errorf("msg1.Content = %q, want %q", msg1.Content, "Hello world")
	}

	msg2 := receiveUpdate(t, p)
	if msg2.Type != agentrun.MessageResult {
		t.Errorf("msg2.Type = %q, want %q", msg2.Type, agentrun.MessageResult)
	}
}

func TestHandlePromptResult_SynthesizesThinking(t *testing.T) {
	p := newTestProcess(t)
	ta := &turnAccumulator{}
	ta.observe(&agentrun.Message{Type: agentrun.MessageThinkingDelta, Content: "thinking"})

	td := &turnDenials{}
	result := &promptResult{StopReason: "end_turn"}
	if err := p.handlePromptResult(nil, result, td, ta); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}

	msg1 := receiveUpdate(t, p)
	if msg1.Type != agentrun.MessageThinking {
		t.Errorf("msg1.Type = %q, want %q", msg1.Type, agentrun.MessageThinking)
	}
	if msg1.Content != "thinking" {
		t.Errorf("msg1.Content = %q, want %q", msg1.Content, "thinking")
	}

	msg2 := receiveUpdate(t, p)
	if msg2.Type != agentrun.MessageResult {
		t.Errorf("msg2.Type = %q, want %q", msg2.Type, agentrun.MessageResult)
	}
}

func TestHandlePromptResult_SynthesizesBoth(t *testing.T) {
	p := newTestProcess(t)
	ta := &turnAccumulator{}
	ta.observe(&agentrun.Message{Type: agentrun.MessageThinkingDelta, Content: "hmm"})
	ta.observe(&agentrun.Message{Type: agentrun.MessageTextDelta, Content: "answer"})

	td := &turnDenials{}
	result := &promptResult{StopReason: "end_turn"}
	if err := p.handlePromptResult(nil, result, td, ta); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}

	// Thinking first, then text, then result.
	msg1 := receiveUpdate(t, p)
	if msg1.Type != agentrun.MessageThinking {
		t.Errorf("msg1.Type = %q, want %q", msg1.Type, agentrun.MessageThinking)
	}
	msg2 := receiveUpdate(t, p)
	if msg2.Type != agentrun.MessageText {
		t.Errorf("msg2.Type = %q, want %q", msg2.Type, agentrun.MessageText)
	}
	msg3 := receiveUpdate(t, p)
	if msg3.Type != agentrun.MessageResult {
		t.Errorf("msg3.Type = %q, want %q", msg3.Type, agentrun.MessageResult)
	}
}

func TestHandlePromptResult_EmptyAccumulator(t *testing.T) {
	p := newTestProcess(t)
	ta := &turnAccumulator{}
	td := &turnDenials{}
	result := &promptResult{StopReason: "end_turn"}
	if err := p.handlePromptResult(nil, result, td, ta); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}

	// Only MessageResult — no synthesized messages.
	msg := receiveUpdate(t, p)
	if msg.Type != agentrun.MessageResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
}

func TestHandlePromptResult_ErrorSealsAccumulator(t *testing.T) {
	p := newTestProcess(t)
	ta := &turnAccumulator{}
	ta.observe(&agentrun.Message{Type: agentrun.MessageTextDelta, Content: "lost"})

	td := &turnDenials{}
	err := p.handlePromptResult(context.DeadlineExceeded, &promptResult{}, td, ta)
	if err == nil {
		t.Fatal("expected error")
	}

	// Accumulator should be sealed (content discarded).
	if msgs := ta.seal(); msgs != nil {
		t.Errorf("seal after error should return nil, got %+v", msgs)
	}

	// No message on updateCh.
	select {
	case msg := <-p.updateCh:
		t.Errorf("expected empty updateCh, got %+v", msg)
	default:
		// expected
	}
}

// --- emitUpdate guard tests ---

func TestEmitUpdate_ClosedChannelGuard(t *testing.T) {
	p := newTestProcess(t)

	// Simulate wireReadLoop having closed updateCh.
	p.updateMu.Lock()
	p.updateChClosed = true
	close(p.updateCh)
	p.updateMu.Unlock()

	// Must not panic on closed channel.
	p.emitUpdate(agentrun.Message{Type: agentrun.MessageResult})
}

// --- buildInitMeta tests ---

func TestBuildInitMeta_AllFields(t *testing.T) {
	ir := &initializeResult{
		AgentInfo: &implementation{Name: "opencode", Version: "1.2.3"},
	}
	models := &sessionModelState{CurrentModelID: "claude-sonnet-4-5-20250514"}
	meta := buildInitMeta(ir, models)
	if meta == nil {
		t.Fatal("expected non-nil InitMeta")
	}
	if meta.Model != "claude-sonnet-4-5-20250514" {
		t.Errorf("Model = %q, want %q", meta.Model, "claude-sonnet-4-5-20250514")
	}
	if meta.AgentName != "opencode" {
		t.Errorf("AgentName = %q, want %q", meta.AgentName, "opencode")
	}
	if meta.AgentVersion != "1.2.3" {
		t.Errorf("AgentVersion = %q, want %q", meta.AgentVersion, "1.2.3")
	}
}

func TestBuildInitMeta_AgentInfoOnly(t *testing.T) {
	ir := &initializeResult{
		AgentInfo: &implementation{Name: "opencode", Version: "1.0.0"},
	}
	meta := buildInitMeta(ir, nil)
	if meta == nil {
		t.Fatal("expected non-nil InitMeta")
	}
	if meta.AgentName != "opencode" {
		t.Errorf("AgentName = %q, want %q", meta.AgentName, "opencode")
	}
	if meta.Model != "" {
		t.Errorf("Model = %q, want empty", meta.Model)
	}
}

func TestBuildInitMeta_ModelsOnly(t *testing.T) {
	ir := &initializeResult{} // no AgentInfo
	models := &sessionModelState{CurrentModelID: "claude-sonnet-4-5-20250514"}
	meta := buildInitMeta(ir, models)
	if meta == nil {
		t.Fatal("expected non-nil InitMeta")
	}
	if meta.Model != "claude-sonnet-4-5-20250514" {
		t.Errorf("Model = %q, want %q", meta.Model, "claude-sonnet-4-5-20250514")
	}
	if meta.AgentName != "" {
		t.Errorf("AgentName = %q, want empty", meta.AgentName)
	}
}

func TestBuildInitMeta_EmptyModelID(t *testing.T) {
	ir := &initializeResult{}
	models := &sessionModelState{CurrentModelID: ""}
	meta := buildInitMeta(ir, models)
	if meta != nil {
		t.Errorf("expected nil InitMeta when all fields empty, got %+v", meta)
	}
}

func TestBuildInitMeta_NilBoth(t *testing.T) {
	meta := buildInitMeta(nil, nil)
	if meta != nil {
		t.Errorf("expected nil InitMeta when both args nil, got %+v", meta)
	}
}

// TestBuildInitMeta_ControlCharsRejected verifies that control characters
// in AgentInfo or Model cause SanitizeCode to return "", triggering the
// nil-guard (all fields empty → nil InitMeta).
func TestBuildInitMeta_ControlCharsRejected(t *testing.T) {
	ir := &initializeResult{
		AgentInfo: &implementation{Name: "bad\x00name", Version: "1.\x1f0"},
	}
	models := &sessionModelState{CurrentModelID: "model\x07id"}
	meta := buildInitMeta(ir, models)
	if meta != nil {
		t.Errorf("expected nil InitMeta when all fields contain control chars, got %+v", meta)
	}
}

// TestBuildInitMeta_EmptyAgentInfoFields verifies that AgentInfo with
// empty Name and Version (but non-nil) combined with nil models → nil.
func TestBuildInitMeta_EmptyAgentInfoFields(t *testing.T) {
	ir := &initializeResult{
		AgentInfo: &implementation{Name: "", Version: ""},
	}
	meta := buildInitMeta(ir, nil)
	if meta != nil {
		t.Errorf("expected nil InitMeta when AgentInfo fields empty, got %+v", meta)
	}
}
