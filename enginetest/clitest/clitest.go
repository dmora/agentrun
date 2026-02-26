package clitest

import (
	"errors"
	"strings"
	"testing"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// universalResumeID is a session ID that passes all current backend validators:
//   - Claude: ^[a-zA-Z0-9_-]{1,128}$
//   - OpenCode: ^ses_[a-zA-Z0-9]{20,40}$
//   - Codex: any non-empty, non-null string
const universalResumeID = "ses_abcdefghij1234567890abcd"

// RunBackendTests runs all applicable compliance suites for a [cli.Backend].
// Optional capabilities ([cli.Resumer], [cli.Streamer], [cli.InputFormatter])
// are discovered via type assertion — mirroring how the CLIEngine resolves
// capabilities at Start time.
func RunBackendTests(t *testing.T, factory func() cli.Backend) {
	t.Helper()

	t.Run("Spawner", func(t *testing.T) {
		RunSpawnerTests(t, func() cli.Spawner { return factory() })
	})
	t.Run("Parser", func(t *testing.T) {
		RunParserTests(t, func() cli.Parser { return factory() })
	})

	probe := factory()
	if _, ok := probe.(cli.Resumer); ok {
		t.Run("Resumer", func(t *testing.T) {
			RunResumerTests(t, func() cli.Resumer { return factory().(cli.Resumer) })
		})
	}

	// Streamer: stub for future use.
	if _, ok := probe.(cli.Streamer); ok {
		t.Run("Streamer", func(t *testing.T) {
			t.Skip("Streamer compliance tests not yet implemented")
		})
	}

	// InputFormatter: stub for future use.
	if _, ok := probe.(cli.InputFormatter); ok {
		t.Run("InputFormatter", func(t *testing.T) {
			t.Skip("InputFormatter compliance tests not yet implemented")
		})
	}
}

// RunSpawnerTests tests the [cli.Spawner] behavioral contract.
// The factory is called once per subtest to ensure fresh backend state.
func RunSpawnerTests(t *testing.T, factory func() cli.Spawner) {
	t.Helper()
	runSpawnerStructural(t, factory)
	runSpawnerSafety(t, factory)
}

// runSpawnerStructural tests structural invariants: non-empty binary, non-nil args.
func runSpawnerStructural(t *testing.T, factory func() cli.Spawner) {
	t.Helper()

	t.Run("ZeroSession", func(t *testing.T) {
		s := factory()
		binary, args := s.SpawnArgs(agentrun.Session{})
		if binary == "" {
			t.Error("binary must be non-empty")
		}
		if args == nil {
			t.Error("args must be non-nil")
		}
	})

	t.Run("BinaryNonEmpty", func(t *testing.T) {
		s := factory()
		binary, _ := s.SpawnArgs(agentrun.Session{Prompt: "hello"})
		if binary == "" {
			t.Error("binary must be non-empty")
		}
	})

	t.Run("BinaryNoNullBytes", func(t *testing.T) {
		s := factory()
		binary, _ := s.SpawnArgs(agentrun.Session{Prompt: "hello"})
		if strings.Contains(binary, "\x00") {
			t.Error("binary must not contain null bytes")
		}
	})

	t.Run("ArgsNonNil", func(t *testing.T) {
		s := factory()
		_, args := s.SpawnArgs(agentrun.Session{Prompt: "hello"})
		if args == nil {
			t.Error("args must be non-nil")
		}
	})
}

// runSpawnerSafety tests safety contracts: null-byte defense, leading-dash defense.
func runSpawnerSafety(t *testing.T, factory func() cli.Spawner) {
	t.Helper()

	t.Run("NoNullBytesInArgs", func(t *testing.T) {
		s := factory()
		_, args := s.SpawnArgs(agentrun.Session{Prompt: "hello", Model: "test-model"})
		if i, ok := indexNullArg(args); ok {
			t.Errorf("args[%d] contains null bytes", i)
		}
	})

	t.Run("NullBytePromptExcluded", func(t *testing.T) {
		s := factory()
		_, args := s.SpawnArgs(agentrun.Session{Prompt: "hello\x00world"})
		if containsArg(args, "hello\x00world") {
			t.Error("null-byte prompt must not appear in args")
		}
	})

	t.Run("NullByteModelExcluded", func(t *testing.T) {
		s := factory()
		_, args := s.SpawnArgs(agentrun.Session{Prompt: "hello", Model: "gpt\x00evil"})
		if containsArg(args, "gpt\x00evil") {
			t.Error("null-byte model must not appear in args")
		}
	})

	t.Run("LeadingDashModelExcluded", func(t *testing.T) {
		s := factory()
		_, args := s.SpawnArgs(agentrun.Session{Prompt: "hello", Model: "-evil"})
		if containsArg(args, "-evil") {
			t.Error("leading-dash model must not appear as a standalone arg")
		}
		if containsArg(args, "--model") || containsArg(args, "-m") {
			t.Error("model flag must be omitted entirely for leading-dash model")
		}
	})
}

// RunParserTests tests the [cli.Parser] behavioral contract.
// Assertions use [errors.Is] to match how the CLIEngine checks parser results.
// The factory is called once per subtest to ensure fresh backend state.
func RunParserTests(t *testing.T, factory func() cli.Parser) {
	t.Helper()
	runParserErrors(t, factory)
	runParserRobustness(t, factory)
}

// runParserErrors tests error-path semantics: ErrSkipLine vs real errors.
func runParserErrors(t *testing.T, factory func() cli.Parser) {
	t.Helper()

	t.Run("EmptyLineReturnsErrSkipLine", func(t *testing.T) {
		p := factory()
		_, err := p.ParseLine("")
		if !errors.Is(err, cli.ErrSkipLine) {
			t.Errorf("ParseLine(\"\") error = %v, want ErrSkipLine", err)
		}
	})

	t.Run("WhitespaceOnlyReturnsErrSkipLine", func(t *testing.T) {
		p := factory()
		_, err := p.ParseLine("   ")
		if !errors.Is(err, cli.ErrSkipLine) {
			t.Errorf("ParseLine(\"   \") error = %v, want ErrSkipLine", err)
		}
	})

	t.Run("InvalidJSONReturnsNonSkipError", func(t *testing.T) {
		p := factory()
		_, err := p.ParseLine("not json")
		if err == nil {
			t.Error("ParseLine(\"not json\") should return an error")
		}
		if errors.Is(err, cli.ErrSkipLine) {
			t.Error("ParseLine(\"not json\") should return a non-skip error, got ErrSkipLine")
		}
	})
}

// garbageCorpus is a fixed set of adversarial inputs used by robustness tests.
var garbageCorpus = []string{
	"\x00",
	strings.Repeat("x", 65536),
	"{{{",
	"\xff\xfe",
	`{"":null}`,
	"null",
	"[]",
}

// runParserRobustness tests no-panic guarantees and guard invariants.
func runParserRobustness(t *testing.T, factory func() cli.Parser) {
	t.Helper()

	t.Run("TypeFieldWrongTypeNoPanic", func(t *testing.T) { //nolint:revive // no assertions — panics are the failure signal
		_ = t
		p := factory()
		for _, input := range []string{`{"type":99}`, `{"type":true}`, `{"type":[]}`} {
			_, _ = p.ParseLine(input)
		}
	})

	t.Run("GarbageNoPanic", func(t *testing.T) { //nolint:revive // no assertions — panics are the failure signal
		_ = t
		p := factory()
		for _, input := range garbageCorpus {
			_, _ = p.ParseLine(input)
		}
	})

	t.Run("ValidMessageHasType", func(t *testing.T) {
		// Guard invariant: if any input accidentally parses into a
		// valid Message (nil error, not ErrSkipLine), that message
		// must have a non-empty Type.
		p := factory()
		corpus := make([]string, 0, len(garbageCorpus)+2)
		corpus = append(corpus, garbageCorpus...)
		corpus = append(corpus, `{"type":99}`, `{"type":"unknown"}`)
		for _, input := range corpus {
			msg, err := p.ParseLine(input)
			if err == nil && msg.Type == "" {
				t.Errorf("ParseLine(%q) returned msg with empty Type and nil error", input)
			}
		}
	})
}

// RunResumerTests tests the [cli.Resumer] behavioral contract.
// The factory is called once per subtest to ensure fresh backend state.
func RunResumerTests(t *testing.T, factory func() cli.Resumer) {
	t.Helper()

	t.Run("NoResumeID", func(t *testing.T) {
		r := factory()
		_, _, err := r.ResumeArgs(agentrun.Session{}, "hello")
		if err == nil {
			t.Error("ResumeArgs with no resume ID should return an error")
		}
	})

	t.Run("NullByteMessage", func(t *testing.T) {
		r := factory()
		_, _, err := r.ResumeArgs(agentrun.Session{
			Options: map[string]string{agentrun.OptionResumeID: universalResumeID},
		}, "hello\x00world")
		if err == nil {
			t.Error("ResumeArgs with null-byte message should return an error")
		}
	})

	t.Run("ValidResume", func(t *testing.T) {
		r := factory()
		binary, args, err := r.ResumeArgs(agentrun.Session{
			Options: map[string]string{agentrun.OptionResumeID: universalResumeID},
		}, "hello")
		if err != nil {
			t.Fatalf("ResumeArgs with valid resume ID should not error: %v", err)
		}
		if binary == "" {
			t.Error("binary must be non-empty")
		}
		if args == nil {
			t.Error("args must be non-nil")
		}
		if !containsArg(args, universalResumeID) {
			t.Errorf("args %v must contain resume ID %q", args, universalResumeID)
		}
	})
}

// containsArg reports whether args contains s as an exact element.
func containsArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// indexNullArg returns the index of the first arg containing a null byte.
func indexNullArg(args []string) (int, bool) {
	for i, a := range args {
		if strings.Contains(a, "\x00") {
			return i, true
		}
	}
	return 0, false
}
