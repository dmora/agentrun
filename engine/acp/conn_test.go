package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTimeout = 5 * time.Second

// testPeer simulates the remote side of a JSON-RPC connection.
// It reads requests from the Conn's writer and sends raw bytes to the Conn's reader.
type testPeer struct {
	reqCh  chan rpcMessage    // requests/notifications read from Conn output
	sendFn func([]byte) error // write raw bytes to Conn's read pipe
	close  func()             // close the write end of the read pipe
	dec    *json.Decoder      // reads from Conn's write pipe
	done   chan struct{}      // closed when readLoop exits
}

// newTestConn creates a Conn wired to a testPeer via io.Pipe.
// The peer's readLoop goroutine is started automatically.
func newTestConn(t *testing.T) (*Conn, *testPeer) {
	t.Helper()

	// Conn reads from pr1, peer writes to pw1.
	pr1, pw1 := io.Pipe()
	// Conn writes to pw2, peer reads from pr2.
	pr2, pw2 := io.Pipe()

	conn := newConn(pr1, pw2, connConfig{})

	peer := &testPeer{
		reqCh: make(chan rpcMessage, 10),
		sendFn: func(b []byte) error {
			_, err := pw1.Write(b)
			return err
		},
		close: func() { pw1.Close() },
		dec:   json.NewDecoder(pr2),
		done:  make(chan struct{}),
	}

	// Read what Conn writes (requests, notifications).
	go func() {
		defer close(peer.done)
		for {
			var msg rpcMessage
			if err := peer.dec.Decode(&msg); err != nil {
				return
			}
			peer.reqCh <- msg
		}
	}()

	t.Cleanup(func() {
		pw1.Close()
		pw2.Close()
		pr1.Close()
		pr2.Close()
	})

	return conn, peer
}

// sendJSON sends a JSON line to the Conn's reader.
func (p *testPeer) sendJSON(t *testing.T, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data = append(data, '\n')
	if err := p.sendFn(data); err != nil {
		t.Fatalf("sendJSON: %v", err)
	}
}

// readRequest reads the next request from the peer's channel with a timeout.
func (p *testPeer) readRequest(t *testing.T) rpcMessage {
	t.Helper()
	select {
	case msg := <-p.reqCh:
		return msg
	case <-time.After(testTimeout):
		t.Fatal("timeout waiting for request from Conn")
		return rpcMessage{}
	}
}

// respond sends a JSON-RPC response for the given request ID.
func (p *testPeer) respond(t *testing.T, id int64, result any) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  data,
	}
	p.sendJSON(t, resp)
}

// respondError sends a JSON-RPC error response.
func (p *testPeer) respondError(t *testing.T, id int64, code int, message string) {
	t.Helper()
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      &id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
	p.sendJSON(t, resp)
}

func TestConn_Call_Success(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	type echoResult struct {
		Value string `json:"value"`
	}

	// Call in a goroutine, respond from peer.
	var got echoResult
	errCh := make(chan error, 1)
	go func() {
		errCh <- conn.Call(ctx, "echo", map[string]string{"msg": "hello"}, &got)
	}()

	req := peer.readRequest(t)
	if req.Method != "echo" {
		t.Fatalf("method = %q, want %q", req.Method, "echo")
	}
	if req.ID == nil {
		t.Fatal("request has no ID")
	}

	peer.respond(t, *req.ID, echoResult{Value: "hello"})

	if err := <-errCh; err != nil {
		t.Fatalf("Call error: %v", err)
	}
	if got.Value != "hello" {
		t.Errorf("result = %q, want %q", got.Value, "hello")
	}
}

func TestConn_Call_Error(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- conn.Call(ctx, "fail", nil, nil)
	}()

	req := peer.readRequest(t)
	peer.respondError(t, *req.ID, -32600, "bad request")

	err := <-errCh
	if err == nil {
		t.Fatal("expected error")
	}

	rpcErr, ok := asRPCError(err)
	if !ok {
		t.Fatalf("error type = %T, want *RPCError", err)
	}
	if rpcErr.Code != -32600 {
		t.Errorf("code = %d, want %d", rpcErr.Code, -32600)
	}
	if rpcErr.Message != "bad request" {
		t.Errorf("message = %q, want %q", rpcErr.Message, "bad request")
	}
}

