//go:build !windows

package claude_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/claude"
	"github.com/dmora/agentrun/filter"
)

var (
	mockBuildOnce  sync.Once
	mockBinaryPath string
	errMockBuild   error
)

// buildMockBinary compiles the mock-streaming binary once, on first use.
// Called via sync.Once so unit tests in this package pay no build cost.
func buildMockBinary() {
	dir, err := os.MkdirTemp("", "mock-streaming-*")
	if err != nil {
		errMockBuild = fmt.Errorf("tmpdir: %w", err)
		return
	}
	mockBinaryPath = filepath.Join(dir, "mock-claude")
	cmd := exec.Command("go", "build", "-o", mockBinaryPath, "./testdata/mock-streaming/main.go")
	if out, err := cmd.CombinedOutput(); err != nil {
		errMockBuild = fmt.Errorf("build mock: %w: %s", err, out)
		os.RemoveAll(dir)
	}
}

const (
	streamingTimeout = 10 * time.Second
	mockTextContent  = "Hello world"
)

// startMockProc creates a mock Claude process, sends "test", and returns it.
// Lazily builds the mock binary on first call via sync.Once.
func startMockProc(t *testing.T) (agentrun.Process, context.Context) {
	t.Helper()
	mockBuildOnce.Do(buildMockBinary)
	if errMockBuild != nil {
		t.Fatalf("mock binary build failed: %v", errMockBuild)
	}
	backend := claude.New(claude.WithBinary(mockBinaryPath))
	engine := cli.NewEngine(backend)

	ctx, cancel := context.WithTimeout(context.Background(), streamingTimeout)
	t.Cleanup(cancel)

	session := agentrun.Session{CWD: t.TempDir()}
	proc, err := engine.Start(ctx, session)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Stop(context.Background()) })

	if err := proc.Send(ctx, "test"); err != nil {
		t.Fatalf("send: %v", err)
	}
	return proc, ctx
}

func TestStreaming_FullPipeline(t *testing.T) {
	proc, _ := startMockProc(t)
	msgs := collectMessages(proc.Output())

	if len(msgs) == 0 {
		t.Fatal("no messages received")
	}

	types := messageTypes(msgs)

	// Verify init arrives first.
	if types[0] != agentrun.MessageInit {
		t.Errorf("first message type = %q, want %q", types[0], agentrun.MessageInit)
	}

	verifyDeltaOrdering(t, types)

	// Verify delta content.
	deltaText := concatContent(msgs, agentrun.MessageTextDelta)
	if deltaText != mockTextContent {
		t.Errorf("concatenated deltas = %q, want %q", deltaText, mockTextContent)
	}

	// Verify complete text content matches.
	for _, m := range msgs {
		if m.Type == agentrun.MessageText && m.Content != mockTextContent {
			t.Errorf("complete text = %q, want %q", m.Content, mockTextContent)
		}
	}
}

// verifyDeltaOrdering checks that deltas arrive before text, and text before result.
func verifyDeltaOrdering(t *testing.T, types []agentrun.MessageType) {
	t.Helper()
	firstDelta, textIdx, resultIdx := findTypeIndices(types)
	if firstDelta == -1 {
		t.Fatal("no MessageTextDelta received")
	}
	if textIdx == -1 {
		t.Fatal("no MessageText received")
	}
	if resultIdx == -1 {
		t.Fatal("no MessageResult received")
	}
	if firstDelta >= textIdx {
		t.Errorf("first delta (idx %d) should arrive before text (idx %d)", firstDelta, textIdx)
	}
	if textIdx >= resultIdx {
		t.Errorf("text (idx %d) should arrive before result (idx %d)", textIdx, resultIdx)
	}
}

