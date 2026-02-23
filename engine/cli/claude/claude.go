package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// Session option keys for Session.Options map.
const (
	// OptionSystemPrompt sets the Claude Code --system-prompt flag.
	OptionSystemPrompt = "system_prompt"

	// OptionPermissionMode sets the Claude Code --permission-mode flag.
	// Values should be PermissionMode constants.
	OptionPermissionMode = "permission_mode"

	// OptionMaxTurns sets the Claude Code --max-turns flag.
	OptionMaxTurns = "max_turns"

	// OptionResumeID is the Claude conversation ID for --resume.
	// Only valid in ResumeArgs; SpawnArgs ignores this key.
	OptionResumeID = "resume_id"
)

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
// Invalid option values are silently skipped (SpawnArgs must not fail per
// the Spawner interface contract).
func (b *Backend) SpawnArgs(session agentrun.Session) (string, []string) {
	args := baseArgs()
	args = appendSessionArgs(args, session)
	// Prompt is always the last positional argument.
	// Null-byte-containing prompts are silently omitted (no error return).
	if !containsNull(session.Prompt) {
		args = append(args, session.Prompt)
	}
	return b.binary, args
}

// StreamArgs builds exec.Cmd arguments for a long-lived streaming session.
// Adds --input-format stream-json and omits the trailing prompt.
// When partial messages are enabled (default), adds --include-partial-messages
// for token-level streaming deltas.
func (b *Backend) StreamArgs(session agentrun.Session) (string, []string) {
	args := baseArgs()
	args = append(args, "--input-format", "stream-json")
	if b.partialMessages {
		args = append(args, "--include-partial-messages")
	}
	args = appendSessionArgs(args, session)
	return b.binary, args
}

// ResumeArgs builds exec.Cmd arguments to resume an existing Claude session.
// Returns an error if OptionResumeID is missing, contains null bytes, or
// permission mode is invalid. Unlike SpawnArgs/StreamArgs, ResumeArgs
// validates strictly because it has an error return.
func (b *Backend) ResumeArgs(session agentrun.Session, initialPrompt string) (string, []string, error) {
	resumeID := session.Options[OptionResumeID]
	if resumeID == "" {
		return "", nil, errors.New("claude: missing resume_id in session options")
	}
	if containsNull(resumeID) {
		return "", nil, errors.New("claude: resume_id contains null bytes")
	}
	if containsNull(initialPrompt) {
		return "", nil, errors.New("claude: initial prompt contains null bytes")
	}

	// Validate permission strictly (error on invalid) before appendSessionArgs
	// which would silently skip.
	perm := PermissionMode(session.Options[OptionPermissionMode])
	if perm != "" && perm != PermissionDefault {
		if _, err := mapPermission(perm); err != nil {
			return "", nil, err
		}
	}

	args := baseArgs()
	args = append(args, "--resume", resumeID)
	args = appendSessionArgs(args, session)
	args = append(args, initialPrompt)
	return b.binary, args, nil
}

// FormatInput encodes a user message for delivery to a Claude stdin pipe.
// Returns an error if the message contains null bytes.
func (b *Backend) FormatInput(message string) ([]byte, error) {
	if containsNull(message) {
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

// containsNull reports whether s contains a null byte.
func containsNull(s string) bool {
	return strings.ContainsRune(s, '\x00')
}

// appendSessionArgs appends model, system-prompt, permission-mode, and
// max-turns flags based on session fields and options. Invalid or
// null-byte-containing values are silently skipped.
func appendSessionArgs(args []string, session agentrun.Session) []string {
	if session.Model != "" && !containsNull(session.Model) {
		args = append(args, "--model", session.Model)
	}

	if sp := session.Options[OptionSystemPrompt]; sp != "" && !containsNull(sp) {
		args = append(args, "--system-prompt", sp)
	}

	perm := PermissionMode(session.Options[OptionPermissionMode])
	if perm != "" && perm != PermissionDefault {
		if mapped, err := mapPermission(perm); err == nil {
			args = append(args, "--permission-mode", mapped)
		}
	}

	if mt := session.Options[OptionMaxTurns]; mt != "" && !containsNull(mt) {
		if n, err := strconv.Atoi(mt); err == nil && n > 0 {
			args = append(args, "--max-turns", mt)
		}
	}

	return args
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
