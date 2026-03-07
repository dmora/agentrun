package cli

import "time"

// Default engine configuration values.
const (
	defaultOutputBuffer = 100
	defaultMaxLineSize  = 128 << 20 // 128 MB
	defaultGracePeriod  = 5 * time.Second
)

// EngineOptions holds resolved construction-time configuration for a CLI engine.
// Use NewEngine with EngineOption functions to customize these values.
type EngineOptions struct {
	// OutputBuffer is the channel buffer size for process output messages.
	OutputBuffer int

	// MaxLineSize is the maximum assembled line size in bytes for stdout reading.
	// Zero or negative means unlimited. The default is 128 MB.
	MaxLineSize int

	// GracePeriod is the duration to wait after SIGTERM before sending SIGKILL.
	GracePeriod time.Duration
}

// EngineOption configures an Engine at construction time.
type EngineOption func(*EngineOptions)

// WithOutputBuffer sets the channel buffer size for process output messages.
// Values <= 0 are ignored.
func WithOutputBuffer(size int) EngineOption {
	return func(o *EngineOptions) {
		if size > 0 {
			o.OutputBuffer = size
		}
	}
}

// WithMaxLineSize sets the maximum line size in bytes for stdout reading.
// Zero or negative means unlimited. The default is 128 MB.
func WithMaxLineSize(size int) EngineOption {
	return func(o *EngineOptions) {
		o.MaxLineSize = size
	}
}

// Deprecated: WithScannerBuffer is an alias for [WithMaxLineSize].
// Values <= 0 are ignored to preserve backwards compatibility.
func WithScannerBuffer(size int) EngineOption {
	if size <= 0 {
		return func(*EngineOptions) {} // no-op, matches old behavior
	}
	return WithMaxLineSize(size)
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

func resolveEngineOptions(opts ...EngineOption) EngineOptions {
	o := EngineOptions{
		OutputBuffer: defaultOutputBuffer,
		MaxLineSize:  defaultMaxLineSize,
		GracePeriod:  defaultGracePeriod,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}