func TestConn_Call_Timeout(t *testing.T) {
	conn, _ := newTestConn(t)
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := conn.Call(ctx, "slow", nil, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

// TestConn_Call_ContextCancel_ResponseDrain verifies that a response arriving
// just before context cancellation is not lost. The inner select in Call's
// ctx.Done() path should drain the pending channel.
func TestConn_Call_ContextCancel_ResponseDrain(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	type result struct {
		Value string `json:"value"`
	}

	// Use a manually-cancelled context so we control timing precisely.
	ctx, cancel := context.WithCancel(context.Background())

	var got result
	errCh := make(chan error, 1)
	go func() {
		errCh <- conn.Call(ctx, "echo", nil, &got)
	}()

	// Wait for the request, send response, then cancel immediately.
	req := peer.readRequest(t)
	peer.respond(t, *req.ID, result{Value: "ok"})
	// Small delay to let ReadLoop dispatch the response to the buffered channel.
	time.Sleep(10 * time.Millisecond)
	cancel()

	err := <-errCh
	// The response was sent before cancel — Call should return nil (not ctx.Err()).
	if err != nil {
		t.Errorf("Call = %v, want nil (response arrived before cancel)", err)
	}
	if got.Value != "ok" {
		t.Errorf("result = %q, want %q", got.Value, "ok")
	}
}

func TestConn_Notification_Dispatch(t *testing.T) {
	conn, peer := newTestConn(t)

	received := make(chan json.RawMessage, 1)
	conn.OnNotification("session/update", func(params json.RawMessage) {
		received <- params
	})

	go conn.ReadLoop()

	// Send a notification (no id).
	notification := map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params":  map[string]string{"type": "text-delta"},
	}
	peer.sendJSON(t, notification)

	select {
	case params := <-received:
		var p map[string]string
		if err := json.Unmarshal(params, &p); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if p["type"] != "text-delta" {
			t.Errorf("type = %q, want %q", p["type"], "text-delta")
		}
	case <-time.After(testTimeout):
		t.Fatal("timeout waiting for notification")
	}
}

func TestConn_MethodCall_AutoRespond(t *testing.T) {
	conn, peer := newTestConn(t)

	type testResponse struct {
		OK bool `json:"ok"`
	}

	conn.OnMethod("test/method", func(_ json.RawMessage) (any, error) {
		return testResponse{OK: true}, nil
	})

	go conn.ReadLoop()

	// Send a method call (has id + method).
	id := int64(42)
	methodCall := rpcMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "test/method",
		Params:  json.RawMessage(`{"key":"value"}`),
	}
	peer.sendJSON(t, methodCall)

	// Read the response the Conn sends back.
	resp := peer.readRequest(t)
	if resp.ID == nil || *resp.ID != 42 {
		t.Fatalf("response ID = %v, want 42", resp.ID)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var got testResponse
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK {
		t.Error("expected ok=true")
	}
}

func TestConn_MethodCall_ErrorResponse(t *testing.T) {
	conn, peer := newTestConn(t)

	conn.OnMethod("test/method", func(_ json.RawMessage) (any, error) {
		return nil, fmt.Errorf("denied")
	})

	go conn.ReadLoop()

	id := int64(7)
	peer.sendJSON(t, rpcMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "test/method",
		Params:  json.RawMessage(`{}`),
	})

	resp := peer.readRequest(t)
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Message != "denied" {
		t.Errorf("error message = %q, want %q", resp.Error.Message, "denied")
	}
}

func TestConn_MethodCall_NotFound(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	id := int64(99)
	peer.sendJSON(t, rpcMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "unknown/method",
		Params:  json.RawMessage(`{}`),
	})

	resp := peer.readRequest(t)
	if resp.Error == nil {
		t.Fatal("expected error response for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code = %d, want -32601", resp.Error.Code)
	}
}

