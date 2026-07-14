package cli

import (
	"io"
	"time"
)

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

	// StderrWriter receives subprocess stderr output when non-nil.
	// The writer's lifetime must span the entire engine lifecycle including
	// resumes, since spawn-per-turn backends start new subprocesses on each
	// Send call. When nil, subprocess stderr is discarded (connected to
	// os.DevNull by exec.Cmd).
	//
	// Concurrency: the subprocess stderr goroutine writes to this writer
	// concurrently with engine operations. Implementations must be safe
	// for concurrent use if the caller reads or resets the writer between
	// turns.
	StderrWriter io.Writer

	// SubagentTools is the set of parent-level tool names that spawn a
	// subagent (e.g., "Task", "Agent"). It gates subagent tracking: when EMPTY
	// (the default), the engine performs no tracking and never stamps
	// Message.Background — today's behavior, byte-identical, with no linger
	// signal for consumers. When non-empty, the engine tracks outstanding
	// subagents and stamps BackgroundStats on MessageResult/MessageTaskResult.
	//
	// The names drive the name-based fallback path (add a pending id when a
	// parent-level tool_use in this set is seen). Backends that emit a native
	// background-task feed (Claude's MessageBackgroundTasks) supersede the
	// name-based path with the authoritative pending set once the first
	// snapshot arrives; for those backends the set primarily acts as the
	// opt-in switch. Empty is deliberately the default so backends that
	// coincidentally use a tool named "Task" are not tracked unless the caller
	// explicitly opts in.
	SubagentTools map[string]struct{}
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

// WithStderrWriter sets a writer to receive subprocess stderr output.
// The writer's lifetime must span the entire engine lifecycle including
// resumes, since spawn-per-turn backends start new subprocesses on each
// Send call. Nil is a no-op (stderr is discarded via os.DevNull).
func WithStderrWriter(w io.Writer) EngineOption {
	return func(o *EngineOptions) {
		o.StderrWriter = w
	}
}

// WithSubagentTools enables subagent tracking for parent-level tool calls
// whose name is in the given set (e.g., "Task", "Agent"). With no names (or
// only empty names) tracking stays OFF and Message.Background is never
// stamped — the default. Repeated calls union the names. Blank names are
// ignored.
//
// Enable this only for backends that report subagent completion
// (MessageTaskResult) or a native background-task feed
// (MessageBackgroundTasks); otherwise a started subagent never decrements and
// its BackgroundSubagent TaskCounts.Pending() would stay positive forever.
func WithSubagentTools(names ...string) EngineOption {
	return func(o *EngineOptions) {
		for _, n := range names {
			if n == "" {
				continue
			}
			if o.SubagentTools == nil {
				o.SubagentTools = make(map[string]struct{})
			}
			o.SubagentTools[n] = struct{}{}
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
