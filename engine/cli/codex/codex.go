package codex

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
	"github.com/dmora/agentrun/engine/cli/internal/optutil"
)

// Session option keys specific to the Codex backend.
// Namespaced with "codex." to prevent collision across backends.
// Cross-cutting options (OptionMode, OptionHITL, OptionResumeID)
// are defined in the root agentrun package.
const (
	// OptionSandbox sets the --sandbox flag for codex exec.
	// Values should be Sandbox constants (SandboxReadOnly, etc.).
	// First turn only — not available on exec resume.
	// Ignored when root OptionMode or OptionHITL is set (independent surfaces).
	OptionSandbox = "codex.sandbox"

	// OptionEphemeral enables --ephemeral mode (no session persistence).
	// Any non-empty value adds the flag.
	OptionEphemeral = "codex.ephemeral"

	// OptionProfile sets the -p <profile> flag for codex exec.
	// First turn only — not available on exec resume.
	OptionProfile = "codex.profile"

	// OptionOutputSchema sets the --output-schema <file> flag.
	// First turn only — not available on exec resume.
	OptionOutputSchema = "codex.output_schema"

	// OptionSkipGitCheck adds --skip-git-repo-check.
	// Any non-empty value adds the flag.
	OptionSkipGitCheck = "codex.skip_git_check"
)

// Sandbox controls the sandbox policy via --sandbox.
type Sandbox string

const (
	SandboxReadOnly       Sandbox = "read-only"
	SandboxWorkspaceWrite Sandbox = "workspace-write"
	SandboxFullAccess     Sandbox = "danger-full-access"
)

// validSandbox reports whether s is a recognized sandbox value.
func validSandbox(s Sandbox) bool {
	switch s {
	case SandboxReadOnly, SandboxWorkspaceWrite, SandboxFullAccess:
		return true
	}
	return false
}

// CLI subcommand and flag constants (goconst).
const (
	subcmdExec   = "exec"
	subcmdResume = "resume"
	flagJSON     = "--json"
)

const defaultBinary = "codex"

// noUUIDSentinel is stored in threadID when the first thread.started has a
// non-UUID ID. This distinguishes "init emitted, no UUID" from "nothing
// happened yet" and prevents duplicate MessageInit emissions.
var noUUIDSentinel = "\x00"

// Backend is a Codex CLI backend for agentrun.
// It implements cli.Spawner, cli.Parser, and cli.Resumer.
//
// Codex does NOT support streaming input (no cli.Streamer or
// cli.InputFormatter). Multi-turn conversation uses resume-per-turn:
// each Send() spawns a new subprocess via "codex exec resume".
//
// One Backend instance per session. The thread ID is auto-captured
// from the first thread.started event via atomic write-once.
type Backend struct {
	binary   string
	threadID atomic.Pointer[string] // write-once from thread.started
}

// Compile-time interface satisfaction checks.
var (
	_ cli.Backend = (*Backend)(nil)
	_ cli.Spawner = (*Backend)(nil)
	_ cli.Parser  = (*Backend)(nil)
	_ cli.Resumer = (*Backend)(nil)
)

// Option configures a Backend at construction time.
type Option func(*Backend)

// WithBinary overrides the Codex CLI binary path.
// Empty values are ignored; the default is "codex".
func WithBinary(path string) Option {
	return func(b *Backend) {
		if path != "" {
			b.binary = path
		}
	}
}

