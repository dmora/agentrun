package agentrun

import "errors"

// Sentinel errors for engine operations.
var (
	// ErrUnavailable indicates the engine cannot start
	// (binary not found, API unreachable, etc.).
	ErrUnavailable = errors.New("agentrun: engine unavailable")

	// ErrTerminated indicates the session was terminated
	// (process killed, connection closed).
	ErrTerminated = errors.New("agentrun: session terminated")

	// ErrSessionNotFound indicates the requested session does not exist.
	ErrSessionNotFound = errors.New("agentrun: session not found")
)
