package agy

import (
	"os"
	"path/filepath"
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
		t.Errorf("SpawnArgs wrapper shell signature mismatch: %q", args)
	}

	wrapperScript := args[1]
	if !strings.Contains(wrapperScript, `"$0" "$@"`) {
		t.Errorf("Wrapper script missing injection-safe invocation: %s", wrapperScript)
	}
	if !strings.Contains(wrapperScript, `{"type":"result","stop_reason":"end_turn"}`) {
		t.Errorf("Wrapper script missing MessageResult sentinel: %s", wrapperScript)
	}

	// The remaining args are passed to agy
	agyArgs := args[3:]

	// Check log file injection
	if len(agyArgs) < 2 || agyArgs[0] != "--log-file" {
		t.Errorf("Missing --log-file injection: %q", agyArgs)
	}
	if b.logFile == "" {
		t.Error("Backend.logFile not set by SpawnArgs")
	}

	// Check model
	foundModel := false
	for i, arg := range agyArgs {
		if arg == "--model" && i+1 < len(agyArgs) && agyArgs[i+1] == "gemini-test" {
			foundModel = true
		}
	}
	if !foundModel {
		t.Errorf("Missing --model flag: %q", agyArgs)
	}

	// Check prompt and skip perms
	if agyArgs[len(agyArgs)-2] != "--print" || agyArgs[len(agyArgs)-1] != "hello world" {
		t.Errorf("Prompt not properly appended: %q", agyArgs)
	}

	foundSkip := false
	for _, arg := range agyArgs {
		if arg == "--dangerously-skip-permissions" {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Errorf("Missing skip permissions flag: %q", agyArgs)
	}
}

func TestBackend_ResumeArgs(t *testing.T) {
	b := New()

	// Mock a log file
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")
	logContent := "server.go:753] Created conversation d8e79181-5db2-4ea9-88e2-eea15ddab587\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0600); err != nil {
		t.Fatal(err)
	}

	b.logFile = logPath

	session := agentrun.Session{
		Options: map[string]string{},
	}

	bin, args, err := b.ResumeArgs(session, "turn 2")
	if err != nil {
		t.Fatalf("ResumeArgs failed: %v", err)
	}

	if bin != "sh" {
		t.Errorf("ResumeArgs binary = %q, want sh", bin)
	}

	agyArgs := args[3:]
	if len(agyArgs) < 2 || agyArgs[0] != "--conversation" || agyArgs[1] != "d8e79181-5db2-4ea9-88e2-eea15ddab587" {
		t.Errorf("ResumeArgs did not properly parse/inject conversation ID: %q", agyArgs)
	}

	// Verify log file was deleted
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("Log file was not deleted after ResumeArgs")
	}

	// Second resume should use atomic pointer
	_, args2, err := b.ResumeArgs(session, "turn 3")
	if err != nil {
		t.Fatalf("Second ResumeArgs failed: %v", err)
	}
	agyArgs2 := args2[3:]
	if len(agyArgs2) < 2 || agyArgs2[0] != "--conversation" || agyArgs2[1] != "d8e79181-5db2-4ea9-88e2-eea15ddab587" {
		t.Errorf("Second ResumeArgs did not reuse conversation ID: %q", agyArgs2)
	}
}
