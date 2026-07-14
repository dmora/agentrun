package agentrun

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunTurn_Normal(t *testing.T) {
	mp := newMockProcess()
	mp.output <- Message{Type: MessageText, Content: "hello"}
	mp.output <- Message{Type: MessageResult, Content: "done"}

	var msgs []Message
	err := RunTurn(context.Background(), mp, "prompt", func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Type != MessageText {
		t.Errorf("msgs[0].Type = %q, want %q", msgs[0].Type, MessageText)
	}
	if msgs[1].Type != MessageResult {
		t.Errorf("msgs[1].Type = %q, want %q", msgs[1].Type, MessageResult)
	}
}

func TestRunTurn_ContextCancellation(t *testing.T) {
	mp := newMockProcess()
	mp.sendFn = func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := RunTurn(ctx, mp, "prompt", func(_ Message) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRunTurn_ProcessCrash(t *testing.T) {
	mp := newMockProcess()
	mp.termErr = errors.New("process crashed")

	// Close channel without MessageResult (simulates crash).
	mp.close()

	err := RunTurn(context.Background(), mp, "prompt", func(_ Message) error {
		return nil
	})
	if err == nil || err.Error() != "process crashed" {
		t.Errorf("err = %v, want 'process crashed'", err)
	}
}

func TestRunTurn_SendError(t *testing.T) {
	mp := newMockProcess()
	sendErr := errors.New("send failed")
	mp.sendFn = func(_ context.Context, _ string) error {
		return sendErr
	}

	err := RunTurn(context.Background(), mp, "prompt", func(_ Message) error {
		return nil
	})
	if !errors.Is(err, sendErr) {
		t.Errorf("err = %v, want %v", err, sendErr)
	}
}

func TestRunTurn_HandlerError(t *testing.T) {
	mp := newMockProcess()
	mp.output <- Message{Type: MessageText, Content: "hello"}

	handlerErr := errors.New("handler abort")
	err := RunTurn(context.Background(), mp, "prompt", func(_ Message) error {
		return handlerErr
	})
	if !errors.Is(err, handlerErr) {
		t.Errorf("err = %v, want %v", err, handlerErr)
	}
}

func TestRunTurn_EmptyOutput(t *testing.T) {
	mp := newMockProcess()
	mp.close()

	err := RunTurn(context.Background(), mp, "prompt", func(_ Message) error {
		t.Error("handler should not be called")
		return nil
	})
	if err != nil {
		t.Errorf("err = %v, want nil (clean exit)", err)
	}
}

func TestRunTurn_SendErrorOnChannelClose(t *testing.T) {
	mp := newMockProcess()
	sendErr := errors.New("broken pipe")
	sendStarted := make(chan struct{})
	mp.sendFn = func(_ context.Context, _ string) error {
		close(sendStarted)
		// Simulate Send failing after channel close.
		time.Sleep(10 * time.Millisecond)
		return sendErr
	}

	// Wait for Send to start, then close.
	go func() {
		<-sendStarted
		mp.close()
	}()

	err := RunTurn(context.Background(), mp, "prompt", func(_ Message) error {
		return nil
	})
	// Should get either sendErr or nil (race between close and send error collection).
	// Both are acceptable — the key invariant is no hang.
	if err != nil && !errors.Is(err, sendErr) {
		t.Errorf("err = %v, want nil or %v", err, sendErr)
	}
}

func TestRunTurn_SendSucceedsBeforeResult(t *testing.T) {
	mp := newMockProcess()
	// Send returns nil immediately. Messages arrive after Send completes.
	// This exercises the sendCh = nil branch (runturn.go:44).
	sendDone := make(chan struct{})
	mp.sendFn = func(_ context.Context, _ string) error {
		close(sendDone)
		return nil
	}

	// Feed messages from a goroutine after Send completes.
	go func() {
		<-sendDone
		mp.output <- Message{Type: MessageText, Content: "hello"}
		mp.output <- Message{Type: MessageResult, Content: "done"}
	}()

	var msgs []Message
	err := RunTurn(context.Background(), mp, "prompt", func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Type != MessageText {
		t.Errorf("msgs[0].Type = %q, want %q", msgs[0].Type, MessageText)
	}
	if msgs[1].Type != MessageResult {
		t.Errorf("msgs[1].Type = %q, want %q", msgs[1].Type, MessageResult)
	}
}

func TestRunTurn_ErrNoResult(t *testing.T) {
	// Channel closes without MessageResult while termErr is ErrNoResult.
	// RunTurn should surface ErrNoResult via proc.Err().
	mp := newMockProcess()
	mp.termErr = ErrNoResult
	mp.output <- Message{Type: MessageText, Content: "partial"}
	mp.close()

	var msgs []Message
	err := RunTurn(context.Background(), mp, "prompt", func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if !errors.Is(err, ErrNoResult) {
		t.Fatalf("RunTurn err = %v, want ErrNoResult", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "partial" {
		t.Fatalf("msgs = %v, want [{partial}]", msgs)
	}
}

func TestRunTurn_MessagePassthrough(t *testing.T) {
	mp := newMockProcess()
	const wantMessage = "the user prompt"

	gotMessage := make(chan string, 1)
	mp.sendFn = func(_ context.Context, message string) error {
		gotMessage <- message
		return nil
	}

	// Provide a result so RunTurn completes.
	mp.output <- Message{Type: MessageResult, Content: "done"}

	err := RunTurn(context.Background(), mp, wantMessage, func(_ Message) error {
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}

	select {
	case got := <-gotMessage:
		if got != wantMessage {
			t.Errorf("Send received %q, want %q", got, wantMessage)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Send to be called")
	}
}

// ---------------------------------------------------------------------------
// SequentialSender (sequential path) tests
// ---------------------------------------------------------------------------

func TestRunTurn_Sequential_Normal(t *testing.T) {
	smp := newSequentialMockProcess()
	smp.sendFn = func(_ context.Context, _ string) error {
		// Simulate spawn-per-turn: replace output channel on Send.
		smp.output = make(chan Message, 16)
		smp.output <- Message{Type: MessageText, Content: "hello"}
		smp.output <- Message{Type: MessageResult, Content: "done"}
		return nil
	}

	var msgs []Message
	err := RunTurn(context.Background(), smp, "prompt", func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Type != MessageText {
		t.Errorf("msgs[0].Type = %q, want %q", msgs[0].Type, MessageText)
	}
	if msgs[1].Type != MessageResult {
		t.Errorf("msgs[1].Type = %q, want %q", msgs[1].Type, MessageResult)
	}
}

func TestRunTurn_Sequential_SendError(t *testing.T) {
	smp := newSequentialMockProcess()
	sendErr := errors.New("send failed")
	smp.sendFn = func(_ context.Context, _ string) error {
		return sendErr
	}

	handlerCalled := false
	err := RunTurn(context.Background(), smp, "prompt", func(_ Message) error {
		handlerCalled = true
		return nil
	})
	if !errors.Is(err, sendErr) {
		t.Errorf("err = %v, want %v", err, sendErr)
	}
	if handlerCalled {
		t.Error("handler should not be called when Send fails")
	}
}

func TestRunTurn_Sequential_ContextCancel(t *testing.T) {
	smp := newSequentialMockProcess()
	smp.sendFn = func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := RunTurn(ctx, smp, "prompt", func(_ Message) error {
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRunTurn_Sequential_ChannelClose(t *testing.T) {
	smp := newSequentialMockProcess()
	crashErr := errors.New("process crashed")
	smp.sendFn = func(_ context.Context, _ string) error {
		// Simulate subprocess crash: replace channel, close it, set termErr.
		smp.output = make(chan Message, 16)
		smp.termErr = crashErr
		smp.done = make(chan struct{})
		close(smp.output)
		close(smp.done)
		return nil
	}

	err := RunTurn(context.Background(), smp, "prompt", func(_ Message) error {
		return nil
	})
	if err == nil || err.Error() != "process crashed" {
		t.Errorf("err = %v, want 'process crashed'", err)
	}
}

func TestRunTurn_Sequential_HandlerError(t *testing.T) {
	smp := newSequentialMockProcess()
	smp.sendFn = func(_ context.Context, _ string) error {
		smp.output = make(chan Message, 16)
		smp.output <- Message{Type: MessageText, Content: "hello"}
		smp.output <- Message{Type: MessageResult, Content: "done"}
		return nil
	}

	handlerErr := errors.New("handler abort")
	err := RunTurn(context.Background(), smp, "prompt", func(_ Message) error {
		return handlerErr
	})
	if !errors.Is(err, handlerErr) {
		t.Errorf("err = %v, want %v", err, handlerErr)
	}
}

func TestRunTurn_Sequential_FreshChannel(t *testing.T) {
	// Verify RunTurn reads from the channel returned by Output() AFTER Send
	// completes, not from a stale pre-Send reference.
	smp := newSequentialMockProcess()
	// Pre-populate original channel with stale data.
	smp.output <- Message{Type: MessageText, Content: "stale"}

	smp.sendFn = func(_ context.Context, _ string) error {
		// Replace channel, simulating spawn-per-turn subprocess replacement.
		smp.output = make(chan Message, 16)
		smp.output <- Message{Type: MessageText, Content: "fresh"}
		smp.output <- Message{Type: MessageResult, Content: "done"}
		return nil
	}

	var msgs []Message
	err := RunTurn(context.Background(), smp, "prompt", func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurn error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Content != "fresh" {
		t.Errorf("msgs[0].Content = %q, want %q (should read from fresh channel)", msgs[0].Content, "fresh")
	}
}

// ---------------------------------------------------------------------------
// Subagent turn-boundary regression (issue #57)
// ---------------------------------------------------------------------------

// TestRunTurn_SubagentResultDoesNotEndTurn pins the fix for issue #57 on a
// persistent (reused-channel) process — the streaming case the bug was reported
// against. A subagent's terminal line is demoted by the backend to
// MessageTaskResult; drainOutput must NOT treat it as turn completion, so
// RunTurn returns only at the parent's own MessageResult with the full turn
// drained. It then verifies a subsequent RunTurn on the SAME process sees none
// of the first turn's messages — proving the premature-return + cross-dispatch
// bleed described in the issue cannot occur.
func TestRunTurn_SubagentResultDoesNotEndTurn(t *testing.T) {
	mp := newMockProcess()

	// Turn 1: narration, then a subagent result (would have ended the turn
	// early under the bug), then the parent's real deliverable + result.
	mp.output <- Message{Type: MessageText, Content: "launched an Explore agent"}
	mp.output <- Message{Type: MessageTaskResult, Content: "subagent findings"}
	mp.output <- Message{Type: MessageText, Content: "the real deliverable"}
	mp.output <- Message{Type: MessageResult, Content: "parent done"}

	var turn1 []Message
	if err := RunTurn(context.Background(), mp, "dispatch #1", func(msg Message) error {
		turn1 = append(turn1, msg)
		return nil
	}); err != nil {
		t.Fatalf("turn 1 RunTurn error: %v", err)
	}

	// (a) The turn drained fully — it did not return at the subagent result.
	if len(turn1) != 4 {
		t.Fatalf("turn 1 got %d messages, want 4 (no early return at subagent result): %+v", len(turn1), turn1)
	}
	if turn1[1].Type != MessageTaskResult {
		t.Errorf("turn1[1].Type = %q, want %q (subagent result must be delivered, not terminating)", turn1[1].Type, MessageTaskResult)
	}
	if turn1[2].Content != "the real deliverable" {
		t.Errorf("turn1[2].Content = %q, want the parent deliverable", turn1[2].Content)
	}
	if last := turn1[len(turn1)-1]; last.Type != MessageResult || last.Content != "parent done" {
		t.Errorf("turn 1 ended on %+v, want the parent MessageResult", last)
	}

	// Turn 2: on the SAME persistent process. Under the bug, turn 1 would have
	// left "the real deliverable"+parent result queued and turn 2 would read
	// them (cross-dispatch bleed). With the fix, turn 2 sees only its own output.
	mp.output <- Message{Type: MessageText, Content: "dispatch #2 output"}
	mp.output <- Message{Type: MessageResult, Content: "second done"}

	var turn2 []Message
	if err := RunTurn(context.Background(), mp, "dispatch #2", func(msg Message) error {
		turn2 = append(turn2, msg)
		return nil
	}); err != nil {
		t.Fatalf("turn 2 RunTurn error: %v", err)
	}
	if len(turn2) != 2 {
		t.Fatalf("turn 2 got %d messages, want 2 (no bleed from turn 1): %+v", len(turn2), turn2)
	}
	if turn2[0].Content != "dispatch #2 output" {
		t.Errorf("turn2[0].Content = %q, want %q (turn 1 must not bleed in)", turn2[0].Content, "dispatch #2 output")
	}
}
