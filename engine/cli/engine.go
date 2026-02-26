//go:build !windows

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli/internal/optutil"
)

// Engine is a CLI subprocess engine that adapts a Backend into an agentrun.Engine.
// It orchestrates subprocess lifecycle, message pumping, and graceful shutdown.
type Engine struct {
	backend Backend
	opts    EngineOptions
}

// Compile-time interface satisfaction check.
var _ agentrun.Engine = (*Engine)(nil)

// NewEngine creates a CLI engine backed by the given Backend.
// Use EngineOption functions to customize buffer sizes and grace period.
func NewEngine(backend Backend, opts ...EngineOption) *Engine {
	return &Engine{
		backend: backend,
		opts:    resolveEngineOptions(opts...),
	}
}

// Validate checks that the backend's binary is available on PATH.
// It recovers from panics in SpawnArgs (backends may panic on zero Session).
func (e *Engine) Validate() (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("%w: SpawnArgs panicked: %v", agentrun.ErrUnavailable, r)
		}
	}()

	binary, _ := e.backend.SpawnArgs(agentrun.Session{})
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("%w: %s: %w", agentrun.ErrUnavailable, binary, err)
	}
	return nil
}

// Start initializes a subprocess session and returns a Process handle.
// Returns [agentrun.ErrSendNotSupported] if the backend lacks a send path
// (neither Streamer+InputFormatter nor Resumer).
// The context parameter is reserved for future use (e.g., start timeout);
// subprocess lifetime is controlled via [agentrun.Process.Stop].
func (e *Engine) Start(_ context.Context, session agentrun.Session, opts ...agentrun.Option) (agentrun.Process, error) {
	startOpts := agentrun.ResolveOptions(opts...)

	// Deep-copy session to prevent aliasing.
	session = cloneSession(session)

	// Apply option overrides.
	if startOpts.Prompt != "" {
		session.Prompt = startOpts.Prompt
	}
	if startOpts.Model != "" {
		session.Model = startOpts.Model
	}

	// Validate CWD.
	if !filepath.IsAbs(session.CWD) {
		return nil, fmt.Errorf("cli: CWD must be an absolute path, got %q", session.CWD)
	}
	info, err := os.Stat(session.CWD)
	if err != nil {
		return nil, fmt.Errorf("cli: CWD: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("cli: CWD is not a directory: %s", session.CWD)
	}

	// Validate cross-cutting options early (before SpawnArgs/StreamArgs).
	if err := optutil.ValidateEffort("cli", session.Options); err != nil {
		return nil, err
	}

	// Resolve capabilities once.
	caps := resolveCapabilities(e.backend)

	if err := validateSendCapability(caps); err != nil {
		return nil, err
	}

	// Determine mode: Streamer (stdin pipe) requires both Streamer and
	// InputFormatter. Without a formatter, fall back to SpawnArgs even
	// if Streamer is present (Resumer handles subsequent Send calls).
	useStreamer := caps.streamer != nil && caps.formatter != nil
	var binary string
	var args []string
	if useStreamer {
		binary, args = caps.streamer.StreamArgs(session)
	} else {
		binary, args = e.backend.SpawnArgs(session)
	}

	resolvedBinary, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", agentrun.ErrUnavailable, binary, err)
	}

	// Validate and resolve environment variables.
	if err := agentrun.ValidateEnv(session.Env); err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	env := agentrun.MergeEnv(os.Environ(), session.Env)

	cmd, stdin, stdout, err := spawnCmd(resolvedBinary, args, session.CWD, useStreamer, env)
	if err != nil {
		return nil, fmt.Errorf("cli: start: %w", err)
	}

	return newProcess(e.backend, caps, session, e.opts, env, cmd, stdin, stdout), nil
}

// spawnCmd builds, configures, and starts an exec.Cmd.
// env is passed directly to cmd.Env — nil inherits the parent environment.
func spawnCmd(binary string, args []string, dir string, wantStdin bool, env []string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stdin io.WriteCloser
	if wantStdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
		}
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return cmd, stdin, stdout, nil
}

// validateSendCapability checks that the backend can fulfill Process.Send.
// A backend needs either Streamer+InputFormatter or Resumer to support Send.
func validateSendCapability(caps capabilities) error {
	hasStreamerPath := caps.streamer != nil && caps.formatter != nil
	hasResumerPath := caps.resumer != nil
	if !hasStreamerPath && !hasResumerPath {
		if caps.streamer != nil {
			return fmt.Errorf("%w: backend implements Streamer but not InputFormatter — implement InputFormatter to enable multi-turn conversation", agentrun.ErrSendNotSupported)
		}
		return fmt.Errorf("%w: backend implements neither Streamer+InputFormatter nor Resumer — implement at least one send path for multi-turn conversation", agentrun.ErrSendNotSupported)
	}
	return nil
}

// cloneSession returns a deep copy of session, cloning Options and Env maps.
func cloneSession(s agentrun.Session) agentrun.Session {
	return s.Clone()
}
