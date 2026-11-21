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
	// explicit Session.Model selection. The ACP engine returns it from Start
	// when Session.Model names a model other than the agent's current one and
	// the agent advertises no model config option to select it with. The
	// request is not silently dropped.
	ErrModelSelectionUnsupported = errors.New("agentrun: model selection unsupported")

	// ErrModelNotSupported indicates that a requested model is not present in
	// a finite catalog advertised by the backend.
	ErrModelNotSupported = errors.New("agentrun: model not supported")
)

// maxListedModels caps how many catalog identifiers ModelNotSupportedError
// puts in its message. Agents can advertise hundreds of models.
const maxListedModels = 10

// ModelNotSupportedError reports an explicit model selection that is absent
// from a backend's finite model catalog. Use errors.Is with
// ErrModelNotSupported or errors.As to inspect the requested model/catalog.
//
// Available holds the full catalog. Error names only the first 10
// identifiers and says how many more there are.
//
// The ACP engine returns this error from Start after the agent has created
// the session, because the catalog arrives in that response. On an agent that
// persists sessions, the rejected Start leaves that session behind and Start
// returns no handle to it. To avoid that, check the model with ModelAvailable
// against a catalog obtained earlier (ModelLister.ListModels, or
// InitMeta.AvailableModels from a previous session) before calling Start.
// ErrModelSelectionUnsupported is returned at the same point and has the same
// effect.
type ModelNotSupportedError struct {
	Model     string
	Available []ModelInfo
}

func (e *ModelNotSupportedError) Error() string {
	if len(e.Available) == 0 {
		return fmt.Sprintf("%s: %q", ErrModelNotSupported, e.Model)
	}
	listed := e.Available
	if len(listed) > maxListedModels {
		listed = listed[:maxListedModels]
	}
	ids := make([]string, 0, len(listed))
	for _, model := range listed {
		ids = append(ids, model.ID)
	}
	list := strings.Join(ids, ", ")
	if more := len(e.Available) - len(listed); more > 0 {
		list += fmt.Sprintf(", and %d more", more)
	}
	return fmt.Sprintf("%s: %q (available: %s)", ErrModelNotSupported, e.Model, list)
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
