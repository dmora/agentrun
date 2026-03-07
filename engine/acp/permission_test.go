package acp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/internal/errfmt"
)

// Test fixture constants shared across permission test files.
const (
	outcomeCancelled = "cancelled"
	outcomeSelected  = "selected"
	toolBash         = "Bash"
	toolWrite        = "Write"
)

func TestSelectPermissionOption_NoMatchingKinds(t *testing.T) {
	options := []permissionOpt{
		{OptionID: "opt-1", Name: "Custom", Kind: "custom_action"},
		{OptionID: "opt-2", Name: "Other", Kind: "other_action"},
	}
	result := selectPermissionOption(options, "allow_once", "allow_always")
	if result.Outcome.Outcome != outcomeCancelled {
		t.Errorf("outcome = %q, want %q", result.Outcome.Outcome, outcomeCancelled)
	}
	if result.Outcome.OptionID != "" {
		t.Errorf("optionID = %q, want empty", result.Outcome.OptionID)
	}
}

func TestSelectPermissionOption_MatchesFirst(t *testing.T) {
	options := []permissionOpt{
		{OptionID: "opt-reject", Name: "Reject", Kind: "reject_once"},
		{OptionID: "opt-allow", Name: "Allow", Kind: "allow_once"},
		{OptionID: "opt-always", Name: "Always", Kind: "allow_always"},
	}
	result := selectPermissionOption(options, "allow_once", "allow_always")
	if result.Outcome.Outcome != outcomeSelected {
		t.Errorf("outcome = %q, want %q", result.Outcome.Outcome, outcomeSelected)
	}
	if result.Outcome.OptionID != "opt-allow" {
		t.Errorf("optionID = %q, want %q", result.Outcome.OptionID, "opt-allow")
	}
}

func TestSelectPermissionOption_EmptyOptions(t *testing.T) {
	result := selectPermissionOption(nil, "allow_once")
	if result.Outcome.Outcome != outcomeCancelled {
		t.Errorf("outcome = %q, want %q", result.Outcome.Outcome, outcomeCancelled)
	}
}

func TestFirstOptionByKind(t *testing.T) {
	options := []permissionOpt{
		{OptionID: "a", Kind: "reject_once"},
		{OptionID: "b", Kind: "allow_once"},
	}
	if got := firstOptionByKind(options, "allow_once"); got != "b" {
		t.Errorf("firstOptionByKind = %q, want %q", got, "b")
	}
	if got := firstOptionByKind(options, "nonexistent"); got != "" {
		t.Errorf("firstOptionByKind = %q, want empty", got)
	}
}

// --- turnDenials unit tests ---

func TestTurnDenials_AddAndSeal(t *testing.T) {
	td := &turnDenials{}
	td.add(toolBash, "no permission handler")
	td.add(toolWrite, "denied by handler")

	denials := td.seal()
	if len(denials) != 2 {
		t.Fatalf("len = %d, want 2", len(denials))
	}
	if denials[0].Tool != toolBash || denials[0].Reason != "no permission handler" {
		t.Errorf("denials[0] = %+v", denials[0])
	}
	if denials[1].Tool != toolWrite || denials[1].Reason != "denied by handler" {
		t.Errorf("denials[1] = %+v", denials[1])
	}
}

func TestTurnDenials_AddAfterSeal(t *testing.T) {
	td := &turnDenials{}
	td.add("First", "reason")
	td.seal()
	td.add("Second", "should be ignored")

	// Seal again should return nil (already sealed, items cleared).
	denials := td.seal()
	if denials != nil {
		t.Errorf("seal after seal should return nil, got %+v", denials)
	}
}

func TestTurnDenials_SealEmpty(t *testing.T) {
	td := &turnDenials{}
	denials := td.seal()
	if denials != nil {
		t.Errorf("seal on empty collector should return nil, got %+v", denials)
	}
}

func TestTurnDenials_ConcurrentAddAndSeal(_ *testing.T) { //nolint:revive // t kept for future assertions
	td := &turnDenials{}
	var wg sync.WaitGroup

	// Launch concurrent adders.
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			td.add("tool", "reason")
		}()
	}

	// Seal concurrently.
	wg.Add(1)
	var denials []agentrun.PermissionDenial
	go func() {
		defer wg.Done()
		denials = td.seal()
	}()

	wg.Wait()
	// No data race under -race. Denials count is non-deterministic
	// due to concurrency, but seal() must not panic.
	_ = denials
}

