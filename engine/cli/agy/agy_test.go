package agy

import (
	"os"
	"strings"
	"testing"

	"github.com/dmora/agentrun"
)

func TestBackend_SpawnArgs(t *testing.T) {
	b := New()
	session := agentrun.Session{
		Prompt: "hello world",
		Model:  "gemini-test",
		Options: map[string]string{
			OptionDangerouslySkipPermissions: "true",
		},
	}

	bin, args := b.SpawnArgs(session)
	if bin != "sh" {
		t.Errorf("SpawnArgs binary = %q, want sh", bin)
	}

	// args[2] is the binary (argv[0]/$0 inside the script), not a literal "sh".
	if len(args) < 3 || args[0] != "-c" || args[2] != "agy" {
		t.Fatalf("SpawnArgs wrapper shell signature mismatch: %q", args)
	}

	wrapperScript := args[1]
	for _, want := range []string{
		`"$0" "$@"`,            // injection-safe invocation
		`mktemp`,               // wrapper self-manages its log (no Go-side temp file)
		`trap`,                 // signal forwarding to the agy child
		resultSentinel,         // MessageResult sentinel
		`"type":"agy_session"`, // conversation-ID sentinel
	} {
		if !strings.Contains(wrapperScript, want) {
			t.Errorf("wrapper script missing %q:\n%s", want, wrapperScript)
		}
	}

	// SpawnArgs must NOT inject --log-file (the wrapper allocates it at run time)
	// and must be a pure builder.
	agyArgs := args[3:]
	for _, a := range agyArgs {
		if a == "--log-file" {
			t.Errorf("SpawnArgs should not inject --log-file; wrapper manages it: %q", agyArgs)
		}
	}

	assertContainsPair(t, agyArgs, "--model", "gemini-test")
	if agyArgs[len(agyArgs)-2] != "--print" || agyArgs[len(agyArgs)-1] != "hello world" {
		t.Errorf("prompt not appended as final --print arg: %q", agyArgs)
	}
	if !containsArg(agyArgs, "--dangerously-skip-permissions") {
		t.Errorf("missing skip-permissions flag: %q", agyArgs)
	}
}

// TestBackend_SpawnArgs_PureNoSideEffects guards the Spawner contract: SpawnArgs
// is called by Engine.Validate with a zero Session and again at Start, so it must
// build args without creating files or mutating capture state.
func TestBackend_SpawnArgs_PureNoSideEffects(t *testing.T) {
	before := countTempLogs(t)
	b := New()
	b.SpawnArgs(agentrun.Session{})            // Validate-style call
	b.SpawnArgs(agentrun.Session{Prompt: "x"}) // Start-style call
	if b.resumeID.Load() != nil {
		t.Error("SpawnArgs must not set the captured resume ID")
	}
	if after := countTempLogs(t); after != before {
		t.Errorf("SpawnArgs created temp log files: before=%d after=%d", before, after)
	}
}

func TestBackend_ResumeArgs_CapturedID(t *testing.T) {
	b := New()
	// Simulate turn 1: the wrapper's agy_session sentinel is parsed, capturing
	// the conversation ID and surfacing it as MessageInit.
	const id = "d8e79181-5db2-4ea9-88e2-eea15ddab587"
	msg, err := b.ParseLine(`{"type":"agy_session","id":"` + id + `"}`)
	if err != nil {
		t.Fatalf("agy_session sentinel: unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit || msg.ResumeID != id {
		t.Fatalf("agy_session sentinel: got Type=%v ResumeID=%q, want MessageInit/%q", msg.Type, msg.ResumeID, id)
	}
	if got := b.resumeID.Load(); got == nil || *got != id {
		t.Fatalf("conversation ID not captured: %v", got)
	}

	bin, args, err := b.ResumeArgs(agentrun.Session{}, "turn 2")
	if err != nil {
		t.Fatalf("ResumeArgs: %v", err)
	}
	if bin != "sh" {
		t.Errorf("ResumeArgs binary = %q, want sh", bin)
	}
	agyArgs := args[3:]
	assertContainsPair(t, agyArgs, "--conversation", id)
	if agyArgs[len(agyArgs)-1] != "turn 2" {
		t.Errorf("resume prompt not appended: %q", agyArgs)
	}
}

// TestBackend_ResumeArgs_MalformedCapturedID guards the isConversationID split:
// a structurally-recognized sentinel with a non-canonical id (charset-valid
// per agy.go's sed capture, but not 36 chars) must still be consumed as
// plumbing — never leaked as MessageText — but must not be trusted for
// auto-resume.
func TestBackend_ResumeArgs_MalformedCapturedID(t *testing.T) {
	b := New()
	msg, err := b.ParseLine(`{"type":"agy_session","id":"d8e79181"}`)
	if err != nil {
		t.Fatalf("agy_session sentinel: unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageInit {
		t.Fatalf("agy_session sentinel with malformed id: Type = %v, want MessageInit", msg.Type)
	}
	if msg.ResumeID != "" {
		t.Errorf("agy_session sentinel with malformed id: ResumeID = %q, want empty", msg.ResumeID)
	}
	if got := b.resumeID.Load(); got != nil {
		t.Errorf("malformed id must not be stored: %v", *got)
	}

	if _, _, err := b.ResumeArgs(agentrun.Session{}, "msg"); err == nil {
		t.Error("ResumeArgs after malformed id capture should still error (nothing usable was stored)")
	}
}

func TestBackend_ResumeArgs_OptionFallback(t *testing.T) {
	b := New()
	const id = "a1b2c3d4-e5f6-7a8b-9c0d-e1f2a3b4c5d6"
	session := agentrun.Session{Options: map[string]string{agentrun.OptionResumeID: id}}
	_, args, err := b.ResumeArgs(session, "msg")
	if err != nil {
		t.Fatalf("ResumeArgs: %v", err)
	}
	assertContainsPair(t, args[3:], "--conversation", id)
}

func TestBackend_ResumeArgs_NoID(t *testing.T) {
	b := New()
	if _, _, err := b.ResumeArgs(agentrun.Session{}, "msg"); err == nil {
		t.Error("ResumeArgs with no captured ID and no OptionResumeID should error")
	}
}

func TestBackend_ResumeArgs_NullBytePrompt(t *testing.T) {
	b := New()
	if _, _, err := b.ResumeArgs(agentrun.Session{}, "bad\x00prompt"); err == nil {
		t.Error("ResumeArgs with null-byte prompt should error")
	}
}

// assertContainsPair checks that args contains flag immediately followed by value.
func assertContainsPair(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return
		}
	}
	t.Errorf("args missing %q %q: %q", flag, value, args)
}

func containsArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// countTempLogs counts agy-*.log files in the OS temp dir (used to assert
// SpawnArgs creates none).
func countTempLogs(t *testing.T) int {
	t.Helper()
	matches, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	n := 0
	for _, e := range matches {
		if strings.HasPrefix(e.Name(), "agy-") && strings.HasSuffix(e.Name(), ".log") {
			n++
		}
	}
	return n
}
