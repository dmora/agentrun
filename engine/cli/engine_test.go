//go:build !windows

package cli_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

const (
	binEcho  = "echo"
	binSleep = "sleep"
	binBash  = "bash"
	binCat   = "cat"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func tempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// drain collects all messages from a process output channel.
func drain(p agentrun.Process) []agentrun.Message {
	msgs := make([]agentrun.Message, 0, 8)
	for m := range p.Output() {
		msgs = append(msgs, m)
	}
	return msgs
}

// textParser parses each line as a text message.
func textParser(line string) (agentrun.Message, error) {
	return agentrun.Message{Type: agentrun.MessageText, Content: line}, nil
}

// ---------------------------------------------------------------------------
// Stub backends (function-field injection)
// ---------------------------------------------------------------------------

type testBackend struct {
	spawnFn func(agentrun.Session) (string, []string)
	parseFn func(string) (agentrun.Message, error)
}

func (b *testBackend) SpawnArgs(s agentrun.Session) (string, []string) { return b.spawnFn(s) }
func (b *testBackend) ParseLine(line string) (agentrun.Message, error) { return b.parseFn(line) }

type testResumerBackend struct {
	testBackend
	resumeFn func(agentrun.Session, string) (string, []string, error)
}

func (b *testResumerBackend) ResumeArgs(s agentrun.Session, prompt string) (string, []string, error) {
	return b.resumeFn(s, prompt)
}

type testStreamerBackend struct {
	testBackend
	streamFn func(agentrun.Session) (string, []string)
	formatFn func(string) ([]byte, error)
}

func (b *testStreamerBackend) StreamArgs(s agentrun.Session) (string, []string) {
	return b.streamFn(s)
}

func (b *testStreamerBackend) FormatInput(msg string) ([]byte, error) {
	return b.formatFn(msg)
}

type testStreamerOnlyBackend struct {
	testBackend
	streamFn func(agentrun.Session) (string, []string)
}

func (b *testStreamerOnlyBackend) StreamArgs(s agentrun.Session) (string, []string) {
	return b.streamFn(s)
}

// testStreamerResumerBackend has Streamer+Resumer but no InputFormatter.
// Used to test that Start falls back to SpawnArgs mode when the streaming
// path is incomplete.
type testStreamerResumerBackend struct {
	testBackend
	streamFn func(agentrun.Session) (string, []string)
	resumeFn func(agentrun.Session, string) (string, []string, error)
}

func (b *testStreamerResumerBackend) StreamArgs(s agentrun.Session) (string, []string) {
	return b.streamFn(s)
}

func (b *testStreamerResumerBackend) ResumeArgs(s agentrun.Session, prompt string) (string, []string, error) {
	return b.resumeFn(s, prompt)
}

// echoBackend returns a minimal backend (Spawner+Parser only) that spawns
// "echo" with session.Prompt. Has no send capability — Start() will reject it.
// Use echoResumerBackend() for tests that need Start() to succeed.
func echoBackend() *testBackend {
	return &testBackend{
		spawnFn: func(s agentrun.Session) (string, []string) {
			return binEcho, []string{s.Prompt}
		},
		parseFn: textParser,
	}
}

// echoResumerBackend returns a backend with Resumer capability that spawns
// "echo" with session.Prompt. Satisfies Start()'s send capability requirement.
func echoResumerBackend() *testResumerBackend {
	return withResumer(*echoBackend())
}

// withResumer wraps a testBackend with Resumer capability, using spawnFn
// as the resume function.
func withResumer(tb testBackend) *testResumerBackend {
	return &testResumerBackend{
		testBackend: tb,
		resumeFn: func(s agentrun.Session, _ string) (string, []string, error) {
			bin, args := tb.spawnFn(s)
			return bin, args, nil
		},
	}
}

// ---------------------------------------------------------------------------
// Compile-time checks
// ---------------------------------------------------------------------------

var _ agentrun.Engine = (*cli.Engine)(nil)

// ---------------------------------------------------------------------------
// Validate tests
// ---------------------------------------------------------------------------

func TestValidate_Found(t *testing.T) {
	b := &testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) { return binEcho, nil },
		parseFn: textParser,
	}
	eng := cli.NewEngine(b)
	if err := eng.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidate_NotFound(t *testing.T) {
	b := &testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return "nonexistent-binary-xyz-999", nil
		},
		parseFn: textParser,
	}
	eng := cli.NewEngine(b)
	err := eng.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, agentrun.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestValidate_PanicRecovery(t *testing.T) {
	b := &testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) { panic("boom") },
		parseFn: textParser,
	}
	eng := cli.NewEngine(b)
	err := eng.Validate()
	if err == nil {
		t.Fatal("expected error from panic")
	}
	if !errors.Is(err, agentrun.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("expected panic message, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Start tests
// ---------------------------------------------------------------------------

func TestStart_Echo(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	ctx := testCtx(t)

	p, err := eng.Start(ctx, agentrun.Session{
		CWD:    tempDir(t),
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Fatalf("expected 'hello', got %q", msgs[0].Content)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestStart_MultiLine(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return "printf", []string{"line1\nline2\nline3\n"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %v", len(msgs), msgs)
	}
}

func TestStart_OptionOverrides(t *testing.T) {
	tests := []struct {
		name    string
		session agentrun.Session
		opt     agentrun.Option
		extract func(agentrun.Session) string
		want    string
	}{
		{
			name:    "Prompt",
			session: agentrun.Session{Prompt: "original"},
			opt:     agentrun.WithPrompt("override"),
			extract: func(s agentrun.Session) string { return s.Prompt },
			want:    "override",
		},
		{
			name:    "Model",
			session: agentrun.Session{Model: "original-model"},
			opt:     agentrun.WithModel("override-model"),
			extract: func(s agentrun.Session) string { return s.Model },
			want:    "override-model",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string
			b := withResumer(testBackend{
				spawnFn: func(s agentrun.Session) (string, []string) {
					captured = tt.extract(s)
					return binEcho, []string{"x"}
				},
				parseFn: textParser,
			})
			tt.session.CWD = tempDir(t)
			eng := cli.NewEngine(b)
			p, err := eng.Start(testCtx(t), tt.session, tt.opt)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			drain(p)
			_ = p.Wait()

			if captured != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, captured)
			}
		})
	}
}

func TestStart_InvalidCWD_Empty(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	_, err := eng.Start(testCtx(t), agentrun.Session{CWD: ""})
	if err == nil {
		t.Fatal("expected error for empty CWD")
	}
}

func TestStart_InvalidCWD_Relative(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	_, err := eng.Start(testCtx(t), agentrun.Session{CWD: "relative/path"})
	if err == nil {
		t.Fatal("expected error for relative CWD")
	}
}

func TestStart_InvalidCWD_NonExistent(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	_, err := eng.Start(testCtx(t), agentrun.Session{CWD: "/nonexistent/path/xyz"})
	if err == nil {
		t.Fatal("expected error for non-existent CWD")
	}
}

func TestStart_ContextCanceled(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binSleep, []string{"60"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Stop immediately to ensure no leaked process.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
}

// ---------------------------------------------------------------------------
// Output tests
// ---------------------------------------------------------------------------

func TestOutput_ClosedAfterExit(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// range terminates when channel closes.
	count := 0
	for range p.Output() {
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}
}

func TestOutput_DrainAfterStop(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binSleep, []string{"60"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)

	// Output channel should be closed after Stop returns.
	drain(p)
}

// ---------------------------------------------------------------------------
// Stop tests
// ---------------------------------------------------------------------------

func TestStop_Graceful(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binSleep, []string{"60"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Stop(ctx); !errors.Is(err, agentrun.ErrTerminated) {
		t.Fatalf("expected ErrTerminated, got %v", err)
	}
}

func TestStop_ForceKill(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			// Trap SIGTERM and ignore it — forces SIGKILL path.
			return binBash, []string{"-c", `trap "" TERM; sleep 60`}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b, cli.WithGracePeriod(200*time.Millisecond))
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give bash time to set up trap.
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
	elapsed := time.Since(start)

	// Should have been killed within grace period + some margin.
	if elapsed > 2*time.Second {
		t.Fatalf("Stop took too long: %v", elapsed)
	}
}

func TestStop_Idempotent(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)

	ctx := testCtx(t)
	_ = p.Stop(ctx)
	_ = p.Stop(ctx) // second call must not panic
}

func TestStop_AfterNaturalExit(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = p.Wait()

	// Stop on an already-exited process.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
}

func TestStop_ContextDeadline(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binBash, []string{"-c", `trap "" TERM; sleep 60`}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b, cli.WithGracePeriod(30*time.Second))
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Short context deadline triggers SIGKILL before grace period.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_ = p.Stop(ctx)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("Stop took too long: %v", elapsed)
	}
}

