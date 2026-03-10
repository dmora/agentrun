package agentrun

import "context"

// RunTurn sends a message and drains Output() until MessageResult or channel
// close. handler is called for each message (including MessageResult).
// Safe for all engine types.
//
// RunTurn type-asserts on [SequentialSender] to select the send/drain strategy:
//
//   - SequentialSender (spawn-per-turn CLI backends): Send executes
//     synchronously in the caller's goroutine and respects the context
//     deadline without leaking a goroutine. If Send succeeds, Output is
//     drained from the fresh channel. If Send fails, the handler is never
//     called and RunTurn returns the Send error immediately.
//
//   - Default (ACP, streaming CLI): Send runs in a background goroutine
//     while Output is drained concurrently. The caller should provide a
//     context with a deadline or timeout. The Send goroutine is not joined
//     on return — if Send blocks indefinitely (e.g., a hung RPC), the
//     goroutine leaks until the context is canceled. After MessageResult
//     arrives, any in-flight Send error is collected non-blocking; a Send
//     error that arrives after MessageResult is intentionally dropped.
//
// In both paths, if the handler returns an error the drain stops and RunTurn
// returns it. If the channel closes without MessageResult, RunTurn returns
// proc.Err(). Context cancellation stops both Send and the drain.
func RunTurn(ctx context.Context, proc Process, message string, handler func(Message) error) error {
	if _, ok := proc.(SequentialSender); ok {
		// Send must complete before draining Output (spawn-per-turn backends).
		if err := proc.Send(ctx, message); err != nil {
			return err
		}
		return drainOutput(ctx, proc, nil, handler)
	}
	// Default: concurrent Send + drain (ACP, streaming CLI).
	sendCh := make(chan error, 1)
	go func() {
		sendCh <- proc.Send(ctx, message)
	}()

	return drainOutput(ctx, proc, sendCh, handler)
}

// drainOutput reads from proc.Output() until MessageResult, channel close,
// or context cancellation. Checks sendCh for Send errors.
func drainOutput(ctx context.Context, proc Process, sendCh <-chan error, handler func(Message) error) error {
	for {
		select {
		case msg, ok := <-proc.Output():
			if !ok {
				return channelClosed(proc, sendCh)
			}
			if err := handler(msg); err != nil {
				return err
			}
			if msg.Type == MessageResult {
				return collectSendError(sendCh)
			}

		case err := <-sendCh:
			if err != nil {
				return err
			}
			sendCh = nil // Send succeeded — stop selecting on it.

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// channelClosed handles Output() channel close: returns Send error if any,
// otherwise returns proc.Err().
func channelClosed(proc Process, sendCh <-chan error) error {
	if err := collectSendError(sendCh); err != nil {
		return err
	}
	return proc.Err()
}

// collectSendError drains the Send error channel without blocking.
func collectSendError(sendCh <-chan error) error {
	select {
	case err := <-sendCh:
		return err
	default:
		return nil
	}
}
