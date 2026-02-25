//go:build !windows

package acp

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/dmora/agentrun"
)

// updateQueueSize is the buffer for decoupling notification dispatch from
// ReadLoop, preventing deadlock when the output channel is full during Send().
// If the agent emits more than updateQueueSize notifications before the
// consumer drains any from Output(), ReadLoop blocks, stalling RPC dispatch.
// Consumers MUST drain Output() concurrently with Send() for long turns.
const updateQueueSize = 1024

// Engine is an ACP engine that communicates with agents via JSON-RPC 2.0
// over a persistent subprocess's stdin/stdout.
type Engine struct {
	opts EngineOptions
}

var _ agentrun.Engine = (*Engine)(nil)

// NewEngine creates an ACP engine. Use EngineOption functions to customize
// the binary, arguments, buffer sizes, and permission handling.
func NewEngine(opts ...EngineOption) *Engine {
	return &Engine{opts: resolveEngineOptions(opts...)}
}

// Validate checks that the engine's binary is configured and available on PATH.
func (e *Engine) Validate() error {
	_, err := e.resolveBinary()
	return err
}

// resolveBinary checks for a configured binary and resolves it via PATH.
func (e *Engine) resolveBinary() (string, error) {
	if e.opts.Binary == "" {
		return "", fmt.Errorf("%w: no binary configured (use WithBinary)", agentrun.ErrUnavailable)
	}
	resolved, err := exec.LookPath(e.opts.Binary)
	if err != nil {
		return "", fmt.Errorf("%w: %s: %w", agentrun.ErrUnavailable, e.opts.Binary, err)
	}
	return resolved, nil
}

// Start spawns the ACP subprocess, performs the initialize + session handshake,
// and returns a Process ready for multi-turn conversation.
//
// Session.AgentID is silently ignored — ACP agents are identified by binary.
func (e *Engine) Start(ctx context.Context, session agentrun.Session, opts ...agentrun.Option) (agentrun.Process, error) {
	startOpts := agentrun.ResolveOptions(opts...)

	session = cloneSession(session)
	if startOpts.Model != "" {
		session.Model = startOpts.Model
	}

	// Validate HITL option.
	hitl := agentrun.HITL(session.Options[agentrun.OptionHITL])
	if hitl != "" && !hitl.Valid() {
		return nil, fmt.Errorf("acp: invalid HITL value: %q", hitl)
	}

	// Validate CWD.
	if session.CWD != "" && !filepath.IsAbs(session.CWD) {
		return nil, fmt.Errorf("acp: CWD must be an absolute path, got %q", session.CWD)
	}

	// Spawn subprocess.
	cmd, stdin, stdout, err := e.spawnSubprocess(session.CWD)
	if err != nil {
		return nil, err
	}

	p := newProcess(cmd, stdin, e.opts)
	conn := newConn(stdout, stdin, connConfig{
		maxMessageSize: e.opts.MaxMessageSize,
		onParseError: func(_ []byte, err error) {
			p.emit(agentrun.Message{
				Type:      agentrun.MessageError,
				Content:   fmt.Sprintf("acp: malformed JSON from agent: %v", err),
				Timestamp: time.Now(),
			})
		},
	})

	wireReadLoop(conn, p, hitl, e.opts)

	// Handshake with timeout.
	hsCtx := ctx
	if e.opts.HandshakeTimeout > 0 {
		var hsCancel context.CancelFunc
		hsCtx, hsCancel = context.WithTimeout(ctx, e.opts.HandshakeTimeout)
		defer hsCancel()
	}

	if err := p.handshake(hsCtx, session); err != nil {
		p.kill()
		return nil, err
	}

	return p, nil
}

// spawnSubprocess resolves the binary and starts the ACP agent process.
func (e *Engine) spawnSubprocess(cwd string) (*exec.Cmd, io.WriteCloser, io.ReadCloser, error) {
	resolvedBinary, err := e.resolveBinary()
	if err != nil {
		return nil, nil, nil, err
	}

	cmd := exec.Command(resolvedBinary, e.opts.Args...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("acp: start: %w", err)
	}

	return cmd, stdin, stdout, nil
}

// wireReadLoop registers handlers on the Conn, starts the dispatch goroutine,
// and launches ReadLoop in the background. On ReadLoop exit, queued updates
// are drained and the process is finished.
func wireReadLoop(conn *Conn, p *process, hitl agentrun.HITL, opts EngineOptions) {
	updateCh := make(chan agentrun.Message, updateQueueSize)
	conn.OnNotification(MethodSessionUpdate, makeUpdateHandler(p, updateCh))
	conn.OnMethod(MethodRequestPerm, p.makePermissionHandler(hitl, opts))
	p.conn = conn

	// Dispatch goroutine: drains updateCh → output channel.
	var dispatchDone sync.WaitGroup
	dispatchDone.Add(1)
	go func() {
		defer dispatchDone.Done()
		for msg := range updateCh {
			p.emit(msg)
		}
	}()

	// ReadLoop goroutine: sole writer to output channel.
	go func() {
		conn.ReadLoop()
		close(updateCh)     // signal dispatch goroutine to finish
		dispatchDone.Wait() // wait for all queued updates to be emitted
		p.finish(p.waitCmd())
	}()
}

// cloneSession returns a deep copy of session, cloning the Options map.
func cloneSession(s agentrun.Session) agentrun.Session {
	if s.Options != nil {
		s.Options = maps.Clone(s.Options)
	}
	return s
}
