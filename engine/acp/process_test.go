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
