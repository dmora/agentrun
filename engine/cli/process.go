//go:build !windows

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dmora/agentrun"
)

// capabilities holds resolved optional interfaces for a process.
// Resolved once in Engine.Start to eliminate process→engine back-references.
type capabilities struct {
	resumer   Resumer
	streamer  Streamer
	formatter InputFormatter
}

func resolveCapabilities(backend Backend) capabilities {
	var caps capabilities
	if r, ok := backend.(Resumer); ok {
		caps.resumer = r
	}
	if s, ok := backend.(Streamer); ok {
		caps.streamer = s
	}
	if f, ok := backend.(InputFormatter); ok {
		caps.formatter = f
	}
	return caps
}

// signalProcess sends sig to a process, returning nil if the process
// has already exited (os.ErrProcessDone).
func signalProcess(proc *os.Process, sig os.Signal) error {
	err := proc.Signal(sig)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

// process implements agentrun.Process for CLI subprocess sessions.
type process struct {
	backend Backend
	caps    capabilities
	session agentrun.Session
	opts    EngineOptions
	env     []string // resolved env for subprocess; nil = inherit parent

	output chan agentrun.Message

	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	replacing  bool
	cancelRead context.CancelFunc

	cmdDone chan struct{} // buffered(1), signaled by every readLoop defer
	done    chan struct{} // closed exactly once by finish()
	termErr error         // set by finish(), read after done closes

	stopping   atomic.Bool
	stopOnce   sync.Once
	finishOnce sync.Once
}

var _ agentrun.Process = (*process)(nil)

// newProcess creates and starts a process with its initial readLoop.
func newProcess(
	backend Backend,
	caps capabilities,
	session agentrun.Session,
	opts EngineOptions,
	env []string,
	cmd *exec.Cmd,
	stdin io.WriteCloser,
	stdout io.ReadCloser,
) *process {
	readCtx, cancelRead := context.WithCancel(context.Background())

	p := &process{
		backend:    backend,
		caps:       caps,
		session:    session,
		opts:       opts,
		env:        env,
		output:     make(chan agentrun.Message, opts.OutputBuffer),
		cmd:        cmd,
		stdin:      stdin,
		cancelRead: cancelRead,
		cmdDone:    make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	go p.readLoop(readCtx, stdout)
	return p
}

// Output returns the channel for receiving messages from the subprocess.
// The underlying channel may change between turns for spawn-per-turn backends
// (Resumer without Streamer). Callers should call Output() at the start of
// each turn rather than caching the channel reference across turns.
func (p *process) Output() <-chan agentrun.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.output
}

// Send transmits a user message to the subprocess.
func (p *process) Send(ctx context.Context, message string) error {
	if p.stopping.Load() {
		return agentrun.ErrTerminated
	}

	// Check if the session has ended.
	select {
	case <-p.done:
		// For Resumer backends, a clean subprocess exit (termErr == nil) is
		// the normal end of a turn, not the end of the session. Restart by
		// spawning a new subprocess with ResumeArgs.
		//
		// termErr must be read under mu because resumeAfterCleanExit resets
		// it under the same lock — concurrent Send() calls would race otherwise.
		p.mu.Lock()
		cleanExit := p.caps.resumer != nil && p.termErr == nil
		p.mu.Unlock()
		if cleanExit {
			return p.resumeAfterCleanExit(ctx, message)
		}
		return agentrun.ErrTerminated
	default:
	}

	// Path 1: stdin pipe (Streamer mode).
	if p.stdin != nil {
		return p.sendStdin(message)
	}

	// Path 2: Resumer (subprocess replacement while running).
	if p.caps.resumer != nil {
		return p.replaceSubprocess(ctx, message)
	}

	// Defensive guard — Start() validates send capability; unreachable.
	return fmt.Errorf("%w: no send path available", agentrun.ErrSendNotSupported)
}

// sendStdin formats and writes a message to the subprocess stdin pipe.
func (p *process) sendStdin(message string) error {
	if p.caps.formatter == nil {
		// Defensive guard — Start() validates send capability; unreachable.
		return fmt.Errorf("%w: InputFormatter missing", agentrun.ErrSendNotSupported)
	}
	data, err := p.caps.formatter.FormatInput(message)
	if err != nil {
		return fmt.Errorf("cli: format input: %w", err)
	}
	p.mu.Lock()
	stdin := p.stdin
	p.mu.Unlock()
	if stdin == nil {
		return agentrun.ErrTerminated
	}
	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("cli: write stdin: %w", err)
	}
	return nil
}

// Stop terminates the subprocess. Safe to call multiple times.
// Blocks until the output channel is closed.
func (p *process) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		p.stopping.Store(true)

		p.mu.Lock()
		if p.stdin != nil {
			_ = p.stdin.Close() // Best-effort: pipe may already be closed.
		}
		cancelRead := p.cancelRead
		cmd := p.cmd
		p.mu.Unlock()

		// Unblock readLoop if stuck on channel send.
		cancelRead()

		// Send SIGTERM for graceful termination.
		_ = signalProcess(cmd.Process, syscall.SIGTERM)

		// Wait for readLoop to finish, with grace period.
		select {
		case <-p.cmdDone:
		case <-time.After(p.opts.GracePeriod):
			_ = signalProcess(cmd.Process, os.Kill)
			<-p.cmdDone
		case <-ctx.Done():
			_ = signalProcess(cmd.Process, os.Kill)
			<-p.cmdDone
		}
	})

	// Block until finish() completes (output channel closed).
	<-p.done
	return p.termErr
}

