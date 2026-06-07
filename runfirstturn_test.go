package agentrun

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunFirstTurn_SequentialDrainsWithoutSend verifies that for a
// spawn-per-turn process (SequentialSender), RunFirstTurn drains the turn
// that Start already initiated and does NOT call Send (which would spawn a
// redundant — and for resume-only backends, broken — second turn).
func TestRunFirstTurn_SequentialDrainsWithoutSend(t *testing.T) {
	proc := newSequentialMockProcess()
	var sendCalls atomic.Int32
	proc.sendFn = func(_ context.Context, _ string) error {
		sendCalls.Add(1)
		return nil
	}

	// The first turn's messages are already flowing (Start spawned it).
	proc.output <- Message{Type: MessageText, Content: "turn 1 output"}
	proc.output <- Message{Type: MessageResult}

	var got []Message
	err := RunFirstTurn(context.Background(), proc, "the prompt", func(m Message) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatalf("RunFirstTurn: %v", err)
	}
	if n := sendCalls.Load(); n != 0 {
		t.Errorf("Send called %d times; want 0 (Start already initiated turn 1)", n)
	}
	if len(got) != 2 || got[0].Content != "turn 1 output" || got[1].Type != MessageResult {
		t.Errorf("drained messages = %+v; want [text, result]", got)
	}
}

// TestRunFirstTurn_NonSequentialSends verifies that for a streaming/persistent
// process (not a SequentialSender), RunFirstTurn sends the prompt to initiate
// the first turn, then drains.
func TestRunFirstTurn_NonSequentialSends(t *testing.T) {
	proc := newMockProcess()
	var sentMessage atomic.Value
	proc.sendFn = func(_ context.Context, message string) error {
		sentMessage.Store(message)
		// Emit the turn result once the prompt has been sent.
		proc.output <- Message{Type: MessageResult}
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got []Message
	err := RunFirstTurn(ctx, proc, "hello agent", func(m Message) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatalf("RunFirstTurn: %v", err)
	}
	if v, _ := sentMessage.Load().(string); v != "hello agent" {
		t.Errorf("Send received %q; want %q", v, "hello agent")
	}
	if len(got) != 1 || got[0].Type != MessageResult {
		t.Errorf("drained messages = %+v; want [result]", got)
	}
}

// TestRunFirstTurn_SequentialChannelCloseNoResult verifies RunFirstTurn returns
// the process terminal error if the first turn's channel closes without a
// MessageResult (e.g., subprocess died before producing output).
func TestRunFirstTurn_SequentialChannelCloseNoResult(t *testing.T) {
	proc := newSequentialMockProcess()
	proc.termErr = ErrNoResult
	proc.close() // channel closed immediately, no result

	err := RunFirstTurn(context.Background(), proc, "prompt", func(Message) error {
		return nil
	})
	if !errors.Is(err, ErrNoResult) {
		t.Errorf("RunFirstTurn err = %v; want ErrNoResult", err)
	}
}
