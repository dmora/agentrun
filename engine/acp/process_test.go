//go:build !windows

package acp

import (
	"context"
	"testing"

	"github.com/dmora/agentrun"
)

// newTestProcess creates a process with a buffered output channel for testing.
func newTestProcess(t *testing.T) *process {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &process{
		output: make(chan agentrun.Message, 1),
		ctx:    ctx,
		cancel: cancel,
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
	if err := p.handlePromptResult(nil, result); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}
	msg := receiveMessage(t, p)
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
	if err := p.handlePromptResult(nil, result); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}
	msg := receiveMessage(t, p)
	if msg.Usage != nil {
		t.Errorf("expected nil Usage when promptResult.Usage is nil, got %+v", msg.Usage)
	}
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
