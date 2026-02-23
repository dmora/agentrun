// Package filter provides composable channel middleware for filtering
// agentrun message streams. Consumers wrap proc.Output() with these
// functions to select the message granularity they need.
package filter

import (
	"context"
	"strings"

	"github.com/dmora/agentrun"
)

// Filter returns a channel that only passes messages of the given types.
// Spawns a goroutine that exits when ctx is cancelled or ch is closed.
// The returned channel is closed when the goroutine exits.
func Filter(ctx context.Context, ch <-chan agentrun.Message, types ...agentrun.MessageType) <-chan agentrun.Message {
	allowed := make(map[agentrun.MessageType]struct{}, len(types))
	for _, t := range types {
		allowed[t] = struct{}{}
	}
	return pipe(ctx, ch, func(msg agentrun.Message) bool {
		_, ok := allowed[msg.Type]
		return ok
	})
}

// Completed returns a channel that drops all delta types, passing only
// complete messages. Spawns a goroutine that exits when ctx is cancelled
// or ch is closed.
func Completed(ctx context.Context, ch <-chan agentrun.Message) <-chan agentrun.Message {
	return pipe(ctx, ch, func(msg agentrun.Message) bool {
		return !IsDelta(msg.Type)
	})
}

// ResultOnly returns a channel that passes only MessageResult.
// Spawns a goroutine that exits when ctx is cancelled or ch is closed.
func ResultOnly(ctx context.Context, ch <-chan agentrun.Message) <-chan agentrun.Message {
	return pipe(ctx, ch, func(msg agentrun.Message) bool {
		return msg.Type == agentrun.MessageResult
	})
}

// IsDelta reports whether t is a streaming delta (partial) message type.
// Convention: all delta types use the "_delta" suffix (e.g., text_delta,
// tool_use_delta, thinking_delta). This avoids needing to update a
// switch statement each time a new delta type is added.
func IsDelta(t agentrun.MessageType) bool {
	return strings.HasSuffix(string(t), "_delta")
}

// pipe spawns a goroutine that reads from ch, passes messages matching
// the predicate to the returned channel, and closes it when ch closes
// or ctx is cancelled. Callers must either drain the returned channel
// or cancel ctx to avoid goroutine leaks. Messages accepted by the
// predicate may be silently dropped if ctx is cancelled mid-send.
func pipe(ctx context.Context, ch <-chan agentrun.Message, accept func(agentrun.Message) bool) <-chan agentrun.Message {
	out := make(chan agentrun.Message)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				if accept(msg) && !trySend(ctx, out, msg) {
					return
				}
			}
		}
	}()
	return out
}

// trySend sends msg on out, returning true on success.
// Returns false if ctx is cancelled before the send completes.
func trySend(ctx context.Context, out chan<- agentrun.Message, msg agentrun.Message) bool {
	select {
	case out <- msg:
		return true
	case <-ctx.Done():
		return false
	}
}