func TestConn_ConcurrentRequests(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	const n = 5
	results := make([]string, n)
	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var res struct {
				Value string `json:"value"`
			}
			err := conn.Call(ctx, "echo", map[string]int{"idx": idx}, &res)
			if err != nil {
				t.Errorf("call %d: %v", idx, err)
				return
			}
			results[idx] = res.Value
		}(i)
	}

	// Respond to all requests (may arrive in any order).
	for range n {
		req := peer.readRequest(t)
		var params map[string]int
		_ = json.Unmarshal(req.Params, &params)
		idx := params["idx"]
		peer.respond(t, *req.ID, map[string]string{"value": fmt.Sprintf("reply-%d", idx)})
	}

	wg.Wait()

	for i, r := range results {
		want := fmt.Sprintf("reply-%d", i)
		if r != want {
			t.Errorf("result[%d] = %q, want %q", i, r, want)
		}
	}
}

func TestConn_ResponseNotificationInterleave(t *testing.T) {
	conn, peer := newTestConn(t)

	notifications := make(chan string, 10)
	conn.OnNotification("update", func(params json.RawMessage) {
		var p struct{ Value string }
		_ = json.Unmarshal(params, &p)
		notifications <- p.Value
	})

	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Start 2 concurrent calls.
	type result struct {
		Answer string `json:"answer"`
	}
	var res1, res2 result
	errCh1 := make(chan error, 1)
	errCh2 := make(chan error, 1)

	go func() { errCh1 <- conn.Call(ctx, "q1", nil, &res1) }()
	go func() { errCh2 <- conn.Call(ctx, "q2", nil, &res2) }()

	// Read both requests — arrival order is non-deterministic.
	rawReqs := [2]rpcMessage{peer.readRequest(t), peer.readRequest(t)}

	// Map method→ID for deterministic response targeting.
	idByMethod := make(map[string]int64, 2)
	for _, r := range rawReqs {
		idByMethod[r.Method] = *r.ID
	}

	// Interleave: notification, respond to q2, notification, respond to q1.
	peer.sendJSON(t, map[string]any{"jsonrpc": "2.0", "method": "update", "params": map[string]string{"value": "n1"}})
	peer.respond(t, idByMethod["q2"], result{Answer: "a2"})
	peer.sendJSON(t, map[string]any{"jsonrpc": "2.0", "method": "update", "params": map[string]string{"value": "n2"}})
	peer.respond(t, idByMethod["q1"], result{Answer: "a1"})

	if err := <-errCh1; err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if err := <-errCh2; err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if res1.Answer != "a1" {
		t.Errorf("res1 = %q, want %q", res1.Answer, "a1")
	}
	if res2.Answer != "a2" {
		t.Errorf("res2 = %q, want %q", res2.Answer, "a2")
	}

	// Verify both notifications arrived.
	var notifs []string
	for range 2 {
		select {
		case n := <-notifications:
			notifs = append(notifs, n)
		case <-time.After(testTimeout):
			t.Fatal("timeout waiting for notification")
		}
	}
	if len(notifs) != 2 {
		t.Errorf("got %d notifications, want 2", len(notifs))
	}
}

func TestConn_DuplicateResponseID(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	var res struct{ Value string }
	errCh := make(chan error, 1)
	go func() { errCh <- conn.Call(ctx, "test", nil, &res) }()

	req := peer.readRequest(t)

	// First response — should succeed.
	peer.respond(t, *req.ID, map[string]string{"value": "first"})

	if err := <-errCh; err != nil {
		t.Fatalf("Call: %v", err)
	}
	if res.Value != "first" {
		t.Errorf("value = %q, want %q", res.Value, "first")
	}

	// Second response with same ID — should be silently dropped.
	peer.respond(t, *req.ID, map[string]string{"value": "second"})

	// Give ReadLoop time to process (no crash = pass).
	time.Sleep(50 * time.Millisecond)
}

