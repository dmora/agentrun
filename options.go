package agentrun

import "time"

// StartOptions holds resolved configuration for Engine.Start.
// Engine implementations call ResolveOptions to collapse functional
// options into this struct.
type StartOptions struct {
	// Prompt overrides Session.Prompt for this invocation.
	Prompt string

	// Model overrides Session.Model for this invocation.
	Model string

	// Timeout sets a deadline for the Start operation.
	// Zero means no timeout beyond the context deadline.
	Timeout time.Duration
}

// Option configures an Engine.Start invocation.
type Option func(*StartOptions)

// ResolveOptions applies functional options and returns the resolved config.
// Engine implementations call this in their Start method.
func ResolveOptions(opts ...Option) StartOptions {
	var so StartOptions
	for _, opt := range opts {
		opt(&so)
	}
	return so
}

// WithPrompt overrides the session prompt for this invocation.
func WithPrompt(prompt string) Option {
	return func(o *StartOptions) {
		o.Prompt = prompt
	}
}

// WithModel overrides the session model for this invocation.
func WithModel(model string) Option {
	return func(o *StartOptions) {
		o.Model = model
	}
}

// WithTimeout sets a deadline for the start operation.
func WithTimeout(d time.Duration) Option {
	return func(o *StartOptions) {
		o.Timeout = d
	}
}
