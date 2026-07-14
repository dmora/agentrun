//go:build !windows

package cli

import (
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
	"github.com/dmora/agentrun/engine/internal/lineread"
)

// capabilities holds resolved optional interfaces for a process.
// Resolved once in Engine.Start to eliminate process→engine back-references.
type capabilities struct {
	resumer   Resumer
	streamer  Streamer
	formatter InputFormatter
	shellFeed bool // ShellFeedBackend presence; gates WithShellTracking, see scanLines
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
	if _, ok := backend.(ShellFeedBackend); ok {
		caps.shellFeed = true
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

	awaitingResult atomic.Bool // true when the current turn still owes MessageResult
	stopping       atomic.Bool
	stopOnce       sync.Once
	finishOnce     sync.Once
}

var _ agentrun.Process = (*process)(nil)
var _ agentrun.BlockSender = (*process)(nil)

// sequentialProcess wraps a CLI process to satisfy agentrun.SequentialSender.
// Used for spawn-per-turn backends (Resumer without Streamer).
type sequentialProcess struct{ *process }

func (s *sequentialProcess) SequentialSend() {}

var _ agentrun.SequentialSender = (*sequentialProcess)(nil)
var _ agentrun.BlockSender = (*sequentialProcess)(nil)

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
	p.awaitingResult.Store(true)
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
	return p.SendBlocks(ctx, agentrun.TextBlock(message))
}

