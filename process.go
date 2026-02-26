package agentrun

import "context"

// Process is an active session handle.
//
// Messages flow through the Output channel. Send transmits user messages
// to the running agent. Stop terminates the session, and Wait blocks
// until it ends naturally.
//
// Important: some engines (notably ACP) require Output() to be drained
// concurrently while Send() is in progress — Send blocks on an RPC call
// while the agent streams updates to Output(). Use [RunTurn] to handle
// this correctly for all engine types.
//
// Process is an interface to enable wrapping with logging, metrics,
// or retry middleware.
type Process interface {
	// Output returns the channel for receiving messages from the agent.
	// The channel is closed when the session ends (normally or on error).
	// The first message may be of type MessageInit for engines that
	// perform a handshake.
	//
	// Callers must drain this channel concurrently with Send for engines
	// that stream updates during the RPC call. See [RunTurn] for a helper
	// that handles this correctly.
	Output() <-chan Message

	// Send transmits a user message to the active session.
	// For ACP engines, Send blocks until the agent's turn completes.
	// Callers must drain Output() concurrently to avoid deadlock.
	// See [RunTurn] for a helper that handles this correctly.
	Send(ctx context.Context, message string) error

	// Stop terminates the session. For CLI engines, this sends SIGTERM
	// then SIGKILL after a grace period.
	Stop(ctx context.Context) error

	// Wait blocks until the session ends naturally.
	// Returns nil on clean exit, or an error describing the failure.
	Wait() error

	// Err returns the terminal error after the Output channel is closed.
	// Returns nil if the session ended cleanly. Callers should call Err
	// after the Output channel is closed to distinguish clean exit from
	// failure.
	Err() error
}
