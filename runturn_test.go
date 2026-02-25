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
