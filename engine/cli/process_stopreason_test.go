package cli

import (
	"testing"

	"github.com/dmora/agentrun"
)

func TestApplyStopReasonCarryForward_Basic(t *testing.T) {
	// message_delta sets StopReason → captured, stripped from message.
	msg := agentrun.Message{
		Type:       agentrun.MessageSystem,
		StopReason: agentrun.StopEndTurn,
	}
	last := applyStopReasonCarryForward(&msg, "")
	if last != agentrun.StopEndTurn {
		t.Errorf("want captured %q, got %q", agentrun.StopEndTurn, last)
	}
	if msg.StopReason != "" {
		t.Errorf("StopReason should be stripped from system message, got %q", msg.StopReason)
	}

	// Next result gets the carried StopReason.
	result := agentrun.Message{Type: agentrun.MessageResult}
	last = applyStopReasonCarryForward(&result, last)
	if result.StopReason != agentrun.StopEndTurn {
		t.Errorf("result should get carried StopReason %q, got %q", agentrun.StopEndTurn, result.StopReason)
	}
	if last != "" {
		t.Errorf("lastStopReason should be cleared after result, got %q", last)
	}
}

func TestApplyStopReasonCarryForward_ClearedAfterUse(t *testing.T) {
	// First turn: message_delta with StopReason, then result.
	msg := agentrun.Message{Type: agentrun.MessageSystem, StopReason: agentrun.StopEndTurn}
	last := applyStopReasonCarryForward(&msg, "")

	result := agentrun.Message{Type: agentrun.MessageResult}
	last = applyStopReasonCarryForward(&result, last)

	// Second turn: result with no preceding StopReason.
	result2 := agentrun.Message{Type: agentrun.MessageResult}
	last = applyStopReasonCarryForward(&result2, last)
	if result2.StopReason != "" {
		t.Errorf("second result should have empty StopReason, got %q", result2.StopReason)
	}
	_ = last
}

func TestApplyStopReasonCarryForward_NoClobber(t *testing.T) {
	// message_delta sets one stop reason.
	msg := agentrun.Message{Type: agentrun.MessageSystem, StopReason: "old_reason"}
	last := applyStopReasonCarryForward(&msg, "")

	// Result has its own stop reason from direct extraction.
	result := agentrun.Message{
		Type:       agentrun.MessageResult,
		StopReason: agentrun.StopMaxTokens,
	}
	last = applyStopReasonCarryForward(&result, last)

	// Result keeps its own StopReason — carry-forward does not overwrite.
	if result.StopReason != agentrun.StopMaxTokens {
		t.Errorf("result should keep own StopReason %q, got %q", agentrun.StopMaxTokens, result.StopReason)
	}
	if last != "" {
		t.Errorf("lastStopReason should be cleared after result, got %q", last)
	}
}

func TestApplyStopReasonCarryForward_InitResetsStale(t *testing.T) {
	// message_delta sets StopReason but no result follows (cancelled turn).
	msg := agentrun.Message{Type: agentrun.MessageSystem, StopReason: agentrun.StopEndTurn}
	last := applyStopReasonCarryForward(&msg, "")

	// MessageInit starts a new turn — clears stale state.
	init := agentrun.Message{Type: agentrun.MessageInit}
	last = applyStopReasonCarryForward(&init, last)
	if last != "" {
		t.Errorf("Init should clear stale lastStopReason, got %q", last)
	}

	// Next result should have empty StopReason.
	result := agentrun.Message{Type: agentrun.MessageResult}
	_ = applyStopReasonCarryForward(&result, last)
	if result.StopReason != "" {
		t.Errorf("result after Init should have empty StopReason, got %q", result.StopReason)
	}
}

func TestApplyStopReasonCarryForward_NoLeakToConsumer(t *testing.T) {
	// System message with StopReason should not leak to consumer.
	msg := agentrun.Message{Type: agentrun.MessageSystem, StopReason: agentrun.StopToolUse}
	_ = applyStopReasonCarryForward(&msg, "")
	if msg.StopReason != "" {
		t.Errorf("consumer should not see StopReason on system message, got %q", msg.StopReason)
	}
}
