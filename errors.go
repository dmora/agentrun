package agentrun

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

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

	// ErrSendNotSupported indicates the engine's backend cannot fulfill
	// Process.Send (no Streamer+InputFormatter or Resumer capability).
	// Returned by Engine.Start when the backend lacks a send path.
	ErrSendNotSupported = errors.New("agentrun: send not supported")

	// ErrNoResult indicates the process exited without producing a result
	// message (MessageResult). This typically means the agent process
	// terminated before completing its turn. Currently produced only by
	// CLI engines; ACP turn-completion is handled by RPC response lifecycle.
	ErrNoResult = errors.New("agentrun: process exited without result")

	// ErrModelDiscoveryUnsupported indicates that a backend or installed
	// backend version cannot enumerate its available models. Explicit model
	// selection may still be supported through Session.Model.
	ErrModelDiscoveryUnsupported = errors.New("agentrun: model discovery unsupported")

	// ErrModelSelectionUnsupported indicates that a backend cannot apply an
	// explicit Session.Model selection.
	ErrModelSelectionUnsupported = errors.New("agentrun: model selection unsupported")

	// ErrModelNotSupported indicates that a requested model is not present in
	// a finite catalog advertised by the backend.
	ErrModelNotSupported = errors.New("agentrun: model not supported")
)

// ModelNotSupportedError reports an explicit model selection that is absent
// from a backend's finite model catalog. Use errors.Is with
// ErrModelNotSupported or errors.As to inspect the requested model/catalog.
type ModelNotSupportedError struct {
	Model     string
	Available []ModelInfo
}

func (e *ModelNotSupportedError) Error() string {
	ids := make([]string, 0, len(e.Available))
	for _, model := range e.Available {
		ids = append(ids, model.ID)
	}
	if len(ids) == 0 {
		return fmt.Sprintf("%s: %q", ErrModelNotSupported, e.Model)
	}
	return fmt.Sprintf("%s: %q (available: %s)", ErrModelNotSupported, e.Model, strings.Join(ids, ", "))
}

func (e *ModelNotSupportedError) Unwrap() error { return ErrModelNotSupported }

// ExitError represents a subprocess that exited with a non-zero status.
// Wraps the underlying error to preserve the error chain — consumers can
// errors.As to *exec.ExitError for OS-level detail (signal info, etc.).
//
// Code semantics: positive = exit status, negative (-1) = signal-killed.
//
// Engines produce ExitError only for natural exits. User-initiated stops
// (via Process.Stop) produce ErrTerminated instead.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "agentrun: exit status " + strconv.Itoa(e.Code)
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode extracts the exit code from an error chain containing *ExitError.
// Returns (0, false) if the error does not contain an ExitError.
// Convenience wrapper around errors.As — equivalent to:
//
//	var exitErr *ExitError
//	if errors.As(err, &exitErr) { return exitErr.Code, true }
func ExitCode(err error) (int, bool) {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code, true
	}
	return 0, false
}
