package agy

import (
	"errors"
	"strings"
	"sync/atomic"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/internal/jsonutil"
	"github.com/dmora/agentrun/engine/cli/internal/optutil"
)

// Session option keys specific to the agy CLI backend.
const (
	OptionDangerouslySkipPermissions = "agy.dangerously_skip_permissions"
	OptionSandbox                    = "agy.sandbox"
)

const defaultBinary = "agy"

// shellWrapper is the sh -c script that runs agy and adapts it to the agentrun
// CLI engine. agy is plain-text and records the conversation ID only in its log
// file, so the wrapper:
//
//   - allocates its OWN temp log via mktemp (so SpawnArgs stays a pure, I/O-free
//     argument builder and nothing leaks on Engine.Validate or single-turn runs),
//   - runs agy in the background and forwards SIGTERM/SIGINT to it, so the engine
//     signalling sh on Stop/replacement actually terminates agy instead of
//     orphaning it,
//   - on clean exit, extracts "Created conversation <id>" from the log and emits
//     it as an {"type":"agy_session",...} sentinel for the parser to capture (so
//     ResumeArgs needs no file side channel), followed by the MessageResult
//     sentinel,
//   - always removes its temp log and preserves agy's exit code.
//
// The binary is passed as argv[0] ($0), never interpolated into the script, so
// WithBinary cannot inject shell metacharacters.
const shellWrapper = `__log=$(mktemp 2>/dev/null) || __log=
if [ -n "$__log" ]; then set -- --log-file "$__log" "$@"; fi
"$0" "$@" &
__pid=$!
trap 'kill -TERM "$__pid" 2>/dev/null' TERM INT
wait "$__pid"
__e=$?
trap - TERM INT
if [ "$__e" -eq 0 ]; then
  if [ -n "$__log" ]; then
    __cid=$(sed -n 's/.*Created conversation \([0-9a-f-][0-9a-f-]*\).*/\1/p' "$__log" 2>/dev/null | head -n 1)
    if [ -n "$__cid" ]; then printf '{"type":"agy_session","id":"%s"}\n' "$__cid"; fi
  fi
  printf '{"type":"result","stop_reason":"end_turn"}\n'
fi
if [ -n "$__log" ]; then rm -f "$__log"; fi
exit "$__e"`

// Backend is an Antigravity CLI backend for agentrun. It implements cli.Spawner,
// cli.Parser, and cli.Resumer for a spawn-per-turn model. The conversation ID is
// captured (write-once) from the wrapper's agy_session sentinel by ParseLine.
type Backend struct {
	binary   string
	resumeID atomic.Pointer[string]
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

// WithBinary overrides the Antigravity CLI binary path.
// Empty values are ignored; the default is "agy".
func WithBinary(path string) Option {
	return func(b *Backend) {
		if path != "" {
			b.binary = path
		}
	}
}

// New creates an agy CLI backend with the given options.
func New(opts ...Option) *Backend {
	b := &Backend{binary: defaultBinary}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// buildWrapperArgs wraps agyArgs + prompt in the sh -c invocation. The binary is
// passed as argv[0] ($0) to avoid shell injection via binary path metacharacters.
func (b *Backend) buildWrapperArgs(agyArgs []string, prompt string) (string, []string) {
	args := make([]string, 0, len(agyArgs)+5)
	// argv[0] = b.binary (becomes $0 in the script); remaining args become $@.
	args = append(args, "-c", shellWrapper, b.binary)
	args = append(args, agyArgs...)
	args = append(args, "--print", prompt)
	return "sh", args
}

// SpawnArgs builds exec.Cmd arguments for a new agy session. It is a pure
// argument builder with no side effects: the temporary log file is allocated by
// the shell wrapper at run time, not here (Engine.Validate also calls SpawnArgs).
func (b *Backend) SpawnArgs(session agentrun.Session) (string, []string) {
	agyArgs := appendSessionArgs(nil, session)

	prompt := session.Prompt
	if jsonutil.ContainsNull(prompt) {
		prompt = ""
	}

	return b.buildWrapperArgs(agyArgs, prompt)
}

// ResumeArgs builds exec.Cmd arguments to resume an existing agy session, using
// the conversation ID captured by ParseLine (or OptionResumeID as a fallback).
func (b *Backend) ResumeArgs(session agentrun.Session, initialPrompt string) (string, []string, error) {
	if jsonutil.ContainsNull(initialPrompt) {
		return "", nil, errors.New("agy: initial prompt contains null bytes")
	}
	if err := optutil.ValidateModeHITL("agy", session.Options); err != nil {
		return "", nil, err
	}

	uuid := b.resolveResumeID(session)
	if uuid == "" {
		return "", nil, errors.New("agy: no conversation ID available (not captured from output and OptionResumeID not set)")
	}

	agyArgs := []string{"--conversation", uuid}
	agyArgs = appendSessionArgs(agyArgs, session)

	binary, args := b.buildWrapperArgs(agyArgs, initialPrompt)
	return binary, args, nil
}

// resolveResumeID returns the auto-captured conversation ID (write-once from the
// agy_session sentinel) or the explicit OptionResumeID fallback.
func (b *Backend) resolveResumeID(session agentrun.Session) string {
	if p := b.resumeID.Load(); p != nil {
		return *p
	}
	return session.Options[agentrun.OptionResumeID]
}

func appendSessionArgs(args []string, session agentrun.Session) []string {
	if session.Model != "" && !jsonutil.ContainsNull(session.Model) && !strings.HasPrefix(session.Model, "-") {
		args = append(args, "--model", session.Model)
	}

	args = optutil.AppendAddDirs(args, session.Options, "--add-dir")
	args = appendPermissionArgs(args, session.Options)

	if sandbox, _, _ := agentrun.ParseBoolOption(session.Options, OptionSandbox); sandbox {
		args = append(args, "--sandbox")
	}

	return args
}

// appendPermissionArgs applies the HITL/permission flag following the
// "root set → backend-specific option ignored" precedence rule.
func appendPermissionArgs(args []string, opts map[string]string) []string {
	if hitl := opts[agentrun.OptionHITL]; hitl != "" {
		// Root OptionHITL governs; backend-specific option is ignored.
		if agentrun.HITL(hitl) == agentrun.HITLOff {
			args = append(args, "--dangerously-skip-permissions")
		}
		return args
	}
	// Root not set: use backend-specific option.
	if skip, _, _ := agentrun.ParseBoolOption(opts, OptionDangerouslySkipPermissions); skip {
		args = append(args, "--dangerously-skip-permissions")
	}
	return args
}
