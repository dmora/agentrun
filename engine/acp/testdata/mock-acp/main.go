//go:build ignore

// Command mock-acp simulates an ACP agent for integration tests.
// It implements the JSON-RPC 2.0 ACP protocol over stdin/stdout:
// initialize, session/new, session/load, session/prompt,
// session/set_mode, session/set_config_option, shutdown.
//
// Environment variables control failure modes:
//
//	ACP_MOCK_MODE=handshake-crash   — exit after initialize (before session/new)
//	ACP_MOCK_MODE=init-error        — return JSON-RPC error to initialize
//	ACP_MOCK_MODE=session-not-found — return error for session/load
//	ACP_MOCK_MODE=permission        — send session/request_permission during prompt
//	ACP_MOCK_MODE=echo-cwd          — include received CWD in session ID
//	ACP_MOCK_MODE=set-mode-fail     — return error for session/set_mode
//	ACP_MOCK_MODE=set-config-fail   — return error for session/set_config_option
//	ACP_MOCK_MODE=slow-prompt       — delay prompt response by 2s (for ctx cancel tests)
//	ACP_MOCK_MODE=prompt-then-exit  — respond to prompt then exit (for done+errCh race test)
//	ACP_MOCK_MODE=rich-usage        — respond with extended usage (cache, thinking tokens)
//	ACP_MOCK_MODE=no-usage          — respond with no usage at all (nil)
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var (
	enc     = json.NewEncoder(os.Stdout)
	scanner = bufio.NewScanner(os.Stdin)
	mode    = os.Getenv("ACP_MOCK_MODE")
	nextID  int64
)

func main() {
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		handleRequest(&req)
	}
}

func handleRequest(req *rpcRequest) {
	switch req.Method {
	case "initialize":
		handleInitialize(req)
	case "session/new":
		handleSessionNew(req)
	case "session/load":
		handleSessionLoad(req)
	case "session/prompt":
		handleSessionPrompt(req)
	case "session/set_mode":
		handleSetMode(req)
	case "session/set_config_option":
		handleSetConfigOption(req)
	case "shutdown":
		os.Exit(0)
	}
}

func handleInitialize(req *rpcRequest) {
	if mode == "init-error" {
		respondError(req.ID, -32600, "mock init error")
		return
	}
	respond(req.ID, map[string]any{
		"protocolVersion": 1,
		"agentCapabilities": map[string]any{
			"loadSession": true,
		},
		"agentInfo": map[string]string{
			"name":    "mock-acp",
			"version": "0.1.0",
		},
		"authMethods": []any{},
	})
	if mode == "handshake-crash" {
		os.Exit(1)
	}
}

func handleSessionNew(req *rpcRequest) {
	var params struct {
		CWD string `json:"cwd"`
	}
	_ = json.Unmarshal(req.Params, &params)

	sessionID := "mock-session-001"
	if mode == "echo-cwd" {
		sessionID = "cwd-" + sanitizeCWD(params.CWD)
	}

	respond(req.ID, map[string]any{
		"sessionId": sessionID,
		"modes": map[string]any{
			"currentModeId": "code",
			"availableModes": []map[string]string{
				{"id": "code", "name": "Code"},
				{"id": "plan", "name": "Plan"},
			},
		},
		"configOptions": []map[string]any{
			{
				"id":           "model",
				"name":         "Model",
				"category":     "model",
				"type":         "select",
				"currentValue": "default-model",
				"options": []map[string]string{
					{"value": "default-model", "name": "Default"},
					{"value": "big-model", "name": "Big"},
				},
			},
		},
	})
}

func handleSessionLoad(req *rpcRequest) {
	if mode == "session-not-found" {
		respondError(req.ID, -32000, "session not found")
		return
	}
	// LoadSessionResult has NO sessionId field.
	respond(req.ID, map[string]any{
		"modes": map[string]any{
			"currentModeId": "code",
			"availableModes": []map[string]string{
				{"id": "code", "name": "Code"},
			},
		},
		"configOptions": []map[string]any{},
	})
}

