package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Conn is a bidirectional JSON-RPC 2.0 multiplexer over newline-delimited JSON.
//
// Conn serializes outbound messages (Call, Notify) via a mutex-protected encoder
// and dispatches inbound messages (responses, notifications, method calls) in
// ReadLoop. All handlers must be registered before ReadLoop starts.
//
// The synchronization model uses sync.Mutex + map[int64]chan for pending calls.
// On ReadLoop exit, all pending channels receive an error — preventing goroutine leaks.
type Conn struct {
	mu  sync.Mutex
	enc *json.Encoder
	w   io.Writer

	nextID  atomic.Int64
	pending map[int64]chan *rpcResponse

	notifyHandlers map[string]func(json.RawMessage)
	methodHandlers map[string]func(json.RawMessage) (any, error)
	onParseError   func(line []byte, err error)

	scanner *bufio.Scanner

	done    chan struct{}
	readErr atomic.Value // stores error (nil = no error)

	maxMessageSize int
}

// connConfig holds optional configuration for a Conn.
type connConfig struct {
	maxMessageSize int
	onParseError   func(line []byte, err error)
}

// newConn creates a JSON-RPC 2.0 connection reading from r and writing to w.
// Call ReadLoop in a goroutine to start processing inbound messages.
func newConn(r io.Reader, w io.Writer, cfg connConfig) *Conn {
	maxSize := cfg.maxMessageSize
	if maxSize <= 0 {
		maxSize = defaultMaxMessageSize
	}
	c := &Conn{
		w:              w,
		enc:            json.NewEncoder(w),
		pending:        make(map[int64]chan *rpcResponse),
		notifyHandlers: make(map[string]func(json.RawMessage)),
		methodHandlers: make(map[string]func(json.RawMessage) (any, error)),
		onParseError:   cfg.onParseError,
		done:           make(chan struct{}),
		maxMessageSize: maxSize,
	}
	c.scanner = newScanner(r, c.maxMessageSize)
	return c
}

func newScanner(r io.Reader, maxSize int) *bufio.Scanner {
	s := bufio.NewScanner(r)
	initCap := min(4096, maxSize)
	s.Buffer(make([]byte, 0, initCap), maxSize)
	return s
}

// OnNotification registers a handler for JSON-RPC notifications (no id field).
// Must be called before ReadLoop starts.
func (c *Conn) OnNotification(method string, h func(json.RawMessage)) {
	c.notifyHandlers[method] = h
}

// OnMethod registers a handler for JSON-RPC method calls (has id field, expects response).
// The handler runs in a dedicated goroutine to avoid blocking ReadLoop.
// Must be called before ReadLoop starts.
func (c *Conn) OnMethod(method string, h func(json.RawMessage) (any, error)) {
	c.methodHandlers[method] = h
}

// Call sends a JSON-RPC request and blocks until the response arrives or ctx expires.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)

	ch := make(chan *rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	req := &rpcRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	if err := c.send(req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("acp: send %s: %w", method, err)
	}

	select {
	case resp, ok := <-ch:
		return c.handleCallResponse(resp, ok, method, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// Response may have arrived just before ctx cancellation —
		// drain ch to avoid discarding a successful result.
		select {
		case resp, ok := <-ch:
			return c.handleCallResponse(resp, ok, method, result)
		default:
			return ctx.Err()
		}
	}
}

// handleCallResponse processes a response received from a pending Call channel.
func (c *Conn) handleCallResponse(resp *rpcResponse, ok bool, method string, result any) error {
	if !ok {
		return fmt.Errorf("acp: %s: connection closed", method)
	}
	if resp.Error != nil {
		return &RPCError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
		}
	}
	if result != nil && resp.Result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("acp: unmarshal %s result: %w", method, err)
		}
	}
	return nil
}

// Notify sends a JSON-RPC notification (no id, no response expected).
func (c *Conn) Notify(method string, params any) error {
	req := &rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.send(req)
}

