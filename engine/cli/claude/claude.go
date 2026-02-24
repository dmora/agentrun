package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
)

// Session option keys specific to the Claude CLI backend.
// Namespaced with "claude." to prevent collision across backends.
// Cross-cutting options (OptionSystemPrompt, OptionMaxTurns,
// OptionThinkingBudget, OptionResumeID) are defined in the root
// agentrun package.
const (
	// OptionPermissionMode sets the Claude Code --permission-mode flag.
	// Values should be PermissionMode constants.
	// This stays in the claude package (not root) because permission modes
	// are Claude CLI-specific — other backends have different or no
	// permission models. See DESIGN.md decision rule.
	OptionPermissionMode = "claude.permission_mode"
)

// validResumeID matches safe Claude session identifiers.
// Positive allowlist prevents control characters in CLI arguments.
var validResumeID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// PermissionMode controls Claude Code's permission behavior.
type PermissionMode string

const (
	// PermissionDefault uses Claude Code's default permission handling.
	// The --permission-mode flag is omitted when this mode is active.
	PermissionDefault PermissionMode = "default"

	// PermissionAcceptEdits auto-accepts file edit operations.
	PermissionAcceptEdits PermissionMode = "acceptEdits"

	// PermissionBypassAll bypasses all permission prompts.
	// Maps to CLI flag value "bypassPermissions".
	PermissionBypassAll PermissionMode = "bypassAll"

	// PermissionPlan restricts Claude to plan-only mode.
	PermissionPlan PermissionMode = "plan"
)

const defaultBinary = "claude"

// Backend is a Claude Code CLI backend for agentrun.
// It implements all cli package interfaces: Spawner, Parser, Resumer,
// Streamer, and InputFormatter.
type Backend struct {
	binary          string
	partialMessages bool // default true — emit token-level streaming deltas
}

// Compile-time interface satisfaction checks.
var (
	_ cli.Backend        = (*Backend)(nil)
	_ cli.Spawner        = (*Backend)(nil)
	_ cli.Parser         = (*Backend)(nil)
	_ cli.Resumer        = (*Backend)(nil)
	_ cli.Streamer       = (*Backend)(nil)
	_ cli.InputFormatter = (*Backend)(nil)
)

// Option configures a Backend at construction time.
type Option func(*Backend)

// WithBinary overrides the Claude CLI binary path.
// Empty values are ignored; the default is "claude".
func WithBinary(path string) Option {
	return func(b *Backend) {
		if path != "" {
			b.binary = path
		}
	}
}

// WithPartialMessages controls whether StreamArgs includes
// --include-partial-messages for token-level streaming deltas.
// Default is true (deltas enabled). Set to false to receive only
// complete messages.
func WithPartialMessages(enabled bool) Option {
	return func(b *Backend) {
		b.partialMessages = enabled
	}
}

