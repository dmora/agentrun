package cli

import "time"

// Default engine configuration values.
const (
	defaultOutputBuffer  = 100
	defaultScannerBuffer = 1 << 20 // 1 MB
	defaultGracePeriod   = 5 * time.Second
)

// EngineOptions holds resolved construction-time configuration for a CLI engine.
// Use NewEngine with EngineOption functions to customize these values.
type EngineOptions struct {
	// OutputBuffer is the channel buffer size for process output messages.
	OutputBuffer int

	// ScannerBuffer is the maximum line size in bytes for the stdout scanner.
	ScannerBuffer int

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

// WithScannerBuffer sets the maximum line size in bytes for the stdout scanner.
// Values <= 0 are ignored.
func WithScannerBuffer(size int) EngineOption {
	return func(o *EngineOptions) {
		if size > 0 {
			o.ScannerBuffer = size
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

func resolveEngineOptions(opts ...EngineOption) EngineOptions {
	o := EngineOptions{
		OutputBuffer:  defaultOutputBuffer,
		ScannerBuffer: defaultScannerBuffer,
		GracePeriod:   defaultGracePeriod,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}
