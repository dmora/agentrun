//go:build !windows

package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/internal/errfmt"
	"github.com/dmora/agentrun/engine/internal/stoputil"
)

// process implements agentrun.Process for ACP subprocess sessions.
type process struct {
	conn *Conn
	// cmd is immutable after newProcess() returns — assigned once, never
	// reassigned. processMetaSnapshot reads it without a lock.
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	sessionID string
	opts      EngineOptions

	output       chan agentrun.Message
	outputMu     sync.Mutex // guards output channel close
	outputClosed bool
	done         chan struct{}

	turnMu sync.Mutex // serializes Send() calls

	termErr    error
	stopping   atomic.Bool
	stopOnce   sync.Once
	finishOnce sync.Once

	ctx    context.Context
	cancel context.CancelFunc
}

var _ agentrun.Process = (*process)(nil)

// newProcess creates a process shell. The Conn and ReadLoop are wired up
// by Engine.Start after construction.
func newProcess(cmd *exec.Cmd, stdin io.WriteCloser, opts EngineOptions) *process {
	ctx, cancel := context.WithCancel(context.Background())
	return &process{
		cmd:    cmd,
		stdin:  stdin,
		opts:   opts,
		output: make(chan agentrun.Message, opts.OutputBuffer),
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Output returns the channel for receiving messages from the agent.
func (p *process) Output() <-chan agentrun.Message {
	return p.output
}

// Send transmits a user message to the active session.
// Blocks until the turn completes (RPC response received) or ctx expires.
// The caller must drain Output() concurrently — see updateQueueSize.
func (p *process) Send(ctx context.Context, message string) error {
	if p.stopping.Load() {
		return agentrun.ErrTerminated
	}
	select {
	case <-p.done:
		return agentrun.ErrTerminated
	default:
	}

	p.turnMu.Lock()
	defer p.turnMu.Unlock()

	// Check again after acquiring the lock.
	if p.stopping.Load() {
		return agentrun.ErrTerminated
	}

	// Send session/prompt request.
	params := promptParams{
		SessionID: p.sessionID,
		Prompt:    []contentBlock{{Type: "text", Text: message}},
	}

	var result promptResult
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.conn.Call(ctx, MethodSessionPrompt, params, &result)
	}()

	// Wait for the RPC response. Prefer errCh over done/ctx when both
	// are ready simultaneously, to avoid discarding a successful result.
	select {
	case err := <-errCh:
		return p.handlePromptResult(err, &result)
	case <-p.done:
		// Drain errCh first — the RPC may have completed.
		// Cannot call handlePromptResult here because finish() has
		// already closed the output channel — emit() would panic.
		select {
		case err := <-errCh:
			if err != nil {
				return fmt.Errorf("acp: prompt: %w", err)
			}
			return nil // RPC succeeded; process exited before MessageResult could be emitted
		default:
		}
		return agentrun.ErrTerminated
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handlePromptResult processes a completed prompt RPC, emitting MessageResult on success.
func (p *process) handlePromptResult(err error, result *promptResult) error {
	if err != nil {
		return fmt.Errorf("acp: prompt: %w", err)
	}
	msg := agentrun.Message{
		Type:       agentrun.MessageResult,
		StopReason: stoputil.Sanitize(result.StopReason),
		Timestamp:  time.Now(),
	}
	// Turn-level token usage only — context window fields (ContextSizeTokens,
	// ContextUsedTokens) are surfaced separately via parseUsageUpdate on
	// MessageContextWindow messages.
	if u := result.Usage; u != nil {
		if u.InputTokens != 0 || u.OutputTokens != 0 ||
			u.CachedReadTokens != 0 || u.CachedWriteTokens != 0 || u.ThoughtTokens != 0 {
			msg.Usage = &agentrun.Usage{
				InputTokens:      u.InputTokens,
				OutputTokens:     u.OutputTokens,
				CacheReadTokens:  u.CachedReadTokens,
				CacheWriteTokens: u.CachedWriteTokens,
				ThinkingTokens:   u.ThoughtTokens,
			}
		}
	}
	p.emit(msg)
	return nil
}

// Stop terminates the session. Safe to call multiple times.
func (p *process) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() {
		p.stopping.Store(true)

		// Send shutdown notification (best-effort).
		if p.conn != nil {
			_ = p.conn.Notify(MethodShutdown, nil)
		}

		// Close stdin to signal EOF.
		if p.stdin != nil {
			_ = p.stdin.Close()
		}

		// Cancel process context to unblock emit().
		p.cancel()

		// SIGTERM → grace → SIGKILL.
		_ = signalProcess(p.cmd.Process, syscall.SIGTERM)

		select {
		case <-p.done:
		case <-time.After(p.opts.GracePeriod):
			_ = signalProcess(p.cmd.Process, os.Kill)
			<-p.done
		case <-ctx.Done():
			_ = signalProcess(p.cmd.Process, os.Kill)
			<-p.done
		}
	})

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

// emit sends a message to the output channel. Blocks until delivered,
// context is cancelled, or the channel is marked closed by finish().
//
// Holds outputMu for the entire check+send to prevent a data race with
// finish() closing the channel. This is safe because finish() calls
// p.cancel() before acquiring outputMu — any emit() blocked on a full
// channel unblocks via ctx.Done() and releases the mutex.
func (p *process) emit(msg agentrun.Message) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	if p.outputClosed {
		return
	}
	select {
	case p.output <- msg:
	case <-p.ctx.Done():
	}
}

// finish sets the terminal error and closes output+done channels.
// Cancels the context first so any emit() blocked on a full output channel
// unblocks via ctx.Done(), then acquires outputMu to safely close.
// finish sets the terminal error and closes done+output channels.
//
// Close order matters: done must close before output so that Err()
// returns the terminal error immediately after a consumer's range
// over Output() exits. Closing output first creates a race —
// the consumer goroutine can call Err() before done is closed.
func (p *process) finish(err error) {
	p.finishOnce.Do(func() {
		if p.stopping.Load() {
			err = agentrun.ErrTerminated
		}
		p.termErr = err
		p.cancel() // unblock any emit() blocked in select

		close(p.done)

		p.outputMu.Lock()
		p.outputClosed = true
		close(p.output)
		p.outputMu.Unlock()
	})
}

// waitCmd waits for the subprocess to exit and returns its error.
func (p *process) waitCmd() error {
	return p.cmd.Wait()
}

// kill forcefully terminates the subprocess and waits for the ReadLoop
// goroutine to call finish(). Does not call cmd.Wait() directly — the
// ReadLoop goroutine is the sole caller to avoid races.
func (p *process) kill() {
	p.stopping.Store(true)
	p.cancel()
	_ = signalProcess(p.cmd.Process, os.Kill)
	<-p.done // ReadLoop goroutine calls finish(waitCmd())
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

// wrapExitError converts a non-zero *exec.ExitError to *agentrun.ExitError.
// nil → nil, non-ExitError → passthrough, code 0 → nil (clean exit).
// Preserves the error chain via ExitError.Unwrap.
//
// NOTE: intentionally duplicated in engine/cli/process.go — keep in sync.
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
// No lock needed: cmd is immutable after newProcess() returns (write-once).
// Contrast with CLI engine's processMetaSnapshot which locks p.mu because
// CLI reassigns cmd on resumeAfterCleanExit for spawn-per-turn backends.
func (p *process) processMetaSnapshot() *agentrun.ProcessMeta {
	if p.cmd == nil || p.cmd.Process == nil || p.cmd.Process.Pid <= 0 {
		return nil
	}
	return &agentrun.ProcessMeta{
		PID:    p.cmd.Process.Pid,
		Binary: p.cmd.Path,
	}
}

// --- Handshake ---

// makeUpdateHandler returns a notification handler that parses session/update
// params and sends the resulting message to updateCh. Runs synchronously in
// ReadLoop but writes to updateCh (not the output channel) to avoid blocking
// RPC response dispatch.
func makeUpdateHandler(p *process, updateCh chan<- agentrun.Message) func(json.RawMessage) {
	return func(params json.RawMessage) {
		var notif sessionNotification
		if err := json.Unmarshal(params, &notif); err != nil {
			msg := agentrun.Message{
				Type:      agentrun.MessageError,
				Content:   fmt.Sprintf("acp: unmarshal update params: %v", err),
				Timestamp: time.Now(),
			}
			select {
			case updateCh <- msg:
			case <-p.ctx.Done():
			}
			return
		}
		msg := parseSessionUpdate(notif.Update)
		if msg == nil {
			return // parser returned nil (no data to report)
		}
		select {
		case updateCh <- *msg:
		case <-p.ctx.Done():
		}
	}
}

// handshakeResult groups the outputs of openSession/resumeSession.
// Avoids growing positional return values as new fields are threaded.
type handshakeResult struct {
	sessionID     string
	modes         *sessionModeState
	models        *sessionModelState
	configOptions []sessionConfigOption
}

// buildInitMeta constructs InitMeta from initialize + session results.
// Returns nil when no meaningful data is available (nil-guard contract).
// Sanitizes all fields at construction time.
func buildInitMeta(initResult *initializeResult, models *sessionModelState) *agentrun.InitMeta {
	var meta agentrun.InitMeta

	if initResult != nil && initResult.AgentInfo != nil {
		meta.AgentName = errfmt.SanitizeCode(initResult.AgentInfo.Name)
		meta.AgentVersion = errfmt.SanitizeCode(initResult.AgentInfo.Version)
	}
	if models != nil && models.CurrentModelID != "" {
		meta.Model = errfmt.SanitizeCode(models.CurrentModelID)
	}

	// Nil-guard: only return non-nil when at least one field is set.
	if meta.Model == "" && meta.AgentName == "" && meta.AgentVersion == "" {
		return nil
	}
	return &meta
}

// handshake performs initialize + session/new (or session/load) and emits MessageInit.
// After emitting MessageInit, applies session configuration (mode, model).
func (p *process) handshake(ctx context.Context, session agentrun.Session) error {
	// Step 1: Initialize.
	initParams := initializeParams{
		ProtocolVersion:    protocolVersion,
		ClientInfo:         &implementation{Name: clientName, Version: clientVersion},
		ClientCapabilities: &clientCapabilities{}, // no fs/terminal for MVP
	}
	var initResult initializeResult
	if err := p.conn.Call(ctx, MethodInitialize, initParams, &initResult); err != nil {
		return fmt.Errorf("acp: initialize: %w", err)
	}

	// Step 2: Session — resume existing or create new.
	var hr handshakeResult
	var err error
	if resumeID := session.Options[agentrun.OptionResumeID]; resumeID != "" {
		hr, err = p.resumeSession(ctx, resumeID, session.CWD)
	} else {
		hr, err = p.openSession(ctx, session)
	}
	if err != nil {
		return err
	}

	// Validate and store session ID.
	if err := validateSessionID(hr.sessionID); err != nil {
		return fmt.Errorf("acp: invalid session ID from agent: %w", err)
	}
	p.sessionID = hr.sessionID

	// Step 3: Emit MessageInit (before config application — consumers need session ID).
	p.emit(agentrun.Message{
		Type:      agentrun.MessageInit,
		ResumeID:  p.sessionID,
		Init:      buildInitMeta(&initResult, hr.models),
		Process:   p.processMetaSnapshot(),
		Timestamp: time.Now(),
	})

	// Step 4: Apply session configuration.
	return p.applySessionConfig(ctx, session, hr.modes, hr.configOptions)
}

// resumeSession loads an existing session by ID.
// Returns a handshakeResult (sessionID from resumeID, since LoadSessionResult has no sessionId).
func (p *process) resumeSession(ctx context.Context, resumeID, cwd string) (handshakeResult, error) {
	if err := validateSessionID(resumeID); err != nil {
		return handshakeResult{}, fmt.Errorf("%w: invalid resume ID: %w", agentrun.ErrSessionNotFound, err)
	}
	params := loadSessionParams{
		SessionID:  resumeID,
		CWD:        cwd,
		MCPServers: []mcpServer{}, // empty slice, never nil
	}
	var result loadSessionResult
	if err := p.conn.Call(ctx, MethodSessionLoad, params, &result); err != nil {
		return handshakeResult{}, fmt.Errorf("%w: session/load: %w", agentrun.ErrSessionNotFound, err)
	}
	// LoadSessionResult has NO sessionId — use resumeID directly.
	return handshakeResult{
		sessionID:     resumeID,
		modes:         result.Modes,
		models:        result.Models,
		configOptions: result.ConfigOptions,
	}, nil
}

// openSession creates a new session with the given configuration.
func (p *process) openSession(ctx context.Context, session agentrun.Session) (handshakeResult, error) {
	params := newSessionParams{
		CWD:        session.CWD,
		MCPServers: []mcpServer{}, // empty slice, never nil
	}
	var result newSessionResult
	if err := p.conn.Call(ctx, MethodSessionNew, params, &result); err != nil {
		return handshakeResult{}, fmt.Errorf("acp: session/new: %w", err)
	}
	return handshakeResult{
		sessionID:     result.SessionID,
		modes:         result.Modes,
		models:        result.Models,
		configOptions: result.ConfigOptions,
	}, nil
}

// sessionIDPattern matches safe session identifiers (relaxed to 256 for real agent IDs).
var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-]{1,256}$`)

func validateSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("session ID %q does not match allowed pattern", id)
	}
	return nil
}

// --- Session config application ---

// configCall represents a single RPC call to apply session configuration.
type configCall struct {
	Method string
	Params any
}

// sessionConfigCalls returns the RPC calls needed to apply session options.
// Pure function: no I/O, testable in isolation.
// Only emits session/set_mode if the agent advertised modes in its session result.
func sessionConfigCalls(sessionID string, session agentrun.Session, modes *sessionModeState, configOptions []sessionConfigOption) []configCall {
	var calls []configCall

	// Mode setting — only if agent advertised modes.
	if mode := session.Options[agentrun.OptionMode]; mode != "" && modes != nil && len(modes.AvailableModes) > 0 {
		calls = append(calls, configCall{
			Method: MethodSessionSetMode,
			Params: setModeParams{SessionID: sessionID, ModeID: mode},
		})
	}

	// Model setting via config option.
	if session.Model != "" {
		// Find a config option with category "model".
		for _, opt := range configOptions {
			if opt.Category == "model" {
				calls = append(calls, configCall{
					Method: MethodSessionSetConfig,
					Params: setConfigOptionParams{SessionID: sessionID, ConfigID: opt.ID, Value: session.Model},
				})
				break
			}
		}
	}

	return calls
}

// applySessionConfig applies mode and model settings after session creation.
// session/set_mode failure is fatal (security boundary).
// session/set_config_option failure is non-fatal (emits MessageError).
func (p *process) applySessionConfig(ctx context.Context, session agentrun.Session, modes *sessionModeState, configOptions []sessionConfigOption) error {
	calls := sessionConfigCalls(p.sessionID, session, modes, configOptions)
	for _, c := range calls {
		var result json.RawMessage
		err := p.conn.Call(ctx, c.Method, c.Params, &result)
		if err != nil {
			if c.Method == MethodSessionSetMode {
				return fmt.Errorf("acp: session/set_mode failed (security-critical): %w", err)
			}
			// Non-fatal: emit error and continue.
			p.emit(agentrun.Message{
				Type:      agentrun.MessageError,
				Content:   fmt.Sprintf("acp: %s: %v", c.Method, err),
				Timestamp: time.Now(),
			})
		}
	}
	return nil
}

// --- Permission handling ---

// makePermissionHandler builds the OnMethod handler for session/request_permission.
// The actual handler runs in a dedicated goroutine (dispatched by Conn.handleMethodCall).
// It maps between the ACP option-based wire format and the public bool-based PermissionHandler.
func (p *process) makePermissionHandler(hitl agentrun.HITL, opts EngineOptions) func(json.RawMessage) (any, error) {
	return func(params json.RawMessage) (any, error) {
		var wireReq requestPermissionParams
		if err := json.Unmarshal(params, &wireReq); err != nil {
			p.emit(agentrun.Message{
				Type:      agentrun.MessageError,
				Content:   fmt.Sprintf("acp: unmarshal permission request: %v", err),
				Timestamp: time.Now(),
			})
			return cancelledPermission(), nil
		}

		// HITL off → auto-approve.
		if hitl == agentrun.HITLOff {
			return selectPermissionOption(wireReq.Options, "allow_once", "allow_always"), nil
		}

		// No handler → auto-deny.
		if opts.PermissionHandler == nil {
			return selectPermissionOption(wireReq.Options, "reject_once", "reject_always"), nil
		}

		// Call handler with timeout + panic recovery.
		ctx, cancel := context.WithTimeout(p.ctx, opts.PermissionTimeout)
		defer cancel()

		pubReq := PermissionRequest{
			SessionID:   wireReq.SessionID,
			ToolName:    wireReq.ToolCall.Title,
			ToolCallID:  wireReq.ToolCall.ToolCallID,
			Description: wireReq.ToolCall.Kind,
		}
		approved, err := safeCallPermissionHandler(ctx, opts.PermissionHandler, pubReq)
		if err != nil {
			p.emit(agentrun.Message{
				Type:      agentrun.MessageError,
				Content:   fmt.Sprintf("acp: permission handler error: %v", err),
				Timestamp: time.Now(),
			})
			return cancelledPermission(), nil
		}

		if approved {
			return selectPermissionOption(wireReq.Options, "allow_once", "allow_always"), nil
		}
		return selectPermissionOption(wireReq.Options, "reject_once", "reject_always"), nil
	}
}

// firstOptionByKind finds the first option matching any of the given kinds.
func firstOptionByKind(options []permissionOpt, kinds ...string) string {
	for _, opt := range options {
		for _, k := range kinds {
			if opt.Kind == k {
				return opt.OptionID
			}
		}
	}
	return ""
}

// cancelledPermission returns a cancelled permission outcome.
func cancelledPermission() requestPermissionResult {
	return requestPermissionResult{
		Outcome: requestPermissionOutcome{Outcome: "cancelled"},
	}
}

// selectPermissionOption finds the first option matching any of the given kinds
// and returns a selected outcome. Falls back to cancelled if no match.
func selectPermissionOption(options []permissionOpt, kinds ...string) requestPermissionResult {
	optID := firstOptionByKind(options, kinds...)
	if optID == "" {
		return cancelledPermission()
	}
	return requestPermissionResult{
		Outcome: requestPermissionOutcome{Outcome: "selected", OptionID: optID},
	}
}

// safeCallPermissionHandler calls h with panic recovery.
func safeCallPermissionHandler(ctx context.Context, h PermissionHandler, req PermissionRequest) (approved bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("permission handler panic: %v", r)
		}
	}()
	return h(ctx, req)
}