// New creates a Claude Code CLI backend with the given options.
// The default binary is "claude".
func New(opts ...Option) *Backend {
	b := &Backend{binary: defaultBinary, partialMessages: true}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// SpawnArgs builds exec.Cmd arguments for a new Claude session.
// OptionResumeID is intentionally ignored here — resume is handled by
// StreamArgs (streaming path) or ResumeArgs (subprocess replacement).
// Invalid option values are silently skipped (SpawnArgs must not fail per
// the Spawner interface contract).
func (b *Backend) SpawnArgs(session agentrun.Session) (string, []string) {
	args := baseArgs()
	args = appendSessionArgs(args, session)
	// Prompt is always the last positional argument.
	// Null-byte-containing prompts are silently omitted (no error return).
	if !jsonutil.ContainsNull(session.Prompt) {
		args = append(args, session.Prompt)
	}
	return b.binary, args
}

// StreamArgs builds exec.Cmd arguments for a long-lived streaming session.
// Adds --input-format stream-json and omits the trailing prompt.
// When partial messages are enabled (default), adds --include-partial-messages
// for token-level streaming deltas.
// When OptionResumeID is set and valid, adds --resume to resume an existing
// conversation over the streaming connection.
func (b *Backend) StreamArgs(session agentrun.Session) (string, []string) {
	args := baseArgs()
	args = append(args, "--input-format", "stream-json")
	if b.partialMessages {
		args = append(args, "--include-partial-messages")
	}
	if id := session.Options[agentrun.OptionResumeID]; id != "" && validateResumeID(id) == nil {
		args = append(args, "--resume", id)
	}
	args = appendSessionArgs(args, session)
	return b.binary, args
}

// ResumeArgs builds exec.Cmd arguments to resume an existing Claude session.
// Returns an error if OptionResumeID is missing, contains null bytes, has
// an invalid format, or permission mode is invalid. Unlike SpawnArgs/StreamArgs,
// ResumeArgs validates strictly because it has an error return.
func (b *Backend) ResumeArgs(session agentrun.Session, initialPrompt string) (string, []string, error) {
	resumeID := session.Options[agentrun.OptionResumeID]
	if resumeID == "" {
		return "", nil, errors.New("claude: missing resume_id in session options")
	}
	if err := validateResumeID(resumeID); err != nil {
		return "", nil, err
	}
	if jsonutil.ContainsNull(initialPrompt) {
		return "", nil, errors.New("claude: initial prompt contains null bytes")
	}

	if err := validateSessionOptions(session.Options); err != nil {
		return "", nil, err
	}

	args := baseArgs()
	args = append(args, "--resume", resumeID)
	args = appendSessionArgs(args, session)
	args = append(args, initialPrompt)
	return b.binary, args, nil
}

// validateResumeID reports whether id matches the safe character allowlist
// for Claude session identifiers. Prevents control characters and other
// unsafe content from reaching CLI arguments.
func validateResumeID(id string) error {
	if !validResumeID.MatchString(id) {
		return fmt.Errorf("claude: invalid resume_id format: %q", id)
	}
	return nil
}

// FormatInput encodes a user message for delivery to a Claude stdin pipe.
// Returns an error if the message contains null bytes.
func (b *Backend) FormatInput(message string) ([]byte, error) {
	if jsonutil.ContainsNull(message) {
		return nil, errors.New("claude: message contains null bytes")
	}
	stdinMsg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": message,
		},
	}
	data, err := json.Marshal(stdinMsg)
	if err != nil {
		return nil, fmt.Errorf("claude: marshal stdin: %w", err)
	}
	return append(data, '\n'), nil
}

// baseArgs returns the common CLI flags for all command modes.
func baseArgs() []string {
	return []string{
		"-p",
		"--verbose",
		"--output-format", "stream-json",
	}
}

// validatePositiveInt checks that opts[key], if non-empty, is a valid positive
// integer with no null bytes. Returns an error with the given label for
// diagnostics. Used by ResumeArgs for strict pre-validation.
func validatePositiveInt(opts map[string]string, key, label string) error {
	v := opts[key]
	if v == "" {
		return nil
	}
	if jsonutil.ContainsNull(v) {
		return fmt.Errorf("claude: %s contains null bytes", label)
	}
	if n, err := strconv.Atoi(v); err != nil || n <= 0 {
		return fmt.Errorf("claude: invalid %s %q: must be a positive integer", label, v)
	}
	return nil
}

// appendPositiveInt appends --flag <value> if opts[key] is a valid positive
// integer. Invalid, zero, negative, or null-byte-containing values are
// silently skipped. The value is round-tripped through Atoi/Itoa to ensure
// only clean integers reach the CLI.
func appendPositiveInt(args []string, opts map[string]string, key, flag string) []string {
	v := opts[key]
	if v == "" || jsonutil.ContainsNull(v) {
		return args
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		args = append(args, flag, strconv.Itoa(n))
	}
	return args
}

// appendSessionArgs appends model, system-prompt, permission-mode,
// max-turns, and max-thinking-tokens flags based on session fields
// and options. Invalid or null-byte-containing values are silently skipped.
func appendSessionArgs(args []string, session agentrun.Session) []string {
	if session.Model != "" && !jsonutil.ContainsNull(session.Model) {
		args = append(args, "--model", session.Model)
	}

	if sp := session.Options[agentrun.OptionSystemPrompt]; sp != "" && !jsonutil.ContainsNull(sp) {
		args = append(args, "--system-prompt", sp)
	}

	if flag, ok := resolvePermissionFlag(session.Options); ok {
		args = append(args, "--permission-mode", flag)
	}

	args = appendPositiveInt(args, session.Options, agentrun.OptionMaxTurns, "--max-turns")
	args = appendPositiveInt(args, session.Options, agentrun.OptionThinkingBudget, "--max-thinking-tokens")

	return args
}