func handleSessionPrompt(req *rpcRequest) {
	// Slow prompt mode — delay before responding (for ctx cancel tests).
	if mode == "slow-prompt" {
		time.Sleep(2 * time.Second)
	}

	// If permission mode, send a permission request first.
	if mode == "permission" {
		sendPermissionRequest()
	}

	// Extract sessionId from params.
	var params struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(req.Params, &params)
	sid := params.SessionID

	// Emit streaming updates as notifications with new envelope format.
	notifyUpdate(sid, map[string]any{
		"sessionUpdate": "agent_thought_chunk",
		"content":       map[string]string{"type": "text", "text": "Let me"},
	})
	notifyUpdate(sid, map[string]any{
		"sessionUpdate": "agent_thought_chunk",
		"content":       map[string]string{"type": "text", "text": " think"},
	})

	notifyUpdate(sid, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]string{"type": "text", "text": "Hello"},
	})
	notifyUpdate(sid, map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content":       map[string]string{"type": "text", "text": " world"},
	})

	notifyUpdate(sid, map[string]any{
		"sessionUpdate": "tool_call",
		"toolCallId":    "call_001",
		"title":         "read_file",
		"kind":          "read",
		"status":        "pending",
		"rawInput":      map[string]string{"path": "foo.txt"},
	})
	notifyUpdate(sid, map[string]any{
		"sessionUpdate": "tool_call_update",
		"toolCallId":    "call_001",
		"title":         "read_file",
		"status":        "completed",
		"content": []map[string]any{
			{"type": "content", "content": map[string]string{"type": "text", "text": "file contents"}},
		},
	})

	// Send RPC response (turn complete).
	switch mode {
	case "rich-usage":
		respond(req.ID, map[string]any{
			"stopReason": "end_turn",
			"usage": map[string]int{
				"inputTokens":       500,
				"outputTokens":      200,
				"totalTokens":       700,
				"cachedReadTokens":  80,
				"cachedWriteTokens": 30,
				"thoughtTokens":     120,
			},
		})
	case "no-usage":
		respond(req.ID, map[string]any{
			"stopReason": "max_tokens",
		})
	default:
		respond(req.ID, map[string]any{
			"stopReason": "end_turn",
			"usage": map[string]int{
				"inputTokens":  100,
				"outputTokens": 50,
				"totalTokens":  150,
			},
		})
	}

	// Exit immediately after prompt response — exercises the Send()
	// done+errCh race where the process exits right after RPC completes.
	if mode == "prompt-then-exit" {
		os.Exit(0)
	}
}

func handleSetMode(req *rpcRequest) {
	if mode == "set-mode-fail" {
		respondError(req.ID, -32000, "mock set_mode error")
		return
	}
	// Success → null result.
	respond(req.ID, nil)
}

func handleSetConfigOption(req *rpcRequest) {
	if mode == "set-config-fail" {
		respondError(req.ID, -32000, "mock set_config_option error")
		return
	}
	respond(req.ID, map[string]any{
		"configOptions": []map[string]any{},
	})
}

func sendPermissionRequest() {
	nextID++
	id := nextID
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "session/request_permission",
		"params": map[string]any{
			"sessionId": "mock-session-001",
			"toolCall": map[string]any{
				"toolCallId": "call_perm_001",
				"title":      "write_file",
				"kind":       "edit",
				"status":     "pending",
			},
			"options": []map[string]string{
				{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
				{"optionId": "allow-always", "name": "Always allow", "kind": "allow_always"},
				{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
				{"optionId": "reject-always", "name": "Always reject", "kind": "reject_always"},
			},
		},
	}
	_ = enc.Encode(req)

	// Read the permission response.
	if scanner.Scan() {
		// We don't need to process the response for mock purposes.
	}
}

func respond(id *int64, result any) {
	if result == nil {
		_ = enc.Encode(rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result:  json.RawMessage("null"),
		})
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mock-acp: marshal: %v\n", err)
		return
	}
	_ = enc.Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	})
}

func respondError(id *int64, code int, message string) {
	_ = enc.Encode(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}

func notifyUpdate(sessionID string, update any) {
	data, err := json.Marshal(update)
	if err != nil {
		return
	}
	params := map[string]any{
		"sessionId": sessionID,
		"update":    json.RawMessage(data),
	}
	paramsData, err := json.Marshal(params)
	if err != nil {
		return
	}
	_ = enc.Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params":  json.RawMessage(paramsData),
	})
}

// sanitizeCWD makes a CWD path safe for use in a session ID.
func sanitizeCWD(cwd string) string {
	safe := make([]byte, 0, len(cwd))
	for _, b := range []byte(cwd) {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' {
			safe = append(safe, b)
		}
	}
	if len(safe) > 200 {
		safe = safe[:200]
	}
	if len(safe) == 0 {
		return "empty"
	}
	return string(safe)
}
