package agentrun

import "context"

// Engine starts and validates agent sessions.
//
// Implementations include CLI subprocess engines (engine/cli) and
// API-based engines (engine/api/adk). Use Validate to check that the
// engine's prerequisites are met before calling Start.
type Engine interface {
	// Start initializes a session and returns a Process handle.
	// The Process immediately begins producing Messages on its Output channel.
	// Options override Session fields for this specific invocation.
	Start(ctx context.Context, session Session, opts ...Option) (Process, error)

	// Validate checks that the engine is available and ready.
	// For CLI engines, this verifies the binary exists and is executable.
	// For API engines, this checks connectivity and authentication.
	Validate() error
}

// ModelLister is an optional engine capability for discovering the models
// available in the current backend authentication and provider environment.
// Engines that cannot enumerate models return ErrModelDiscoveryUnsupported.
//
// The session supplies the same CWD, environment, and backend options that
// would be used by Start. Implementations must not run a model turn.
type ModelLister interface {
	ListModels(ctx context.Context, session Session) ([]ModelInfo, error)
}