// rootOptionsSet reports whether either OptionMode or OptionHITL is present
// in opts. When true, root options take precedence over backend-specific
// OptionPermissionMode.
func rootOptionsSet(opts map[string]string) bool {
	return opts[agentrun.OptionMode] != "" || opts[agentrun.OptionHITL] != ""
}

// resolvePermissionFlag maps root-level OptionMode/OptionHITL and
// backend-specific OptionPermissionMode to a --permission-mode value.
// Root options and backend options are independent control surfaces:
// when root options are set, OptionPermissionMode is ignored;
// when root options are absent, OptionPermissionMode is used.
//
// Invalid Mode/HITL values are treated as unrecognized and produce no flag.
// This is intentional: SpawnArgs/StreamArgs must not fail (no error return),
// so unknown values are silently skipped. ResumeArgs validates strictly via
// validateSessionOptions before calling appendSessionArgs.
func resolvePermissionFlag(opts map[string]string) (string, bool) {
	mode := agentrun.Mode(opts[agentrun.OptionMode])
	hitl := agentrun.HITL(opts[agentrun.OptionHITL])

	// Root options set — use them exclusively.
	if rootOptionsSet(opts) {
		if mode == agentrun.ModePlan {
			return "plan", true
		}
		if hitl == agentrun.HITLOff {
			return "bypassPermissions", true
		}
		// act+on, just act, or just hitl=on → default behavior (no flag).
		return "", false
	}

	// Root options absent — defer to backend-specific OptionPermissionMode.
	perm := PermissionMode(opts[OptionPermissionMode])
	if perm != "" && perm != PermissionDefault {
		if mapped, err := mapPermission(perm); err == nil {
			return mapped, true
		}
	}
	return "", false
}

// validateSessionOptions performs strict validation of session options used
// by ResumeArgs. Checks mode, HITL, permission mode, max turns, and thinking
// budget. Returns the first validation error encountered.
func validateSessionOptions(opts map[string]string) error {
	if err := validateModeHITL(opts); err != nil {
		return err
	}
	// Validate permission only when root options are absent (independent surfaces).
	if err := validatePermissionIfNoRoot(opts); err != nil {
		return err
	}
	if err := validatePositiveInt(opts, agentrun.OptionMaxTurns, "max turns"); err != nil {
		return err
	}
	return validatePositiveInt(opts, agentrun.OptionThinkingBudget, "thinking budget")
}

// validateModeHITL checks OptionMode and OptionHITL for valid values.
func validateModeHITL(opts map[string]string) error {
	if mode := agentrun.Mode(opts[agentrun.OptionMode]); mode != "" && !mode.Valid() {
		return fmt.Errorf("claude: unknown mode %q: valid: plan, act", mode)
	}
	if hitl := agentrun.HITL(opts[agentrun.OptionHITL]); hitl != "" && !hitl.Valid() {
		return fmt.Errorf("claude: unknown hitl %q: valid: on, off", hitl)
	}
	return nil
}

// validatePermissionIfNoRoot validates OptionPermissionMode only when root
// options (OptionMode/OptionHITL) are absent — they are independent surfaces.
func validatePermissionIfNoRoot(opts map[string]string) error {
	if rootOptionsSet(opts) {
		return nil
	}
	perm := PermissionMode(opts[OptionPermissionMode])
	if perm != "" && perm != PermissionDefault {
		_, err := mapPermission(perm)
		return err
	}
	return nil
}

// mapPermission maps a PermissionMode to its Claude CLI flag value.
// Returns an error for unknown modes; the error message includes valid values.
func mapPermission(perm PermissionMode) (string, error) {
	switch perm {
	case PermissionDefault:
		return "default", nil
	case PermissionAcceptEdits:
		return "acceptEdits", nil
	case PermissionBypassAll:
		return "bypassPermissions", nil
	case PermissionPlan:
		return "plan", nil
	default:
		return "", fmt.Errorf("claude: unknown permission mode %q; valid: default, acceptEdits, bypassAll, plan", perm)
	}
}