// ReadLoop reads and dispatches inbound JSON-RPC messages until the reader
// closes or an unrecoverable error occurs. On exit, all pending Call channels
// are closed with an error. Must be called exactly once.
func (c *Conn) ReadLoop() {
	defer close(c.done)
	defer c.drainPending()

	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue // skip blank lines and non-JSON (e.g. agent startup banners)
		}

		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			if c.onParseError != nil {
				c.onParseError(append([]byte(nil), line...), err)
			}
			continue
		}

		c.dispatch(&msg)
	}

	if err := c.scanner.Err(); err != nil {
		c.readErr.Store(err)
	}
}

// Err returns the ReadLoop error after it exits. Returns nil if ReadLoop
// hasn't finished or exited cleanly (reader closed with no scanner error).
func (c *Conn) Err() error {
	if v := c.readErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// Done returns a channel that is closed when ReadLoop exits.
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Standard JSON-RPC 2.0 error codes.
const (
	rpcMethodNotFound   = -32601
	rpcInternalError    = -32603
	rpcApplicationError = -32000
)

// --- Internal ---

// send serializes and writes a JSON-RPC message. Thread-safe.
func (c *Conn) send(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(v)
}

// dispatch routes an inbound message to the appropriate handler.
func (c *Conn) dispatch(msg *rpcMessage) {
	// Response (has id + result or error, no method).
	if msg.ID != nil && msg.Method == "" {
		c.handleResponse(msg)
		return
	}

	// Method call from agent (has id + method).
	if msg.ID != nil && msg.Method != "" {
		c.handleMethodCall(msg)
		return
	}

	// Notification (no id, has method).
	if msg.Method != "" {
		c.handleNotification(msg)
		return
	}
}

// handleResponse delivers a response to the waiting Call goroutine.
func (c *Conn) handleResponse(msg *rpcMessage) {
	c.mu.Lock()
	ch, ok := c.pending[*msg.ID]
	if ok {
		delete(c.pending, *msg.ID)
	}
	c.mu.Unlock()

	if !ok {
		return // duplicate or unsolicited response — drop
	}

	resp := &rpcResponse{
		Result: msg.Result,
		Error:  msg.Error,
	}
	ch <- resp
}

// handleMethodCall dispatches a method call to a registered handler in a
// dedicated goroutine. Sends the response back to the agent.
func (c *Conn) handleMethodCall(msg *rpcMessage) {
	h, ok := c.methodHandlers[msg.Method]
	if !ok {
		// No handler — send method-not-found error.
		c.sendError(*msg.ID, rpcMethodNotFound, "method not found: "+msg.Method)
		return
	}

	// Run handler in a dedicated goroutine to avoid blocking ReadLoop.
	id := *msg.ID
	params := msg.Params
	go func() {
		result, err := h(params)
		if err != nil {
			c.sendError(id, rpcApplicationError, err.Error())
			return
		}
		c.sendResult(id, result)
	}()
}

// handleNotification dispatches a notification to a registered handler.
func (c *Conn) handleNotification(msg *rpcMessage) {
	h, ok := c.notifyHandlers[msg.Method]
	if !ok {
		return // unknown notification — silently ignore
	}
	h(msg.Params)
}

// sendResult sends a JSON-RPC success response.
// Send errors are intentionally ignored: these run in handler goroutines
// during ReadLoop, and the connection may already be closing. The agent
// will time out if it never receives a response.
func (c *Conn) sendResult(id int64, result any) {
	data, err := json.Marshal(result)
	if err != nil {
		c.sendError(id, rpcInternalError, "marshal result: "+err.Error())
		return
	}
	resp := &rpcResponse{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  data,
	}
	_ = c.send(resp) // best-effort — connection may be closing
}

// sendError sends a JSON-RPC error response.
// Send errors are intentionally ignored (same rationale as sendResult).
func (c *Conn) sendError(id int64, code int, message string) {
	resp := &rpcResponse{
		JSONRPC: "2.0",
		ID:      &id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
	_ = c.send(resp) // best-effort
}

// drainPending closes all pending Call channels so blocked callers unblock.
func (c *Conn) drainPending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// --- Wire types ---

// rpcRequest is an outbound JSON-RPC 2.0 request or notification.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcMessage is a generic inbound JSON-RPC 2.0 message (request, response, or notification).
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcResponse is an outbound JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RPCError is an exported error type for JSON-RPC errors returned by Call.
type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}