// SendBlocks transmits structured content blocks to the subprocess.
func (p *process) SendBlocks(ctx context.Context, blocks ...agentrun.ContentBlock) error {
	if p.stopping.Load() {
		return agentrun.ErrTerminated
	}

	// Validate blocks.
	if err := agentrun.ValidateBlocks(blocks); err != nil {
		return fmt.Errorf("cli: validate blocks: %w", err)
	}

	// Check if the session has ended.
	select {
	case <-p.done:
		// For Resumer backends, a clean subprocess exit (termErr == nil) is
		// the normal end of a turn, not the end of the session. Restart by
		// spawning a new subprocess with ResumeArgs.
		p.mu.Lock()
		cleanExit := p.caps.resumer != nil && p.termErr == nil
		p.mu.Unlock()
		if cleanExit {
			text := agentrun.TextFromBlocks(blocks)
			return p.resumeAfterCleanExit(ctx, text)
		}
		return agentrun.ErrTerminated
	default:
	}

	// Path 1: stdin pipe (Streamer mode).
	if p.stdin != nil {
		if bf, ok := p.backend.(BlockFormatter); ok {
			data, err := bf.FormatInputBlocks(blocks)
			if err != nil {
				return fmt.Errorf("cli: format input blocks: %w", err)
			}
			return p.sendStdinData(data)
		}
		// Fallback: standard InputFormatter with text degradation.
		text := agentrun.TextFromBlocks(blocks)
		return p.sendStdin(text)
	}

	// Path 2: Resumer (subprocess replacement while running).
	if p.caps.resumer != nil {
		text := agentrun.TextFromBlocks(blocks)
		return p.replaceSubprocess(ctx, text)
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
	return p.sendStdinData(data)
}

// sendStdinData writes pre-formatted data to the subprocess stdin pipe.
func (p *process) sendStdinData(data []byte) error {
	p.mu.Lock()
	stdin := p.stdin
	p.mu.Unlock()
	if stdin == nil {
		return agentrun.ErrTerminated
	}
	// Set before write so a fast subprocess that processes input and emits
	// MessageResult before sendStdin returns cannot have its Store(false)
	// overwritten by a late Store(true).
	p.awaitingResult.Store(true)
	if _, err := stdin.Write(data); err != nil {
		p.awaitingResult.Store(false)
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

// finalizeTurn finishes the process unless a concurrent Send has claimed
// subprocess replacement. The check and the close happen under p.mu so they
// are atomic with respect to a Send deciding which resume path to take.
func (p *process) finalizeTurn(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.replacing {
		return
	}
	p.finishLocked(err)
}

// finishLocked sets the terminal error and closes output+done channels.
// Called exactly once via sync.Once. Caller MUST hold p.mu.
//
// Closing under p.mu is what makes the turn boundary race-free: a concurrent
// Send decides between reusing the channels (replaceSubprocess) and allocating
// fresh ones (resumeAfterCleanExit) under the same lock, so it can never reuse
// a channel this function just closed.
//
// Close order matters: done must close before output so that Err()
// returns the terminal error immediately after a consumer's range
// over Output() exits. Closing output first creates a race —
// the consumer goroutine can call Err() before done is closed.
func (p *process) finishLocked(err error) {
	p.finishOnce.Do(func() {
		p.termErr = err
		close(p.done)
		close(p.output)
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
			waitErr = fmt.Errorf("cli: reader: %w", scanErr)
		default:
			waitErr = wrapExitError(waitErr)
			if waitErr == nil && p.awaitingResult.Load() {
				waitErr = agentrun.ErrNoResult
			}
		}
		if p.stopping.Load() {
			waitErr = agentrun.ErrTerminated
		}

		p.finalizeTurn(waitErr)

		// Always signal cmdDone so Stop/replaceSubprocess can proceed.
		p.cmdDone <- struct{}{}
	}()

	scanErr = p.scanLines(ctx, stdout)
	if scanErr != nil {
		// Surface reader error as a message before termination.
		msg := agentrun.Message{
			Type:      agentrun.MessageError,
			Content:   fmt.Sprintf("cli: reader: %v", scanErr),
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

// defaultReadBuffer is the internal bufio.Reader chunk size for stdout reading.
const defaultReadBuffer = 64 * 1024

// scanLines reads lines from stdout and sends parsed messages to the output channel.
func (p *process) scanLines(ctx context.Context, stdout io.ReadCloser) error {
	lr := lineread.NewReader(stdout, defaultReadBuffer, p.opts.MaxLineSize)

	var lastStopReason agentrun.StopReason
	var maxCallFill int
	// trackShell is WithShellTracking gated by the backend's declared
	// ShellFeedBackend capability (resolved once in Engine.Start): a backend
	// with no native feed structurally cannot report non-subagent kinds, so
	// the option must not activate tracking for it — see ShellFeedBackend.
	trackShell := p.opts.ShellTracking && p.caps.shellFeed
	// Loop-local, scoped to this subprocess. Nil (inert) unless the engine was
	// configured with WithSubagentTools and/or WithShellTracking (backend
	// capability permitting); a subprocess replacement starts a fresh
	// tracker, which is correct — replacement kills any in-process
	// background work we were waiting on.
	tracker := newBackgroundTracker(p.opts.SubagentTools, trackShell)

	for {
		line, err := lr.ReadLineString()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		msg, skip, backendError := p.parseLine(line)
		if skip {
			continue
		}

		// Only reset maxCallFill on backend-emitted errors (turn abort
		// boundary), not synthetic parse errors (recoverable).
		if backendError {
			maxCallFill = 0
		}
		lastStopReason, maxCallFill = p.enrichMessage(&msg, lastStopReason, maxCallFill, tracker)

		if !p.emitWithSynthesis(ctx, msg) {
			return nil
		}
	}
}

// emitWithSynthesis sends msg to the output channel and, when the message
// carries context fill data, follows it with a synthesized MessageContextWindow.
// Returns false if the context was cancelled (caller should return).
func (p *process) emitWithSynthesis(ctx context.Context, msg agentrun.Message) bool {
	synth := synthesizeContextWindow(msg)

	select {
	case p.output <- msg:
	case <-ctx.Done():
		return false
	}

	if synth != nil {
		select {
		case p.output <- *synth:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// synthesizeContextWindow creates a MessageContextWindow to emit after a
// mid-turn content message with context fill data. Returns nil when no
// synthesis is needed (result, init, error, or no fill data).
func synthesizeContextWindow(msg agentrun.Message) *agentrun.Message {
	switch msg.Type {
	case agentrun.MessageResult, agentrun.MessageInit, agentrun.MessageError:
		return nil
	}
	used, size, ok := agentrun.ContextFill(msg)
	if !ok {
		return nil
	}
	return &agentrun.Message{
		Type: agentrun.MessageContextWindow,
		Usage: &agentrun.Usage{
			ContextUsedTokens: used,
			ContextSizeTokens: size,
		},
		Timestamp: msg.Timestamp,
	}
}

// enrichMessage applies engine-level enrichment to a parsed message:
// error-boundary fill reset, stop-reason carry-forward, process metadata,
// context fill tracking, result acknowledgement, and background-task
// tracking. Returns updated (lastStopReason, maxCallFill) for the next
// iteration.
func (p *process) enrichMessage(msg *agentrun.Message, lastStopReason agentrun.StopReason, maxCallFill int, tracker *backgroundTracker) (agentrun.StopReason, int) {
	lastStopReason = applyStopReasonCarryForward(msg, lastStopReason)
	if msg.Type == agentrun.MessageInit {
		msg.Process = p.processMetaSnapshot()
	}
	maxCallFill = applyContextFill(msg, maxCallFill)
	if msg.Type == agentrun.MessageResult {
		p.awaitingResult.Store(false)
	}
	// Fold into the background-task pending set, then stamp the outcome on
	// the two result-bearing types. observe-before-stamp so a completion's
	// stamp reflects the post-removal count. Both are no-ops on a nil
	// (inert) tracker, leaving Message.Background nil.
	tracker.observe(msg)
	if msg.Type == agentrun.MessageResult || msg.Type == agentrun.MessageTaskResult {
		tracker.stamp(msg)
	}
	return lastStopReason, maxCallFill
}

// parseLine delegates to the backend parser and wraps parse errors as
// MessageError messages. Returns (msg, skip, backendError):
//   - skip=true: line should be ignored (ErrSkipLine)
//   - backendError=true: backend emitted MessageError (turn abort boundary,
//     caller must reset maxCallFill). False for synthetic parse errors.
func (p *process) parseLine(line string) (agentrun.Message, bool, bool) {
	msg, err := p.backend.ParseLine(line)
	if errors.Is(err, ErrSkipLine) {
		return agentrun.Message{}, true, false
	}
	backendError := err == nil && msg.Type == agentrun.MessageError
	if err != nil {
		msg = agentrun.Message{
			Type:    agentrun.MessageError,
			Content: fmt.Sprintf("cli: parse: %v", err),
		}
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	return msg, false, backendError
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

// callContextFill returns the context fill for a single API call: the sum of
// input-side token fields (InputTokens + CacheReadTokens + CacheWriteTokens).
// These represent the full input/context size sent to the model for one call.
// Returns 0 for nil Usage. Negative individual fields are clamped to zero.
func callContextFill(u *agentrun.Usage) int {
	if u == nil {
		return 0
	}
	return max(0, u.InputTokens) + max(0, u.CacheReadTokens) + max(0, u.CacheWriteTokens)
}

// applyContextFill tracks per-call context fill and enriches mid-turn messages
// and MessageResult.
//
// For non-result messages with Usage, it sets ContextUsedTokens to the per-call
// context fill (InputTokens + CacheReadTokens + CacheWriteTokens) — reflecting
// that individual API call's fill, not the running peak — and updates maxCallFill
// with the peak input-side fill across all API calls in the turn. Mid-turn
// ContextUsedTokens is only set when the backend hasn't already provided an
// authoritative value (same == 0 guard as MessageResult). For MessageResult, it
// applies the tracked max (when the backend hasn't already set ContextUsedTokens)
// and resets the accumulator for the next turn.
//
// When no per-call usage is available (e.g., Codex, OpenCode — which only
// report usage on the result event), ContextUsedTokens remains 0 ("not
// reported" per omitempty semantics). This is intentional: the CLI engine
// lacks trustworthy per-call data for these backends, and fabricating a
// value from result-level totals would produce incorrect estimates.
//
// Also resets on MessageInit (new session). Backend-emitted MessageError
// (turn abort) is handled by scanLines before calling this function, because
// scanLines must distinguish backend errors from synthetic parse errors that
// should NOT reset the accumulator.
//
// Returns the (possibly updated or reset) maxCallFill for the next iteration.
func applyContextFill(msg *agentrun.Message, maxCallFill int) int {
	if msg.Type == agentrun.MessageInit {
		return 0
	}
	if msg.Usage != nil && msg.Type != agentrun.MessageResult {
		fill := callContextFill(msg.Usage)
		if msg.Usage.ContextUsedTokens == 0 {
			msg.Usage.ContextUsedTokens = fill
		}
		// maxCallFill always uses the computed fill, not the backend-provided
		// ContextUsedTokens, so the peak calculation stays consistent even when
		// a backend overrides individual mid-turn messages.
		maxCallFill = max(maxCallFill, fill)
	}
	if msg.Type == agentrun.MessageResult {
		if maxCallFill > 0 && msg.Usage != nil && msg.Usage.ContextUsedTokens == 0 {
			msg.Usage.ContextUsedTokens = maxCallFill
		}
		return 0 // reset — turn boundary; prevents stale leak in streamer mode
	}
	return maxCallFill
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

// replaceSubprocess performs the Resumer subprocess-replacement pattern: the
// running subprocess is interrupted and a fresh one is spawned to resume.
func (p *process) replaceSubprocess(ctx context.Context, message string) error {
	binary, args, err := p.caps.resumer.ResumeArgs(p.session, message)
	if err != nil {
		return fmt.Errorf("cli: resume args: %w", err)
	}
	resolvedBinary, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", agentrun.ErrUnavailable, binary, err)
	}

	// Claim replacement under p.mu, re-checking the terminal state. The
	// readLoop's exit defer (finalizeTurn) makes its finish decision under the
	// same lock, so exactly one of two things happens here:
	//   - done still open: we set replacing, so the readLoop skips finish and
	//     the channels are preserved for the replacement subprocess.
	//   - done already closed: the subprocess finalized between our caller's
	//     check and now, closing the channels — so we must allocate fresh ones
	//     via spawnCleanResume rather than reuse the closed channels. This is
	//     the fix for the turn-boundary race.
	p.mu.Lock()
	select {
	case <-p.done:
		// Mirror the outer SendBlocks guard: only resume on a clean exit. A turn
		// that finalized with an error must surface as terminated, not be
		// silently resumed (which would also discard the terminal error).
		termErr := p.termErr
		p.mu.Unlock()
		if termErr != nil {
			return agentrun.ErrTerminated
		}
		return p.spawnCleanResume(resolvedBinary, args)
	default:
	}
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
	p.finishLocked(err)
	p.mu.Unlock()
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
	return p.spawnCleanResume(resolvedBinary, args)
}

// spawnCleanResume spawns a resume subprocess after the previous one has exited,
// allocating fresh output/done channels (and resetting finishOnce) so the new
// readLoop can finalize the next turn. Used by the normal clean-exit path and by
// replaceSubprocess when the previous subprocess finalized concurrently.
func (p *process) spawnCleanResume(resolvedBinary string, args []string) error {
	cmd, stdin, stdout, err := spawnCmd(resolvedBinary, args, p.session.CWD, p.caps.streamer != nil, p.env, p.opts.StderrWriter)
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
	cmd, stdin, stdout, err := spawnCmd(binary, args, p.session.CWD, p.caps.streamer != nil, p.env, p.opts.StderrWriter)
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

	p.awaitingResult.Store(true)
	go p.readLoop(readCtx, stdout)
}