// ---------------------------------------------------------------------------
// Wait / Err tests
// ---------------------------------------------------------------------------

func TestWait_CleanExit(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)
	if err := p.Wait(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWait_ErrorExit(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binBash, []string{"-c", "exit 42"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)
	if err := p.Wait(); err == nil {
		t.Fatal("expected non-nil error for exit 42")
	}
}

func TestWait_OutputNotDrained(t *testing.T) {
	// Verify no deadlock when output channel is not drained.
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Don't drain — Wait should still return because output buffer > 0.
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait deadlocked without draining output")
	}
}

func TestErr_BeforeClose(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binSleep, []string{"60"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Err should be nil while process is running.
	if err := p.Err(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
}

func TestErr_AfterStop(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binSleep, []string{"60"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)

	if err := p.Err(); !errors.Is(err, agentrun.ErrTerminated) {
		t.Fatalf("expected ErrTerminated, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Send tests
// ---------------------------------------------------------------------------

func TestSend_Stdin(t *testing.T) {
	b := &testStreamerBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) {
				return binCat, nil
			},
			parseFn: textParser,
		},
		streamFn: func(_ agentrun.Session) (string, []string) {
			return binCat, nil
		},
		formatFn: func(msg string) ([]byte, error) {
			return []byte(msg + "\n"), nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := p.Send(testCtx(t), "hello stdin"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Read one message.
	msg := <-p.Output()
	if msg.Content != "hello stdin" {
		t.Fatalf("expected 'hello stdin', got %q", msg.Content)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
}

func TestSend_Resume(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) {
				// Long-lived: outputs "initial" then waits for SIGTERM.
				return binBash, []string{"-c", "echo initial; sleep 60"}
			},
			parseFn: textParser,
		},
		resumeFn: func(_ agentrun.Session, prompt string) (string, []string, error) {
			return binEcho, []string{prompt}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First message from initial spawn.
	msg := <-p.Output()
	if msg.Content != "initial" {
		t.Fatalf("expected 'initial', got %q", msg.Content)
	}

	// Send triggers Resumer replacement (kills old, spawns new).
	if err := p.Send(testCtx(t), "resumed"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Second message from replacement subprocess.
	msg = <-p.Output()
	if msg.Content != "resumed" {
		t.Fatalf("expected 'resumed', got %q", msg.Content)
	}

	drain(p)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestStart_NoSendCapability(t *testing.T) {
	eng := cli.NewEngine(echoBackend())
	_, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err == nil {
		t.Fatal("expected error for no send capability")
	}
	if !errors.Is(err, agentrun.ErrSendNotSupported) {
		t.Fatalf("expected ErrSendNotSupported, got %v", err)
	}
}

func TestSend_AfterStop(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binSleep, []string{"60"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)

	if err := p.Send(testCtx(t), "hello"); !errors.Is(err, agentrun.ErrTerminated) {
		t.Fatalf("expected ErrTerminated, got %v", err)
	}
}

func TestSend_Stdin_ConcurrentWithStop(t *testing.T) {
	b := &testStreamerBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) {
				return binCat, nil
			},
			parseFn: textParser,
		},
		streamFn: func(_ agentrun.Session) (string, []string) {
			return binCat, nil
		},
		formatFn: func(msg string) ([]byte, error) {
			return []byte(msg + "\n"), nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 100 {
			if err := p.Send(testCtx(t), fmt.Sprintf("msg-%d", i)); err != nil {
				return // expected once Stop is called
			}
		}
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()
	wg.Wait()
}

func TestSend_Resume_ContextCanceled(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) {
				// Trap SIGTERM so the old process doesn't exit quickly.
				return binBash, []string{"-c", `trap "" TERM; echo initial; sleep 60`}
			},
			parseFn: textParser,
		},
		resumeFn: func(_ agentrun.Session, _ string) (string, []string, error) {
			return binSleep, []string{"60"}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Read initial message.
	<-p.Output()

	// Give bash time to set up SIGTERM trap.
	time.Sleep(100 * time.Millisecond)

	// Send with already-canceled context — forces ctx.Done path in replaceSubprocess.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = p.Send(ctx, "should-fail")
	if err == nil {
		t.Fatal("expected error from canceled context")
	}

	// Process should be cleanly finishable — Stop must not deadlock.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopCancel()
	_ = p.Stop(stopCtx)
}

func TestStart_StreamerWithoutFormatter(t *testing.T) {
	b := &testStreamerOnlyBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) {
				return binCat, nil
			},
			parseFn: textParser,
		},
		streamFn: func(_ agentrun.Session) (string, []string) {
			return binCat, nil
		},
	}
	eng := cli.NewEngine(b)
	_, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err == nil {
		t.Fatal("expected error when Streamer lacks InputFormatter")
	}
	if !errors.Is(err, agentrun.ErrSendNotSupported) {
		t.Fatalf("expected ErrSendNotSupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "InputFormatter") {
		t.Fatalf("expected InputFormatter mention, got %v", err)
	}
}

func TestStart_ResumerOnlyHasSendCapability(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)
	_ = p.Wait()
}

func TestStart_StreamerWithFormatterHasSendCapability(t *testing.T) {
	b := &testStreamerBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) { return binCat, nil },
			parseFn: textParser,
		},
		streamFn: func(_ agentrun.Session) (string, []string) { return binCat, nil },
		formatFn: func(msg string) ([]byte, error) { return []byte(msg + "\n"), nil },
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
}

