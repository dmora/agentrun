package filter

import (
	"context"
	"testing"

	"github.com/dmora/agentrun"
)

func msg(t agentrun.MessageType) agentrun.Message {
	return agentrun.Message{Type: t, Content: string(t)}
}

func fill(ch chan<- agentrun.Message, msgs ...agentrun.Message) {
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
}

func drain(ch <-chan agentrun.Message) []agentrun.Message {
	var out []agentrun.Message
	for m := range ch {
		out = append(out, m)
	}
	return out
}

// --- Filter tests ---

func TestFilter_PassesRequestedTypes(t *testing.T) {
	in := make(chan agentrun.Message, 5)
	go fill(in,
		msg(agentrun.MessageTextDelta),
		msg(agentrun.MessageText),
		msg(agentrun.MessageResult),
		msg(agentrun.MessageError),
		msg(agentrun.MessageSystem),
	)

	out := Filter(context.Background(), in, agentrun.MessageText, agentrun.MessageResult)
	got := drain(out)

	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	if got[0].Type != agentrun.MessageText {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, agentrun.MessageText)
	}
	if got[1].Type != agentrun.MessageResult {
		t.Errorf("got[1].Type = %q, want %q", got[1].Type, agentrun.MessageResult)
	}
}

func TestFilter_NoTypesDropsAll(t *testing.T) {
	in := make(chan agentrun.Message, 3)
	go fill(in,
		msg(agentrun.MessageText),
		msg(agentrun.MessageResult),
		msg(agentrun.MessageError),
	)

	out := Filter(context.Background(), in)
	got := drain(out)

	if len(got) != 0 {
		t.Errorf("got %d messages, want 0 (no types = drop all)", len(got))
	}
}

func TestFilter_ContextCancellation(_ *testing.T) {
	in := make(chan agentrun.Message)
	ctx, cancel := context.WithCancel(context.Background())
	out := Filter(ctx, in, agentrun.MessageText)

	cancel()

	// Output channel should close after ctx cancel.
	drain(out)
}

func TestFilter_EmptyInput(t *testing.T) {
	in := make(chan agentrun.Message)
	close(in)

	out := Filter(context.Background(), in, agentrun.MessageText)
	got := drain(out)

	if len(got) != 0 {
		t.Errorf("got %d messages, want 0", len(got))
	}
}

// --- Completed tests ---

func TestCompleted_DropsDeltas(t *testing.T) {
	in := make(chan agentrun.Message, 6)
	go fill(in,
		msg(agentrun.MessageTextDelta),
		msg(agentrun.MessageToolUseDelta),
		msg(agentrun.MessageThinkingDelta),
		msg(agentrun.MessageText),
		msg(agentrun.MessageResult),
		msg(agentrun.MessageError),
	)

	out := Completed(context.Background(), in)
	got := drain(out)

	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}
	want := []agentrun.MessageType{agentrun.MessageText, agentrun.MessageResult, agentrun.MessageError}
	for i, w := range want {
		if got[i].Type != w {
			t.Errorf("got[%d].Type = %q, want %q", i, got[i].Type, w)
		}
	}
}

func TestCompleted_PassesNonDelta(t *testing.T) {
	nonDelta := []agentrun.MessageType{
		agentrun.MessageText, agentrun.MessageResult, agentrun.MessageError,
		agentrun.MessageInit, agentrun.MessageSystem, agentrun.MessageEOF,
		agentrun.MessageToolUse, agentrun.MessageToolResult,
	}
	in := make(chan agentrun.Message, len(nonDelta))
	go func() {
		for _, mt := range nonDelta {
			in <- msg(mt)
		}
		close(in)
	}()

	out := Completed(context.Background(), in)
	got := drain(out)

	if len(got) != len(nonDelta) {
		t.Fatalf("got %d messages, want %d", len(got), len(nonDelta))
	}
}

func TestCompleted_ContextCancellation(_ *testing.T) {
	in := make(chan agentrun.Message)
	ctx, cancel := context.WithCancel(context.Background())
	out := Completed(ctx, in)

	cancel()

	drain(out)
}

func TestCompleted_EmptyInput(t *testing.T) {
	in := make(chan agentrun.Message)
	close(in)

	out := Completed(context.Background(), in)
	got := drain(out)

	if len(got) != 0 {
		t.Errorf("got %d messages, want 0", len(got))
	}
}

// --- ResultOnly tests ---

func TestResultOnly_PassesOnlyResult(t *testing.T) {
	in := make(chan agentrun.Message, 5)
	go fill(in,
		msg(agentrun.MessageTextDelta),
		msg(agentrun.MessageText),
		msg(agentrun.MessageError),
		msg(agentrun.MessageResult),
		msg(agentrun.MessageInit),
	)

	out := ResultOnly(context.Background(), in)
	got := drain(out)

	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Type != agentrun.MessageResult {
		t.Errorf("got[0].Type = %q, want %q", got[0].Type, agentrun.MessageResult)
	}
}

func TestResultOnly_EmptyInput(t *testing.T) {
	in := make(chan agentrun.Message)
	close(in)

	out := ResultOnly(context.Background(), in)
	got := drain(out)

	if len(got) != 0 {
		t.Errorf("got %d messages, want 0", len(got))
	}
}

func TestResultOnly_ContextCancellation(_ *testing.T) {
	in := make(chan agentrun.Message)
	ctx, cancel := context.WithCancel(context.Background())
	out := ResultOnly(ctx, in)

	cancel()

	// Output channel should close after ctx cancel.
	drain(out)
}

// --- IsDelta tests ---

func TestIsDelta(t *testing.T) {
	tests := []struct {
		mt   agentrun.MessageType
		want bool
	}{
		{agentrun.MessageTextDelta, true},
		{agentrun.MessageToolUseDelta, true},
		{agentrun.MessageThinkingDelta, true},
		{agentrun.MessageText, false},
		{agentrun.MessageResult, false},
		{agentrun.MessageError, false},
		{agentrun.MessageInit, false},
		{agentrun.MessageSystem, false},
		{agentrun.MessageEOF, false},
		{agentrun.MessageToolUse, false},
		{agentrun.MessageToolResult, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.mt), func(t *testing.T) {
			if got := IsDelta(tt.mt); got != tt.want {
				t.Errorf("IsDelta(%q) = %v, want %v", tt.mt, got, tt.want)
			}
		})
	}
}