func TestTurnDenials_Sanitization(t *testing.T) {
	td := &turnDenials{}
	// Control chars in tool → sanitized to empty.
	td.add("bad\x00tool", "good reason")
	// Long reason → truncated.
	td.add("Good", strings.Repeat("x", errfmt.MaxLen+100))

	denials := td.seal()
	if len(denials) != 2 {
		t.Fatalf("len = %d, want 2", len(denials))
	}
	if denials[0].Tool != "" {
		t.Errorf("control-char tool should be sanitized to empty, got %q", denials[0].Tool)
	}
	if len(denials[1].Reason) > errfmt.MaxLen {
		t.Errorf("reason should be truncated to %d, got %d", errfmt.MaxLen, len(denials[1].Reason))
	}
}

// --- denyAllPermHandler tests ---

func TestDenyAllPermHandler(t *testing.T) {
	result, err := denyAllPermHandler(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	permResult, ok := result.(requestPermissionResult)
	if !ok {
		t.Fatalf("result type = %T, want requestPermissionResult", result)
	}
	if permResult.Outcome.Outcome != outcomeCancelled {
		t.Errorf("outcome = %q, want %q", permResult.Outcome.Outcome, outcomeCancelled)
	}
}

// --- makeTurnPermHandler tests ---

func TestMakeTurnPermHandler_NoHandler_RecordsDenial(t *testing.T) {
	p := newTestProcess(t)
	p.opts = EngineOptions{
		PermissionTimeout: 5 * time.Second,
	}
	td := &turnDenials{}
	handler := p.makeTurnPermHandler(td)

	params := mustMarshal(t, requestPermissionParams{
		SessionID: "ses-1",
		ToolCall:  toolCallUpdate{Title: toolBash, ToolCallID: "tc-1", Kind: "shell"},
		Options: []permissionOpt{
			{OptionID: "r1", Kind: "reject_once"},
		},
	})

	result, err := handler(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	permResult := result.(requestPermissionResult)
	if permResult.Outcome.Outcome != outcomeSelected {
		t.Errorf("outcome = %q, want %q", permResult.Outcome.Outcome, outcomeSelected)
	}

	denials := td.seal()
	if len(denials) != 1 {
		t.Fatalf("denials len = %d, want 1", len(denials))
	}
	if denials[0].Tool != toolBash {
		t.Errorf("Tool = %q, want %q", denials[0].Tool, toolBash)
	}
	if denials[0].Reason != "no permission handler" {
		t.Errorf("Reason = %q, want 'no permission handler'", denials[0].Reason)
	}
}

func TestMakeTurnPermHandler_HandlerDenies_RecordsDenial(t *testing.T) {
	p := newTestProcess(t)
	p.opts = EngineOptions{
		PermissionHandler: func(_ context.Context, _ PermissionRequest) (bool, error) {
			return false, nil
		},
		PermissionTimeout: 5 * time.Second,
	}
	td := &turnDenials{}
	handler := p.makeTurnPermHandler(td)

	params := mustMarshal(t, requestPermissionParams{
		SessionID: "ses-1",
		ToolCall:  toolCallUpdate{Title: toolWrite, ToolCallID: "tc-1", Kind: "file"},
		Options: []permissionOpt{
			{OptionID: "r1", Kind: "reject_once"},
		},
	})

	_, err := handler(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	denials := td.seal()
	if len(denials) != 1 {
		t.Fatalf("denials len = %d, want 1", len(denials))
	}
	if denials[0].Tool != toolWrite {
		t.Errorf("Tool = %q, want %q", denials[0].Tool, toolWrite)
	}
	if denials[0].Reason != "denied by handler" {
		t.Errorf("Reason = %q, want 'denied by handler'", denials[0].Reason)
	}
}

func TestMakeTurnPermHandler_HandlerApproves_NoDenial(t *testing.T) {
	p := newTestProcess(t)
	p.opts = EngineOptions{
		PermissionHandler: func(_ context.Context, _ PermissionRequest) (bool, error) {
			return true, nil
		},
		PermissionTimeout: 5 * time.Second,
	}
	td := &turnDenials{}
	handler := p.makeTurnPermHandler(td)

	params := mustMarshal(t, requestPermissionParams{
		SessionID: "ses-1",
		ToolCall:  toolCallUpdate{Title: "Read", ToolCallID: "tc-1", Kind: "file"},
		Options: []permissionOpt{
			{OptionID: "a1", Kind: "allow_once"},
		},
	})

	_, err := handler(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	denials := td.seal()
	if denials != nil {
		t.Errorf("denials should be nil when approved, got %+v", denials)
	}
}

func TestMakeTurnPermHandler_HITLOff_AutoApproves(t *testing.T) {
	p := newTestProcess(t)
	p.hitl = agentrun.HITLOff
	td := &turnDenials{}
	handler := p.makeTurnPermHandler(td)

	params := mustMarshal(t, requestPermissionParams{
		SessionID: "ses-1",
		ToolCall:  toolCallUpdate{Title: toolBash},
		Options: []permissionOpt{
			{OptionID: "a1", Kind: "allow_once"},
		},
	})

	result, err := handler(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	permResult := result.(requestPermissionResult)
	if permResult.Outcome.Outcome != outcomeSelected {
		t.Errorf("outcome = %q, want %q (auto-approve)", permResult.Outcome.Outcome, outcomeSelected)
	}
	if permResult.Outcome.OptionID != "a1" {
		t.Errorf("optionID = %q, want a1", permResult.Outcome.OptionID)
	}

	denials := td.seal()
	if denials != nil {
		t.Errorf("no denials expected with HITLOff, got %+v", denials)
	}
}

// --- D7: Cancelled permission flows are NOT denials ---

func TestMakeTurnPermHandler_UnmarshalError_NoDenial(t *testing.T) {
	p := newTestProcess(t)
	td := &turnDenials{}
	handler := p.makeTurnPermHandler(td)

	// Invalid JSON.
	result, err := handler(json.RawMessage(`{invalid`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	permResult := result.(requestPermissionResult)
	if permResult.Outcome.Outcome != outcomeCancelled {
		t.Errorf("outcome = %q, want %q (D7: infra error)", permResult.Outcome.Outcome, outcomeCancelled)
	}

	// Should emit MessageError, NOT a denial.
	msg := receiveMessage(t, p)
	if msg.Type != agentrun.MessageError {
		t.Errorf("msg.Type = %q, want %q", msg.Type, agentrun.MessageError)
	}

	denials := td.seal()
	if denials != nil {
		t.Errorf("unmarshal errors should NOT produce denials, got %+v", denials)
	}
}

func TestMakeTurnPermHandler_HandlerPanic_NoDenial(t *testing.T) {
	p := newTestProcess(t)
	p.opts = EngineOptions{
		PermissionHandler: func(_ context.Context, _ PermissionRequest) (bool, error) {
			panic("handler exploded")
		},
		PermissionTimeout: 5 * time.Second,
	}
	td := &turnDenials{}
	handler := p.makeTurnPermHandler(td)

	params := mustMarshal(t, requestPermissionParams{
		SessionID: "ses-1",
		ToolCall:  toolCallUpdate{Title: toolBash},
		Options:   []permissionOpt{{OptionID: "r1", Kind: "reject_once"}},
	})

	result, err := handler(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	permResult := result.(requestPermissionResult)
	if permResult.Outcome.Outcome != outcomeCancelled {
		t.Errorf("outcome = %q, want %q (D7: handler panic)", permResult.Outcome.Outcome, outcomeCancelled)
	}

	msg := receiveMessage(t, p)
	if msg.Type != agentrun.MessageError {
		t.Errorf("msg.Type = %q, want %q", msg.Type, agentrun.MessageError)
	}

	denials := td.seal()
	if denials != nil {
		t.Errorf("handler panics should NOT produce denials, got %+v", denials)
	}
}

// --- handlePromptResult with denials ---

func TestHandlePromptResult_WithDenials(t *testing.T) {
	p := newTestProcess(t)
	td := &turnDenials{}
	td.add(toolBash, "denied by handler")
	td.add(toolWrite, "no permission handler")

	result := &promptResult{StopReason: "end_turn"}
	if err := p.handlePromptResult(nil, result, td); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}

	msg := receiveMessage(t, p)
	if msg.Type != agentrun.MessageResult {
		t.Errorf("Type = %q, want %q", msg.Type, agentrun.MessageResult)
	}
	if len(msg.Denials) != 2 {
		t.Fatalf("Denials len = %d, want 2", len(msg.Denials))
	}
	if msg.Denials[0].Tool != toolBash {
		t.Errorf("Denials[0].Tool = %q, want %q", msg.Denials[0].Tool, toolBash)
	}
	if msg.Denials[1].Tool != toolWrite {
		t.Errorf("Denials[1].Tool = %q, want %q", msg.Denials[1].Tool, toolWrite)
	}
}

func TestHandlePromptResult_NoDenials(t *testing.T) {
	p := newTestProcess(t)
	td := &turnDenials{}

	result := &promptResult{StopReason: "end_turn"}
	if err := p.handlePromptResult(nil, result, td); err != nil {
		t.Fatalf("handlePromptResult: %v", err)
	}

	msg := receiveMessage(t, p)
	if msg.Denials != nil {
		t.Errorf("Denials should be nil when empty, got %+v", msg.Denials)
	}
}

func TestHandlePromptResult_ErrorDiscardsDenials(t *testing.T) {
	p := newTestProcess(t)
	td := &turnDenials{}
	td.add(toolBash, "denied")

	err := p.handlePromptResult(context.DeadlineExceeded, &promptResult{}, td)
	if err == nil {
		t.Fatal("expected error")
	}

	// Collector should be sealed (denials discarded).
	denials := td.seal()
	if denials != nil {
		t.Errorf("denials should be discarded on error, got %+v", denials)
	}
}

// --- Delegating wrapper tests ---

func TestPermHandlerDelegation_NoHandlerLoaded(t *testing.T) {
	p := newTestProcess(t)
	// permHandler is zero — Load() returns nil.
	// The delegating wrapper (in wireReadLoop) returns cancelledPermission().
	h := p.permHandler.Load()
	if h != nil {
		t.Fatal("expected nil handler initially")
	}
}

func TestPermHandlerDelegation_HandlerSwap(t *testing.T) {
	p := newTestProcess(t)
	p.opts = EngineOptions{PermissionTimeout: 5 * time.Second}

	// Install a turn handler.
	td := &turnDenials{}
	handler := p.makeTurnPermHandler(td)
	p.permHandler.Store(&handler)

	// Verify it's loaded.
	h := p.permHandler.Load()
	if h == nil {
		t.Fatal("expected non-nil handler after Store")
	}

	// Swap to deny-all.
	denyAll := denyAllPermHandler
	p.permHandler.Store(&denyAll)

	// Verify deny-all is active.
	result, err := (*p.permHandler.Load())(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	permResult := result.(requestPermissionResult)
	if permResult.Outcome.Outcome != outcomeCancelled {
		t.Errorf("deny-all should return %q, got %q", outcomeCancelled, permResult.Outcome.Outcome)
	}

	// Original collector should have no denials (deny-all doesn't record).
	denials := td.seal()
	if denials != nil {
		t.Errorf("deny-all should not record denials, got %+v", denials)
	}
}

func TestPermHandlerDelegation_SealedCollectorIgnoresLateAdd(t *testing.T) {
	// Simulates: handler is mid-execution when turn boundary crosses.
	// The handler holds a reference to the old collector, which is now sealed.
	td := &turnDenials{}
	td.add("Early", "before seal")
	denials := td.seal()
	if len(denials) != 1 {
		t.Fatalf("pre-seal len = %d, want 1", len(denials))
	}

	// Late add after seal — should be no-op.
	td.add("Late", "after seal")
	laterDenials := td.seal()
	if laterDenials != nil {
		t.Errorf("post-seal add should be ignored, got %+v", laterDenials)
	}
}

// --- helpers ---

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