func TestStart_StreamerResumerWithoutFormatter_FallsBackToSpawn(t *testing.T) {
	streamCalled := false
	b := &testStreamerResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binEcho, []string{s.Prompt}
			},
			parseFn: textParser,
		},
		streamFn: func(_ agentrun.Session) (string, []string) {
			streamCalled = true
			return binCat, nil
		},
		resumeFn: func(s agentrun.Session, _ string) (string, []string, error) {
			bin, args := s.Prompt, []string{}
			return bin, args, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "hello"})
	if err != nil {
		t.Fatalf("Start should succeed (Resumer available): %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()
	if streamCalled {
		t.Error("StreamArgs should not be called when InputFormatter is missing")
	}
}

// ---------------------------------------------------------------------------
// resumeAfterCleanExit tests (spawn-per-turn pattern)
// ---------------------------------------------------------------------------

func TestResumeAfterCleanExit_HappyPath(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binEcho, []string{s.Prompt}
			},
			parseFn: textParser,
		},
		resumeFn: func(_ agentrun.Session, prompt string) (string, []string, error) {
			return binEcho, []string{prompt}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "turn1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First turn: drain output, subprocess exits cleanly.
	msgs := drain(p)
	if len(msgs) != 1 || msgs[0].Content != "turn1" {
		t.Fatalf("turn1: expected [turn1], got %v", msgs)
	}

	// Second turn: Send triggers resumeAfterCleanExit (done closed, termErr nil).
	if err := p.Send(testCtx(t), "turn2"); err != nil {
		t.Fatalf("Send turn2: %v", err)
	}
	msgs = drain(p)
	if len(msgs) != 1 || msgs[0].Content != "turn2" {
		t.Fatalf("turn2: expected [turn2], got %v", msgs)
	}

	// Third turn: another round to verify stability across multiple resumes.
	if err := p.Send(testCtx(t), "turn3"); err != nil {
		t.Fatalf("Send turn3: %v", err)
	}
	msgs = drain(p)
	if len(msgs) != 1 || msgs[0].Content != "turn3" {
		t.Fatalf("turn3: expected [turn3], got %v", msgs)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
}

func TestResumeAfterCleanExit_OutputChannelSwap(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binEcho, []string{s.Prompt}
			},
			parseFn: textParser,
		},
		resumeFn: func(_ agentrun.Session, prompt string) (string, []string, error) {
			return binEcho, []string{prompt}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ch1 := p.Output()
	drain(p)

	// Resume with a new turn.
	if err := p.Send(testCtx(t), "y"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	ch2 := p.Output()

	// Old channel should be closed.
	if _, ok := <-ch1; ok {
		t.Error("old output channel should be closed after resume")
	}

	// New channel should have the new subprocess output.
	msg := <-ch2
	if msg.Content != "y" {
		t.Fatalf("expected 'y' from new channel, got %q", msg.Content)
	}
	drain(p)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.Stop(ctx)
}

func TestResumeAfterCleanExit_ResumeArgsError(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binEcho, []string{s.Prompt}
			},
			parseFn: textParser,
		},
		resumeFn: func(_ agentrun.Session, _ string) (string, []string, error) {
			return "", nil, errors.New("no session ID")
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)

	err = p.Send(testCtx(t), "y")
	if err == nil {
		t.Fatal("expected error from ResumeArgs")
	}
	if !strings.Contains(err.Error(), "no session ID") {
		t.Fatalf("expected 'no session ID' in error, got %v", err)
	}
}