// Wait blocks until the session ends naturally.
func (p *process) Wait() error {
	<-p.done
	return p.termErr
}

// Err returns the terminal error, or nil if still running.
func (p *process) Err() error {
	select {
	case <-p.done:
		return p.termErr
	default:
		return nil
	}
}

// finish sets the terminal error and closes output+done channels.
// Called exactly once via sync.Once.
func (p *process) finish(err error) {
	p.finishOnce.Do(func() {
		p.termErr = err
		close(p.output)
		close(p.done)
	})
}

// readLoop is the goroutine that reads subprocess stdout and pumps messages.
func (p *process) readLoop(ctx context.Context, stdout io.ReadCloser) {
	var panicErr error
	var scanErr error

	defer func() {
		if r := recover(); r != nil {
			_ = signalProcess(p.cmd.Process, os.Kill)
			panicErr = fmt.Errorf("cli: parser panic: %v", r)
		}

		p.mu.Lock()
		cmd := p.cmd
		p.mu.Unlock()

		waitErr := cmd.Wait()
		switch {
		case panicErr != nil:
			waitErr = panicErr
		case scanErr != nil:
			waitErr = fmt.Errorf("cli: scanner: %w", scanErr)
		default:
			waitErr = wrapExitError(waitErr)
		}
		if p.stopping.Load() {
			waitErr = agentrun.ErrTerminated
		}

		p.mu.Lock()
		replacing := p.replacing
		p.mu.Unlock()

		if !replacing {
			p.finish(waitErr)
		}

		// Always signal cmdDone so Stop/replaceSubprocess can proceed.
		p.cmdDone <- struct{}{}
	}()

	scanErr = p.scanLines(ctx, stdout)
	if scanErr != nil {
		// Surface scanner error as a message before termination.
		msg := agentrun.Message{
			Type:      agentrun.MessageError,
			Content:   fmt.Sprintf("cli: scanner: %v", scanErr),
			Timestamp: time.Now(),
		}
		select {
		case p.output <- msg:
		default:
			// Channel full; error preserved in scanErr, surfaced via finish().
		}
		p.mu.Lock()
		_ = signalProcess(p.cmd.Process, os.Kill)
		p.mu.Unlock()
	}
}

// scanLines reads lines from stdout and sends parsed messages to the output channel.
func (p *process) scanLines(ctx context.Context, stdout io.ReadCloser) error {
	scanner := bufio.NewScanner(stdout)
	initCap := min(4096, p.opts.ScannerBuffer)
	scanner.Buffer(make([]byte, 0, initCap), p.opts.ScannerBuffer)

	var lastStopReason agentrun.StopReason

	for scanner.Scan() {
		line := scanner.Text()
		msg, err := p.backend.ParseLine(line)
		if errors.Is(err, ErrSkipLine) {
			continue
		}
		if err != nil {
			msg = agentrun.Message{
				Type:    agentrun.MessageError,
				Content: fmt.Sprintf("cli: parse: %v", err),
			}
		}
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now()
		}

		lastStopReason = applyStopReasonCarryForward(&msg, lastStopReason)
		if msg.Type == agentrun.MessageInit {
			msg.Process = p.processMetaSnapshot()
		}

		select {
		case p.output <- msg:
		case <-ctx.Done():
			return nil
		}
	}
	return scanner.Err()
}

// wrapExitError converts a non-zero *exec.ExitError to *agentrun.ExitError.
// nil → nil, non-ExitError → passthrough, code 0 → nil (clean exit).
// Preserves the error chain via ExitError.Unwrap.
//
// NOTE: intentionally duplicated in engine/acp/process.go — keep in sync.
func wrapExitError(err error) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return err
	}
	code := ee.ExitCode()
	if code == 0 {
		return nil
	}
	return &agentrun.ExitError{Code: code, Err: err}
}

// processMetaSnapshot returns subprocess metadata for MessageInit enrichment.
// Returns nil if cmd or its process is unavailable.
//
// Locks p.mu because CLI's cmd is reassigned on resumeAfterCleanExit
// for spawn-per-turn backends — unlike ACP where cmd is write-once.
func (p *process) processMetaSnapshot() *agentrun.ProcessMeta {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 {
		return nil
	}
	return &agentrun.ProcessMeta{
		PID:    cmd.Process.Pid,
		Binary: cmd.Path,
	}
}

