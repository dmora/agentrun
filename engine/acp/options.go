package acp

import (
	"context"
	"time"
)

// Default engine configuration values.
const (
	defaultOutputBuffer      = 4096 // handles ~4K notifications per turn without blocking
	defaultGracePeriod       = 5 * time.Second
	defaultHandshakeTimeout  = 30 * time.Second
	defaultPermissionTimeout = 30 * time.Second
	defaultMaxMessageSize    = 4 << 20 // 4 MB — max JSON-RPC message size for Conn scanner
)

// PermissionRequest carries the agent's permission request to the handler.
// The handler returns a simple approve/deny decision; the engine maps this
// to the ACP option-based wire format internally.
type PermissionRequest struct {
	SessionID   string
	ToolName    string // from toolCallUpdate.Title
	ToolCallID  string // from toolCallUpdate.ToolCallID
	Description string // from toolCallUpdate.Kind
}

// PermissionHandler is called when the agent requests client-side permission.
// Runs in a dedicated goroutine (not blocking ReadLoop). Return true to approve.
// The engine maps the boolean to the ACP option-based wire format internally:
// true → allow_once (prefer) or allow_always; false → reject_once or reject_always.
//
// This collapses ACP's richer option-based outcomes to binary approve/deny.
// A future version may expose the full option set via PermissionRequest if
// consumers need to distinguish allow_once from allow_always.
// If nil, permission requests are auto-denied (unless HITL is off).
type PermissionHandler func(ctx context.Context, req PermissionRequest) (approved bool, err error)

// EngineOptions holds resolved construction-time configuration for an ACP engine.
type EngineOptions struct {
	// Binary is the ACP agent executable name or path.
	Binary string

	// Args are additional arguments passed to the binary (e.g., ["acp"]).
	Args []string

	// OutputBuffer is the channel buffer size for process output messages.
	OutputBuffer int

	// GracePeriod is the duration to wait after SIGTERM before sending SIGKILL.
	GracePeriod time.Duration

	// HandshakeTimeout is the deadline for initialize + session/new during Start().
	HandshakeTimeout time.Duration

	// MaxMessageSize is the maximum JSON-RPC message size in bytes for the scanner.
	MaxMessageSize int

	// PermissionTimeout is the deadline for the PermissionHandler callback.
	PermissionTimeout time.Duration

	// PermissionHandler is called when the agent requests client-side permission.
	PermissionHandler PermissionHandler
}

// EngineOption configures an Engine at construction time.
type EngineOption func(*EngineOptions)

// WithBinary sets the ACP agent executable name or path.
func WithBinary(binary string) EngineOption {
	return func(o *EngineOptions) {
		if binary != "" {
			o.Binary = binary
		}
	}
}

// WithArgs sets additional arguments passed to the binary.
func WithArgs(args ...string) EngineOption {
	return func(o *EngineOptions) {
		o.Args = args
	}
}

// WithOutputBuffer sets the channel buffer size for process output messages.
// Values <= 0 are ignored.
func WithOutputBuffer(size int) EngineOption {
	return func(o *EngineOptions) {
		if size > 0 {
			o.OutputBuffer = size
		}
	}
}

// WithGracePeriod sets the duration to wait after SIGTERM before sending SIGKILL.
// Values <= 0 are ignored.
func WithGracePeriod(d time.Duration) EngineOption {
	return func(o *EngineOptions) {
		if d > 0 {
			o.GracePeriod = d
		}
	}
}

// WithHandshakeTimeout sets the deadline for the initialize + session handshake.
// Values <= 0 are ignored.
func WithHandshakeTimeout(d time.Duration) EngineOption {
	return func(o *EngineOptions) {
		if d > 0 {
			o.HandshakeTimeout = d
		}
	}
}

// WithPermissionHandler sets the callback for agent permission requests.
func WithPermissionHandler(h PermissionHandler) EngineOption {
	return func(o *EngineOptions) {
		o.PermissionHandler = h
	}
}

// WithPermissionTimeout sets the deadline for the permission handler callback.
// Values <= 0 are ignored.
func WithPermissionTimeout(d time.Duration) EngineOption {
	return func(o *EngineOptions) {
		if d > 0 {
			o.PermissionTimeout = d
		}
	}
}

func resolveEngineOptions(opts ...EngineOption) EngineOptions {
	o := EngineOptions{
		OutputBuffer:      defaultOutputBuffer,
		GracePeriod:       defaultGracePeriod,
		HandshakeTimeout:  defaultHandshakeTimeout,
		MaxMessageSize:    defaultMaxMessageSize,
		PermissionTimeout: defaultPermissionTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}