func TestResumeAfterCleanExit_BinaryNotFound(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binEcho, []string{s.Prompt}
			},
			parseFn: textParser,
		},
		resumeFn: func(_ agentrun.Session, _ string) (string, []string, error) {
			return "nonexistent-binary-xyz-999", nil, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)

	err = p.Send(testCtx(t), "y")
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	if !errors.Is(err, agentrun.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestResumeAfterCleanExit_ContextCanceled(t *testing.T) {
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binEcho, []string{s.Prompt}
			},
			parseFn: textParser,
		},
		resumeFn: func(_ agentrun.Session, prompt string) (string, []string, error) {
			return binEcho, []string{prompt}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)

	// Send with already-canceled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = p.Send(ctx, "y")
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

// ---------------------------------------------------------------------------
// ReadLoop behavior tests
// ---------------------------------------------------------------------------

func TestReadLoop_SkipLine(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return "printf", []string{"keep\nskip\nkeep2\n"}
		},
		parseFn: func(line string) (agentrun.Message, error) {
			if line == "skip" {
				return agentrun.Message{}, cli.ErrSkipLine
			}
			return agentrun.Message{Type: agentrun.MessageText, Content: line}, nil
		},
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (skip filtered), got %d", len(msgs))
	}
	if msgs[0].Content != "keep" || msgs[1].Content != "keep2" {
		t.Fatalf("unexpected messages: %v", msgs)
	}
}