func TestStreaming_CompletedFilter(t *testing.T) {
	proc, ctx := startMockProc(t)
	msgs := collectMessages(filter.Completed(ctx, proc.Output()))

	for _, m := range msgs {
		if filter.IsDelta(m.Type) {
			t.Errorf("delta message %q should not pass through Completed filter", m.Type)
		}
	}

	typeSet := make(map[agentrun.MessageType]bool)
	for _, m := range msgs {
		typeSet[m.Type] = true
	}
	for _, want := range []agentrun.MessageType{agentrun.MessageInit, agentrun.MessageText, agentrun.MessageResult} {
		if !typeSet[want] {
			t.Errorf("missing %q in Completed output", want)
		}
	}
}

func TestStreaming_FilterMiddleware(t *testing.T) {
	proc, ctx := startMockProc(t)
	msgs := collectMessages(filter.Filter(ctx, proc.Output(), agentrun.MessageTextDelta, agentrun.MessageResult))

	for _, m := range msgs {
		if m.Type != agentrun.MessageTextDelta && m.Type != agentrun.MessageResult {
			t.Errorf("unexpected message type %q", m.Type)
		}
	}

	var hasDelta, hasResult bool
	for _, m := range msgs {
		if m.Type == agentrun.MessageTextDelta {
			hasDelta = true
		}
		if m.Type == agentrun.MessageResult {
			hasResult = true
		}
	}
	if !hasDelta {
		t.Error("missing MessageTextDelta")
	}
	if !hasResult {
		t.Error("missing MessageResult")
	}
}

func TestStreaming_AllDeltaTypes(t *testing.T) {
	proc, _ := startMockProc(t)
	msgs := collectMessages(proc.Output())

	// Verify all 3 delta types are present.
	typeSet := make(map[agentrun.MessageType]bool)
	for _, m := range msgs {
		typeSet[m.Type] = true
	}
	for _, want := range []agentrun.MessageType{
		agentrun.MessageTextDelta,
		agentrun.MessageToolUseDelta,
		agentrun.MessageThinkingDelta,
	} {
		if !typeSet[want] {
			t.Errorf("missing delta type %q", want)
		}
	}

	// Verify concatenated content for each delta type.
	if got := concatContent(msgs, agentrun.MessageThinkingDelta); got != "Let me think" {
		t.Errorf("thinking deltas = %q, want %q", got, "Let me think")
	}
	if got := concatContent(msgs, agentrun.MessageToolUseDelta); got != `{"path":"foo.txt"}` {
		t.Errorf("tool use deltas = %q, want %q", got, `{"path":"foo.txt"}`)
	}
	if got := concatContent(msgs, agentrun.MessageTextDelta); got != mockTextContent {
		t.Errorf("text deltas = %q, want %q", got, mockTextContent)
	}
}

// collectMessages drains a message channel into a slice.
func collectMessages(ch <-chan agentrun.Message) []agentrun.Message {
	var msgs []agentrun.Message
	for msg := range ch {
		msgs = append(msgs, msg)
	}
	return msgs
}

// messageTypes extracts the Type field from each message.
func messageTypes(msgs []agentrun.Message) []agentrun.MessageType {
	types := make([]agentrun.MessageType, len(msgs))
	for i, m := range msgs {
		types[i] = m.Type
	}
	return types
}

// findTypeIndices returns the indices of the first TextDelta, last Text, and last Result
// in a type slice. Returns -1 for any type not found.
func findTypeIndices(types []agentrun.MessageType) (firstDelta, textIdx, resultIdx int) {
	firstDelta, textIdx, resultIdx = -1, -1, -1
	for i, mt := range types {
		if mt == agentrun.MessageTextDelta && firstDelta == -1 {
			firstDelta = i
		}
		if mt == agentrun.MessageText {
			textIdx = i
		}
		if mt == agentrun.MessageResult {
			resultIdx = i
		}
	}
	return
}

// concatContent concatenates Content from all messages of the given type.
func concatContent(msgs []agentrun.Message, mt agentrun.MessageType) string {
	var b strings.Builder
	for _, m := range msgs {
		if m.Type == mt {
			b.WriteString(m.Content)
		}
	}
	return b.String()
}
