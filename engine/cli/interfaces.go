package cli

import (
	"errors"

	"github.com/dmora/agentrun"
)

// ErrSkipLine is returned by Parser.ParseLine when a line should be
// consumed but produces no Message (blank lines, heartbeats, comments).
// The CLIEngine checks errors.Is(err, ErrSkipLine) and continues iteration.
var ErrSkipLine = errors.New("cli: skip line")

// Spawner builds the exec.Cmd arguments to start a new session.
// Every CLI backend must implement Spawner.
//
// SpawnArgs is a pure argument builder — it must not fail. Backends that
// need to validate session state should do so in their constructor or in
// Engine.Validate. Implementations MUST return arguments suitable for
// exec.Cmd (pre-split argv) and MUST NOT pass arguments through a shell
// interpreter.
type Spawner interface {
	SpawnArgs(session agentrun.Session) (binary string, args []string)
}

// Parser converts a raw stdout line into a structured Message.
// Every CLI backend must implement Parser.
//
// ParseLine returns ErrSkipLine when a line should be consumed but
// produces no Message. Any other non-nil error indicates a parse failure.
// A returned Message with zero Timestamp will have its Timestamp set by
// the CLIEngine.
type Parser interface {
	ParseLine(line string) (agentrun.Message, error)
}

// Resumer builds the exec.Cmd arguments to resume an existing session.
// Resumer is optional — the CLIEngine discovers it via type assertion:
//
//	if r, ok := backend.(Resumer); ok {
//	    binary, args, err := r.ResumeArgs(session, "continue from here")
//	}
//
// The initialPrompt parameter is baked into the subprocess args because
// some CLI tools (e.g., Claude Code's --resume flag) require the first
// message at spawn time (single-shot resume pattern).
type Resumer interface {
	ResumeArgs(session agentrun.Session, initialPrompt string) (binary string, args []string, err error)
}

// Streamer builds a long-lived streaming command that attaches to a
// subprocess for continuous output. When a backend implements Streamer,
// the initial prompt is NOT included in the command args — the caller
// must send it explicitly via Process.Send after Start returns.
// Streamer is optional — the CLIEngine discovers it via type assertion:
//
//	if s, ok := backend.(Streamer); ok {
//	    binary, args := s.StreamArgs(session)
//	}
type Streamer interface {
	StreamArgs(session agentrun.Session) (binary string, args []string)
}

// Backend is the minimum interface a CLI backend must implement.
// Optional capabilities (Resumer, Streamer, InputFormatter) are
// discovered via type assertion at runtime.
type Backend interface {
	Spawner
	Parser
}

// InputFormatter encodes user messages for delivery to a subprocess stdin
// pipe. InputFormatter is optional — the CLIEngine discovers it via type
// assertion independently of Streamer:
//
//	if f, ok := backend.(InputFormatter); ok {
//	    data, err := f.FormatInput("user message")
//	}
//
// A backend can implement Streamer, InputFormatter, both, or neither.
// message must be valid UTF-8. Implementations must return an error for
// messages containing null bytes.
type InputFormatter interface {
	FormatInput(message string) ([]byte, error)
}
