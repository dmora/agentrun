package agentrun

import "context"

// Process is an active session handle.
//
// Messages flow through the Output channel. Send transmits user messages
// to the running agent. Stop terminates the session, and Wait blocks
// until it ends naturally.
//
// Process is an interface to enable wrapping with logging, metrics,
// or retry middleware.
type Process interface {
	// Output returns the channel for receiving messages from the agent.
	// The channel is closed when the session ends (normally or on error).
	// The first message may be of type MessageInit for engines that
	// perform a handshake.
	Output() <-chan Message

	// Send transmits a user message to the active session.
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