func TestReadLoop_ParseError(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binEcho, []string{"bad"}
		},
		parseFn: func(_ string) (agentrun.Message, error) {
			return agentrun.Message{}, errors.New("parse failed")
		},
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(msgs))
	}
	if msgs[0].Type != agentrun.MessageError {
		t.Fatalf("expected MessageError, got %v", msgs[0].Type)
	}
	if !strings.Contains(msgs[0].Content, "parse failed") {
		t.Fatalf("expected 'parse failed' in content, got %q", msgs[0].Content)
	}
}

func TestReadLoop_Timestamp(t *testing.T) {
	before := time.Now()
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Timestamp.Before(before) {
		t.Fatalf("timestamp %v is before start time %v", msgs[0].Timestamp, before)
	}
}

func TestReadLoop_RawLine(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "hello"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	if len(msgs) != 1 {
		t.Fatalf("expected 1, got %d", len(msgs))
	}
	if msgs[0].RawLine != "hello" {
		t.Fatalf("expected RawLine 'hello', got %q", msgs[0].RawLine)
	}
}

func TestReadLoop_SessionIDPreserved(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binEcho, []string{"init_line"}
		},
		parseFn: func(_ string) (agentrun.Message, error) {
			return agentrun.Message{
				Type:    agentrun.MessageInit,
				Content: "ses_test123",
			}, nil
		},
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Type != agentrun.MessageInit {
		t.Fatalf("expected MessageInit, got %v", msgs[0].Type)
	}
	if msgs[0].Content != "ses_test123" {
		t.Fatalf("Content = %q, want %q (session ID must survive readLoop)", msgs[0].Content, "ses_test123")
	}
}

