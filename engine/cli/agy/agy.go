package agy

import (
	"errors"
	"os"
	"regexp"
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

// validConversationID matches the UUID format used by agy conversation IDs.
var validConversationID = regexp.MustCompile(
	`Created conversation ([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`,
)

// shellWrapper is the sh -c script that runs agy (via $0) and emits a JSON
// MessageResult sentinel on clean exit. Using "$0" instead of embedding the
// binary path in the script prevents shell injection via WithBinary.
const shellWrapper = `"$0" "$@"; _E=$?; [ $_E -eq 0 ] && printf '{"type":"result","stop_reason":"end_turn"}\n'; exit $_E`

// Backend is an Antigravity CLI backend for agentrun.
// It implements cli.Spawner, cli.Parser, and cli.Resumer for a spawn-per-turn model.
type Backend struct {
	binary   string
	logFile  string // path to the temporary log file for capturing the session ID
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

// buildWrapperArgs wraps agyArgs + prompt in a sh -c invocation that emits
// the MessageResult sentinel on success. The binary is passed as argv[0] ($0)
// to avoid shell injection via binary path metacharacters.
func (b *Backend) buildWrapperArgs(agyArgs []string, prompt string) (string, []string) {
	args := make([]string, 0, len(agyArgs)+5)
	// argv[0] = b.binary (becomes $0 in the script); remaining args become $@.
	args = append(args, "-c", shellWrapper, b.binary)
	args = append(args, agyArgs...)
	args = append(args, "--print", prompt)
	return "sh", args
}

// SpawnArgs builds exec.Cmd arguments for a new agy session.
func (b *Backend) SpawnArgs(session agentrun.Session) (string, []string) {
	// Create a temp log file so agy records the new conversation ID.
	// If creation fails we omit --log-file; ResumeArgs falls back to OptionResumeID.
	if f, err := os.CreateTemp("", "agy-*.log"); err == nil {
		f.Close()
		b.logFile = f.Name()
	}

	var agyArgs []string
	if b.logFile != "" {
		agyArgs = append(agyArgs, "--log-file", b.logFile)
	}
	agyArgs = appendSessionArgs(agyArgs, session)

	prompt := session.Prompt
	if jsonutil.ContainsNull(prompt) {
		prompt = ""
	}

	return b.buildWrapperArgs(agyArgs, prompt)
}

// ResumeArgs builds exec.Cmd arguments to resume an existing agy session.
func (b *Backend) ResumeArgs(session agentrun.Session, initialPrompt string) (string, []string, error) {
	if jsonutil.ContainsNull(initialPrompt) {
		return "", nil, errors.New("agy: initial prompt contains null bytes")
	}
	if err := optutil.ValidateModeHITL("agy", session.Options); err != nil {
		return "", nil, err
	}

	// Determine the conversation UUID to resume.
	var uuid string
	if captured := b.resumeID.Load(); captured != nil {
		uuid = *captured
	}

	if uuid == "" && b.logFile != "" {
		// First resume: read the log file written by SpawnArgs to get the UUID.
		if data, err := os.ReadFile(b.logFile); err == nil {
			if m := validConversationID.FindSubmatch(data); len(m) == 2 {
				uuid = string(m[1])
				b.resumeID.Store(&uuid)
			}
		}
		_ = os.Remove(b.logFile)
		b.logFile = ""
	}

	// Fallback to explicitly-provided resume ID.
	if uuid == "" {
		uuid = session.Options[agentrun.OptionResumeID]
	}

	if uuid == "" {
		return "", nil, errors.New("agy: no conversation ID available (not captured from log and OptionResumeID not set)")
	}

	agyArgs := []string{"--conversation", uuid}
	agyArgs = appendSessionArgs(agyArgs, session)

	binary, args := b.buildWrapperArgs(agyArgs, initialPrompt)
	return binary, args, nil
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