// applyStopReasonCarryForward implements StopReason carry-forward between
// parsed messages. Claude CLI's result.stop_reason is always null in streaming
// mode; the real stop_reason arrives earlier in message_delta stream events.
// This function captures it and applies it to the next MessageResult.
//
// Returns the updated lastStopReason for the next call.
func applyStopReasonCarryForward(msg *agentrun.Message, last agentrun.StopReason) agentrun.StopReason {
	// Clear stale carry-forward on new turn (streaming mode: scanLines
	// spans the entire subprocess lifetime).
	if msg.Type == agentrun.MessageInit {
		return ""
	}

	// Capture StopReason from non-result messages (e.g., message_delta).
	if msg.StopReason != "" && msg.Type != agentrun.MessageResult {
		captured := msg.StopReason
		msg.StopReason = "" // don't leak to consumer on the system message
		return captured
	}

	// Apply carried StopReason to result messages only when the result
	// itself has no StopReason (avoid clobbering authoritative values).
	if msg.Type == agentrun.MessageResult {
		if msg.StopReason == "" && last != "" {
			msg.StopReason = last
		}
		return "" // always clear on result
	}

	return last
}

// replaceSubprocess performs the Resumer subprocess-replacement pattern.
func (p *process) replaceSubprocess(ctx context.Context, message string) error {
	binary, args, err := p.caps.resumer.ResumeArgs(p.session, message)
	if err != nil {
		return fmt.Errorf("cli: resume args: %w", err)
	}
	resolvedBinary, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", agentrun.ErrUnavailable, binary, err)
	}

	// Signal old process to terminate.
	p.mu.Lock()
	p.replacing = true
	oldCancel := p.cancelRead
	if p.stdin != nil {
		_ = p.stdin.Close() // Best-effort: pipe may already be closed.
	}
	oldCmd := p.cmd
	p.mu.Unlock()

	oldCancel()
	_ = signalProcess(oldCmd.Process, syscall.SIGTERM)

	// Wait for old readLoop to finish.
	select {
	case <-p.cmdDone:
	case <-ctx.Done():
		_ = signalProcess(oldCmd.Process, os.Kill)
		<-p.cmdDone
		p.failReplacement(ctx.Err())
		return ctx.Err()
	}

	return p.spawnReplacement(resolvedBinary, args)
}

// failReplacement handles cleanup when subprocess replacement fails.
// It resets the replacing flag, finishes the process with the given error,
// and signals cmdDone so a subsequent Stop() call won't deadlock.
func (p *process) failReplacement(err error) {
	p.mu.Lock()
	p.replacing = false
	p.mu.Unlock()
	p.finish(err)
	select {
	case p.cmdDone <- struct{}{}:
	default:
	}
}

// resumeAfterCleanExit restarts a session after the subprocess exited cleanly
// between turns. This is the normal flow for spawn-per-turn backends (Resumer
// without Streamer) like OpenCode, where each turn is a separate subprocess.
//
// It creates fresh output/done channels and resets finishOnce so the new
// readLoop can call finish() when the next subprocess exits.
func (p *process) resumeAfterCleanExit(ctx context.Context, message string) error {
	if p.stopping.Load() {
		return agentrun.ErrTerminated
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	binary, args, err := p.caps.resumer.ResumeArgs(p.session, message)
	if err != nil {
		return fmt.Errorf("cli: resume args: %w", err)
	}
	resolvedBinary, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", agentrun.ErrUnavailable, binary, err)
	}

	cmd, stdin, stdout, err := spawnCmd(resolvedBinary, args, p.session.CWD, p.caps.streamer != nil, p.env)
	if err != nil {
		return fmt.Errorf("cli: resume: %w", err)
	}

	// Drain stale cmdDone signal from the previous subprocess.
	select {
	case <-p.cmdDone:
	default:
	}

	// Check for concurrent Stop() before committing to the new subprocess.
	// If Stop() has already been called, abort and clean up the spawned process.
	p.mu.Lock()
	if p.stopping.Load() {
		p.mu.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return agentrun.ErrTerminated
	}
	// Reset channel infrastructure for the new turn.
	p.output = make(chan agentrun.Message, p.opts.OutputBuffer)
	p.done = make(chan struct{})
	p.finishOnce = sync.Once{}
	p.termErr = nil
	p.mu.Unlock()

	p.installSubprocess(cmd, stdin, stdout)
	return nil
}

// spawnReplacement starts a new subprocess and readLoop after a Resumer swap.
func (p *process) spawnReplacement(binary string, args []string) error {
	cmd, stdin, stdout, err := spawnCmd(binary, args, p.session.CWD, p.caps.streamer != nil, p.env)
	if err != nil {
		p.failReplacement(fmt.Errorf("cli: resume: %w", err))
		return err
	}

	p.installSubprocess(cmd, stdin, stdout)
	return nil
}

// installSubprocess wires a spawned command into the process and starts its
// readLoop. Must be called while no readLoop is active for this process.
func (p *process) installSubprocess(cmd *exec.Cmd, stdin io.WriteCloser, stdout io.ReadCloser) {
	readCtx, cancelRead := context.WithCancel(context.Background())

	p.mu.Lock()
	p.cmd = cmd
	p.stdin = stdin
	p.cancelRead = cancelRead
	p.replacing = false
	p.mu.Unlock()

	go p.readLoop(readCtx, stdout)
}