// ---------------------------------------------------------------------------
// OptionResumeID integration tests
// ---------------------------------------------------------------------------

func TestStart_OptionResumeID_StreamArgs(t *testing.T) {
	var capturedSession agentrun.Session
	b := &testStreamerBackend{
		testBackend: testBackend{
			spawnFn: func(_ agentrun.Session) (string, []string) { return binCat, nil },
			parseFn: textParser,
		},
		streamFn: func(s agentrun.Session) (string, []string) {
			capturedSession = s
			return binCat, nil
		},
		formatFn: func(msg string) ([]byte, error) { return []byte(msg + "\n"), nil },
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{
		CWD:     tempDir(t),
		Options: map[string]string{agentrun.OptionResumeID: "conv-abc123"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()

	if capturedSession.Options[agentrun.OptionResumeID] != "conv-abc123" {
		t.Fatalf("StreamArgs did not receive OptionResumeID: %v", capturedSession.Options)
	}
}

func TestStart_OptionResumeID_SpawnArgs(t *testing.T) {
	var capturedSession agentrun.Session
	b := withResumer(testBackend{
		spawnFn: func(s agentrun.Session) (string, []string) {
			capturedSession = s
			return binEcho, []string{"x"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{
		CWD:     tempDir(t),
		Options: map[string]string{agentrun.OptionResumeID: "ses_abc12345678901234567"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)
	_ = p.Wait()

	if capturedSession.Options[agentrun.OptionResumeID] != "ses_abc12345678901234567" {
		t.Fatalf("SpawnArgs did not receive OptionResumeID: %v", capturedSession.Options)
	}
}

func TestResumeAfterCleanExit_OptionResumeID(t *testing.T) {
	var resumeSession agentrun.Session
	b := &testResumerBackend{
		testBackend: testBackend{
			spawnFn: func(s agentrun.Session) (string, []string) {
				return binEcho, []string{s.Prompt}
			},
			parseFn: textParser,
		},
		resumeFn: func(s agentrun.Session, prompt string) (string, []string, error) {
			resumeSession = s
			return binEcho, []string{prompt}, nil
		},
	}
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{
		CWD:     tempDir(t),
		Prompt:  "turn1",
		Options: map[string]string{agentrun.OptionResumeID: "ses_resume1234567890123456"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Drain first turn — subprocess exits cleanly.
	drain(p)

	// Send triggers resumeAfterCleanExit.
	if err := p.Send(testCtx(t), "turn2"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	drain(p)

	if resumeSession.Options[agentrun.OptionResumeID] != "ses_resume1234567890123456" {
		t.Fatalf("ResumeArgs did not receive OptionResumeID: %v", resumeSession.Options)
	}
}

func TestReadLoop_PanicRecovery(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binEcho, []string{"trigger"}
		},
		parseFn: func(_ string) (agentrun.Message, error) {
			panic("parser exploded")
		},
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Process should terminate with error, not crash the host.
	err = p.Wait()
	if err == nil {
		t.Fatal("expected error from panic")
	}
	if !strings.Contains(err.Error(), "parser panic") {
		t.Fatalf("expected 'parser panic' in error, got %v", err)
	}
}

func TestReadLoop_ScannerOverflow(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			// Generate a line longer than 256 bytes (no trailing newline).
			return binBash, []string{"-c", fmt.Sprintf("head -c %d /dev/zero | tr '\\0' 'A'", 512)}
		},
		parseFn: textParser,
	})
	// Set tiny scanner buffer to trigger overflow.
	eng := cli.NewEngine(b, cli.WithScannerBuffer(256))
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	msgs := drain(p)
	hasError := false
	for _, m := range msgs {
		if m.Type == agentrun.MessageError && strings.Contains(m.Content, "scanner") {
			hasError = true
			break
		}
	}
	if !hasError {
		t.Fatalf("expected scanner error message, got %v", msgs)
	}
}

// ---------------------------------------------------------------------------
// Session deep-copy test
// ---------------------------------------------------------------------------

func TestStart_SessionDeepCopy(t *testing.T) {
	var capturedOpts map[string]string
	b := withResumer(testBackend{
		spawnFn: func(s agentrun.Session) (string, []string) {
			capturedOpts = s.Options
			return binEcho, []string{"x"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)

	origOpts := map[string]string{"key": "original"}
	p, err := eng.Start(testCtx(t), agentrun.Session{
		CWD:     tempDir(t),
		Options: origOpts,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)
	_ = p.Wait()

	// Mutating original should not affect captured.
	origOpts["key"] = "mutated"
	if capturedOpts["key"] != "original" {
		t.Fatalf("session was not deep-copied: captured=%q", capturedOpts["key"])
	}
}

// ---------------------------------------------------------------------------
// Concurrency tests
// ---------------------------------------------------------------------------

func TestConcurrent_StopAndRead(t *testing.T) {
	b := withResumer(testBackend{
		spawnFn: func(_ agentrun.Session) (string, []string) {
			return binBash, []string{"-c", "while true; do echo line; sleep 0.01; done"}
		},
		parseFn: textParser,
	})
	eng := cli.NewEngine(b)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t)})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Read and stop concurrently.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		drain(p)
	}()
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Stop(ctx)
	}()
	wg.Wait()
}

func TestConcurrent_EngineStart(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	dir := tempDir(t)

	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := eng.Start(testCtx(t), agentrun.Session{
				CWD:    dir,
				Prompt: fmt.Sprintf("msg-%d", i),
			})
			if err != nil {
				t.Errorf("Start: %v", err)
				return
			}
			drain(p)
			_ = p.Wait()
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Options tests
// ---------------------------------------------------------------------------

func TestOptions_Defaults(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend())
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestOptions_Custom(t *testing.T) {
	eng := cli.NewEngine(echoResumerBackend(),
		cli.WithOutputBuffer(10),
		cli.WithScannerBuffer(4096),
		cli.WithGracePeriod(1*time.Second),
	)
	p, err := eng.Start(testCtx(t), agentrun.Session{CWD: tempDir(t), Prompt: "x"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	drain(p)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CWD as file (not directory) test
// ---------------------------------------------------------------------------

func TestStart_CWD_IsFile(t *testing.T) {
	dir := tempDir(t)
	filePath := filepath.Join(dir, "afile.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := cli.NewEngine(echoResumerBackend())
	_, err := eng.Start(testCtx(t), agentrun.Session{CWD: filePath})
	if err == nil {
		t.Fatal("expected error when CWD is a file")
	}
}
