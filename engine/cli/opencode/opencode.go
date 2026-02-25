package opencode

import (
	"errors"
	"fmt"
	"regexp"
	"sync/atomic"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
)

// Session option keys specific to the OpenCode backend.
// Namespaced with "opencode." to prevent collision across backends.
// Cross-cutting options (OptionThinkingBudget, OptionMode, OptionHITL,
// OptionResumeID) are defined in the root agentrun package.
const (
	// OptionVariant sets the OpenCode --variant flag.
	// Values should be Variant constants.
	OptionVariant = "opencode.variant"

	// OptionFork enables forking when resuming an OpenCode session.
	// Any non-empty value adds the --fork flag.
	OptionFork = "opencode.fork"

	// OptionTitle sets the OpenCode --title flag for session naming.
	// Values exceeding maxTitleLen bytes are silently skipped.
	OptionTitle = "opencode.title"
)

// maxTitleLen is the maximum byte length for session titles.
// 512 bytes is generous for a human-readable title while preventing
// unbounded CLI argument length. Titles exceeding this are silently
// skipped per the Spawner contract.
const maxTitleLen = 512

// Variant controls provider-specific reasoning effort level via --variant.
type Variant string

const (
	VariantHigh    Variant = "high"
	VariantMax     Variant = "max"
	VariantMinimal Variant = "minimal"
	VariantLow     Variant = "low"
)

// validSessionID matches observed OpenCode session IDs: "ses_" + 20-40 alphanumeric chars.
var validSessionID = regexp.MustCompile(`^ses_[a-zA-Z0-9]{20,40}$`)

const defaultBinary = "opencode"

// Backend is an OpenCode CLI backend for agentrun.
// It implements cli.Spawner, cli.Parser, and cli.Resumer.
//
// OpenCode does NOT support streaming input (no cli.Streamer or
// cli.InputFormatter). Multi-turn conversation uses resume-per-turn:
// each Send() spawns a new subprocess with --session <id>.
//
// One Backend instance per session. The session ID is auto-captured
// from the first step_start event via atomic write-once.
type Backend struct {
	binary    string
	sessionID atomic.Pointer[string] // write-once from first step_start
}

// Compile-time interface satisfaction checks.
// OpenCode does NOT implement cli.Streamer or cli.InputFormatter.
var (
	_ cli.Backend = (*Backend)(nil)
	_ cli.Spawner = (*Backend)(nil)
	_ cli.Parser  = (*Backend)(nil)
	_ cli.Resumer = (*Backend)(nil)
)

// Option configures a Backend at construction time.
type Option func(*Backend)

// WithBinary overrides the OpenCode CLI binary path.
// Empty values are ignored; the default is "opencode".
func WithBinary(path string) Option {
	return func(b *Backend) {
		if path != "" {
			b.binary = path
		}
	}
}

// New creates an OpenCode CLI backend with the given options.
// The default binary is "opencode".
func New(opts ...Option) *Backend {
	b := &Backend{binary: defaultBinary}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// SpawnArgs builds exec.Cmd arguments for a new OpenCode session.
// When OptionResumeID is set and valid, adds --session for cold resume.
// Invalid option values are silently skipped per the Spawner contract.
func (b *Backend) SpawnArgs(session agentrun.Session) (string, []string) {
	args := baseArgs()
	args = appendCommonArgs(args, session)

	if id := session.Options[agentrun.OptionResumeID]; id != "" && validateSessionID(id) == nil {
		args = append(args, "--session", id)
	}

	if id := session.Options[agentrun.OptionAgentID]; id != "" && !jsonutil.ContainsNull(id) {
		args = append(args, "--agent", id)
	}

	if t := session.Options[OptionTitle]; t != "" && !jsonutil.ContainsNull(t) && len(t) <= maxTitleLen {
		args = append(args, "--title", t)
	}

	// Prompt is always the last positional argument.
	if session.Prompt != "" && !jsonutil.ContainsNull(session.Prompt) {
		args = append(args, session.Prompt)
	}
	return b.binary, args
}

// ResumeArgs builds exec.Cmd arguments to resume an existing OpenCode session.
// The session ID is resolved from:
//  1. The atomic write-once ID captured from step_start (auto-capture)
//  2. session.Options[OptionResumeID] (explicit fallback)
//
// Returns an error if no session ID is available or if the message
// contains null bytes.
//
// Note on HITL: OptionHITL=off requires OPENCODE_AUTO_APPROVE=1 env var
// on the subprocess. The CLI engine does not yet support per-backend env
// vars, so consumers must set this externally.
func (b *Backend) ResumeArgs(session agentrun.Session, initialPrompt string) (string, []string, error) {
	sid := b.resolveSessionID(session)
	if sid == "" {
		return "", nil, errors.New("opencode: no session ID available (not captured from step_start and not set via OptionResumeID)")
	}
	if err := validateSessionID(sid); err != nil {
		return "", nil, err
	}
	if jsonutil.ContainsNull(initialPrompt) {
		return "", nil, errors.New("opencode: initial prompt contains null bytes")
	}

	args := baseArgs()
	args = append(args, "--session", sid)

	if session.Options[OptionFork] != "" {
		args = append(args, "--fork")
	}

	args = appendCommonArgs(args, session)

	if initialPrompt != "" {
		args = append(args, initialPrompt)
	}
	return b.binary, args, nil
}

// resolveSessionID returns the session ID from the atomic store (auto-capture)
// or from OptionResumeID. Stored ID takes precedence.
func (b *Backend) resolveSessionID(session agentrun.Session) string {
	if p := b.sessionID.Load(); p != nil {
		return *p
	}
	return session.Options[agentrun.OptionResumeID]
}

// baseArgs returns the common CLI flags for all command modes.
func baseArgs() []string {
	return []string{"run", "--format", "json"}
}

// appendCommonArgs appends model, thinking, and variant flags.
// Mode, HITL, SystemPrompt, and MaxTurns are silently ignored
// (OpenCode has no CLI flags for these).
func appendCommonArgs(args []string, session agentrun.Session) []string {
	if session.Model != "" && !jsonutil.ContainsNull(session.Model) {
		args = append(args, "--model", session.Model)
	}

	if session.Options[agentrun.OptionThinkingBudget] != "" {
		args = append(args, "--thinking")
	}

	if v := session.Options[OptionVariant]; v != "" && !jsonutil.ContainsNull(v) {
		args = append(args, "--variant", v)
	}

	return args
}

// SessionID returns the auto-captured session ID, or empty string if not yet captured.
func (b *Backend) SessionID() string {
	if p := b.sessionID.Load(); p != nil {
		return *p
	}
	return ""
}

// validateSessionID reports whether id matches the expected OpenCode session ID format.
func validateSessionID(id string) error {
	if !validSessionID.MatchString(id) {
		return fmt.Errorf("opencode: invalid session ID format: %q", id)
	}
	return nil
}