func TestConn_Close_WhileIdle(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	peer.close()

	select {
	case <-conn.Done():
	case <-time.After(testTimeout):
		t.Fatal("ReadLoop didn't exit after close")
	}

	if err := conn.Err(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConn_Close_WhilePending(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- conn.Call(ctx, "pending", nil, nil) }()

	// Wait for the request to be sent.
	peer.readRequest(t)

	// Close the connection without responding.
	peer.close()

	err := <-errCh
	if err == nil {
		t.Fatal("expected error for pending call after close")
	}
	if !strings.Contains(err.Error(), "connection closed") {
		t.Errorf("error = %v, want 'connection closed'", err)
	}
}

func TestConn_MalformedJSON_Skipped(t *testing.T) {
	conn, peer := newTestConn(t)

	received := make(chan struct{}, 1)
	conn.OnNotification("ping", func(_ json.RawMessage) {
		received <- struct{}{}
	})

	go conn.ReadLoop()

	// Send garbage — should be silently skipped.
	_ = peer.sendFn([]byte("not-json\n"))
	_ = peer.sendFn([]byte("{truncated\n"))

	// Send a valid notification — should still dispatch.
	peer.sendJSON(t, map[string]any{"jsonrpc": "2.0", "method": "ping"})

	select {
	case <-received:
	case <-time.After(testTimeout):
		t.Fatal("valid notification not dispatched after malformed JSON")
	}
}

func TestConn_Notify(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	if err := conn.Notify("shutdown", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	msg := peer.readRequest(t)
	if msg.Method != "shutdown" {
		t.Errorf("method = %q, want %q", msg.Method, "shutdown")
	}
	if msg.ID != nil {
		t.Error("notification should not have an ID")
	}
}

func TestConn_Notify_WithParams(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	params := map[string]string{"reason": "user"}
	if err := conn.Notify("shutdown", params); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	msg := peer.readRequest(t)
	var p map[string]string
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p["reason"] != "user" {
		t.Errorf("reason = %q, want %q", p["reason"], "user")
	}
}

func TestConn_Call_NilResult(t *testing.T) {
	conn, peer := newTestConn(t)
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- conn.Call(ctx, "fire_and_forget", nil, nil)
	}()

	req := peer.readRequest(t)
	peer.respond(t, *req.ID, map[string]string{"ignored": "true"})

	if err := <-errCh; err != nil {
		t.Fatalf("Call with nil result: %v", err)
	}
}

func TestConn_Call_SendFailure(t *testing.T) {
	// Create a Conn with a writer that fails immediately.
	pr, pw := io.Pipe()
	pw.Close() // broken pipe — writes will fail

	conn := newConn(pr, pw, connConfig{})
	go conn.ReadLoop()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	err := conn.Call(ctx, "test", nil, nil)
	if err == nil {
		t.Fatal("expected error from broken writer")
	}
	if !strings.Contains(err.Error(), "send") {
		t.Errorf("error = %v, want to contain 'send'", err)
	}

	// Pending map should be cleaned up.
	conn.mu.Lock()
	pending := len(conn.pending)
	conn.mu.Unlock()
	if pending != 0 {
		t.Errorf("pending map has %d entries, want 0", pending)
	}

	pr.Close()
}

// FuzzConn_DecodeMessage verifies that arbitrary bytes never panic ReadLoop.
func FuzzConn_DecodeMessage(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","method":"test"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	f.Add([]byte(`not json`))
	f.Add([]byte{})
	f.Add([]byte(`{"id":null}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		r := strings.NewReader(string(data) + "\n")
		w := io.Discard

		conn := newConn(r, w, connConfig{})
		conn.OnNotification("test", func(_ json.RawMessage) {})

		done := make(chan struct{})
		go func() {
			defer close(done)
			conn.ReadLoop()
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("ReadLoop hung on fuzz input")
		}
	})
}

// asRPCError extracts an *RPCError from err.
func asRPCError(err error) (*RPCError, bool) {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr, true
	}
	return nil, false
}