// New creates a Codex CLI backend with the given options.
// The default binary is "codex".
func New(opts ...Option) *Backend {
	b := &Backend{binary: defaultBinary}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// SpawnArgs builds exec.Cmd arguments for a new Codex session.
// When OptionResumeID is set, produces "exec resume" subcommand.
// Invalid option values are silently skipped per the Spawner contract.
func (b *Backend) SpawnArgs(session agentrun.Session) (string, []string) {
	opts := session.Options

	// OptionResumeID present → subcommand switch to "exec resume".
	if id := opts[agentrun.OptionResumeID]; id != "" && !jsonutil.ContainsNull(id) {
		args := buildResumeCommand(id, session)
		if session.Prompt != "" && !jsonutil.ContainsNull(session.Prompt) {
			args = append(args, session.Prompt)
		}
		return b.binary, args
	}
	return b.binary, buildExecCommand(session)
}

// ResumeArgs builds exec.Cmd arguments to resume an existing Codex session.
// The thread ID is resolved from:
//  1. The atomic write-once ID captured from thread.started (auto-capture)
//  2. session.Options[OptionResumeID] (explicit fallback)
//
// Returns an error if no thread ID is available, if the prompt
// contains null bytes, or if session options are invalid.
func (b *Backend) ResumeArgs(session agentrun.Session, initialPrompt string) (string, []string, error) {
	if err := validateSessionOptions(session.Options); err != nil {
		return "", nil, err
	}

	tid := b.resolveThreadID(session)
	if tid == "" {
		return "", nil, errors.New("codex: no thread ID available (not captured from thread.started and not set via OptionResumeID)")
	}
	if jsonutil.ContainsNull(tid) {
		return "", nil, errors.New("codex: thread ID contains null bytes")
	}
	if jsonutil.ContainsNull(initialPrompt) {
		return "", nil, errors.New("codex: initial prompt contains null bytes")
	}

	args := buildResumeCommand(tid, session)
	if initialPrompt != "" {
		args = append(args, initialPrompt)
	}
	return b.binary, args, nil
}

// ThreadID returns the auto-captured thread ID, or empty string if not yet
// captured or if only a non-UUID sentinel was stored.
func (b *Backend) ThreadID() string {
	if p := b.threadID.Load(); p != nil && *p != noUUIDSentinel {
		return *p
	}
	return ""
}

// resolveThreadID returns the thread ID from the atomic store (auto-capture)
// or from OptionResumeID. Stored ID takes precedence. Sentinel values are
// treated as empty (fall through to OptionResumeID).
func (b *Backend) resolveThreadID(session agentrun.Session) string {
	if p := b.threadID.Load(); p != nil && *p != noUUIDSentinel {
		return *p
	}
	return session.Options[agentrun.OptionResumeID]
}

// buildExecCommand builds args for: codex exec --json [exec-only] [common] [policy] -- <prompt>
func buildExecCommand(session agentrun.Session) []string {
	args := []string{subcmdExec, flagJSON}
	args = appendExecOnlyArgs(args, session)
	args = appendCommonArgs(args, session)
	args = appendExecPolicy(args, session.Options)

	// POSIX -- separator prevents prompt content from being parsed as flags.
	args = append(args, "--")
	if session.Prompt != "" && !jsonutil.ContainsNull(session.Prompt) {
		args = append(args, session.Prompt)
	}
	return args
}

// buildResumeCommand builds args for: codex exec resume --json [common] [--full-auto] -- <thread_id> [prompt]
// Does NOT append the prompt — caller adds it (SpawnArgs uses session.Prompt, ResumeArgs uses initialPrompt).
// Note: --sandbox is NOT supported on exec resume — sandbox policy is set on the first exec only.
func buildResumeCommand(threadID string, session agentrun.Session) []string {
	args := []string{subcmdExec, subcmdResume, flagJSON}
	args = appendCommonArgs(args, session)
	args = appendResumePolicy(args, session.Options)
	// POSIX -- separator prevents threadID/prompt from being parsed as flags.
	args = append(args, "--", threadID)
	return args
}

// codexEffort maps root Effort values to Codex model_reasoning_effort values.
// max → "xhigh" is a Codex-specific mapping.
var codexEffort = map[agentrun.Effort]string{
	agentrun.EffortLow:    "low",
	agentrun.EffortMedium: "medium",
	agentrun.EffortHigh:   "high",
	agentrun.EffortMax:    "xhigh",
}

// appendCommonArgs appends flags available on both exec and exec resume.
func appendCommonArgs(args []string, session agentrun.Session) []string {
	if m := session.Model; m != "" && !jsonutil.ContainsNull(m) && !strings.HasPrefix(m, "-") {
		args = append(args, "-m", m)
	}

	if session.Options[OptionEphemeral] != "" {
		args = append(args, "--ephemeral")
	}

	if session.Options[OptionSkipGitCheck] != "" {
		args = append(args, "--skip-git-repo-check")
	}

	// Effort: Codex supports low, medium, high, max (max → "xhigh").
	if e := agentrun.Effort(session.Options[agentrun.OptionEffort]); e != "" {
		if v, ok := codexEffort[e]; ok {
			args = append(args, "-c", "model_reasoning_effort="+v)
		}
	}

	// Additional directories.
	for _, dir := range agentrun.ParseListOption(session.Options, agentrun.OptionAddDirs) {
		if filepath.IsAbs(dir) && !strings.HasPrefix(dir, "-") {
			args = append(args, "--add-dir", dir)
		}
	}

	return args
}

// appendExecOnlyArgs appends flags only available on first-turn exec (not resume).
func appendExecOnlyArgs(args []string, session agentrun.Session) []string {
	if p := session.Options[OptionProfile]; p != "" && !jsonutil.ContainsNull(p) && !strings.HasPrefix(p, "-") {
		args = append(args, "-p", p)
	}

	if s := session.Options[OptionOutputSchema]; s != "" && !jsonutil.ContainsNull(s) && !strings.HasPrefix(s, "-") {
		args = append(args, "--output-schema", s)
	}

	return args
}

// appendResumePolicy appends only --full-auto for resume commands.
// Unlike exec, resume does NOT support --sandbox. The sandbox policy
// established on the first exec turn persists for the session.
func appendResumePolicy(args []string, opts map[string]string) []string {
	if resolveResumeFullAuto(opts) {
		args = append(args, "--full-auto")
	}
	return args
}

// resolveResumeFullAuto decides whether --full-auto applies on resume.
// ModePlan always suppresses --full-auto. Backend-specific OptionSandbox
// is not relevant (--sandbox is exec-only).
func resolveResumeFullAuto(opts map[string]string) bool {
	if !optutil.RootOptionsSet(opts) {
		return false
	}
	mode := agentrun.Mode(opts[agentrun.OptionMode])
	if mode == agentrun.ModePlan {
		return false
	}
	hitl := agentrun.HITL(opts[agentrun.OptionHITL])
	return hitl == agentrun.HITLOff
}

// appendExecPolicy appends the resolved --sandbox or --full-auto flag for exec (first turn).
func appendExecPolicy(args []string, opts map[string]string) []string {
	sandbox, fullAuto := resolveExecPolicy(opts)
	if sandbox != "" {
		args = append(args, "--sandbox", sandbox)
	}
	if fullAuto {
		args = append(args, "--full-auto")
	}
	return args
}

// resolveExecPolicy maps root-level OptionMode/OptionHITL and backend-specific
// OptionSandbox to --sandbox and --full-auto flags.
//
// Root options and backend options are independent control surfaces:
// when root options are set, OptionSandbox is ignored.
//
// Key invariant: ModePlan ALWAYS suppresses --full-auto because --full-auto
// implies --sandbox workspace-write, which would defeat plan-mode safety.
//
// Returns (sandboxValue, fullAuto).
func resolveExecPolicy(opts map[string]string) (string, bool) {
	mode := agentrun.Mode(opts[agentrun.OptionMode])
	hitl := agentrun.HITL(opts[agentrun.OptionHITL])

	if optutil.RootOptionsSet(opts) {
		// ModePlan wins — read-only sandbox, no full-auto.
		if mode == agentrun.ModePlan {
			return string(SandboxReadOnly), false
		}
		// HITLOff without ModePlan → full-auto (no explicit sandbox).
		if hitl == agentrun.HITLOff {
			return "", true
		}
		// act+on, just act, just hitl=on → default behavior.
		return "", false
	}

	// Root absent — use backend-specific options.
	var sandbox string
	if s := Sandbox(opts[OptionSandbox]); s != "" && validSandbox(s) && !jsonutil.ContainsNull(string(s)) {
		sandbox = string(s)
	}

	return sandbox, false
}

// validateSessionOptions performs strict validation of session options used
// by ResumeArgs. Checks mode, HITL, sandbox enum, and effort values.
func validateSessionOptions(opts map[string]string) error {
	if err := optutil.ValidateModeHITL("codex", opts); err != nil {
		return err
	}
	if err := validateSandboxIfNoRoot(opts); err != nil {
		return err
	}
	if e := agentrun.Effort(opts[agentrun.OptionEffort]); e != "" && !e.Valid() {
		return fmt.Errorf("codex: unknown effort %q: valid: low, medium, high, max", e)
	}
	return nil
}

// validateSandboxIfNoRoot validates OptionSandbox only when root options
// (OptionMode/OptionHITL) are absent — they are independent surfaces.
func validateSandboxIfNoRoot(opts map[string]string) error {
	if optutil.RootOptionsSet(opts) {
		return nil
	}
	s := Sandbox(opts[OptionSandbox])
	if s != "" && !validSandbox(s) {
		return fmt.Errorf("codex: unknown sandbox %q: valid: read-only, workspace-write, danger-full-access", s)
	}
	return nil
}
