package agy

import (
	"strings"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// ParseLine implements cli.Parser for the agy backend.
// It maps plain text lines to agentrun.MessageText, and detects the
// synthesized MessageResult sentinel emitted by the shell wrapper.
func (b *Backend) ParseLine(line string) (agentrun.Message, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return agentrun.Message{}, cli.ErrSkipLine
	}

	// Detect the exact sentinel emitted by the shell wrapper.
	if trimmed == `{"type":"result","stop_reason":"end_turn"}` {
		return agentrun.Message{
			Type:       agentrun.MessageResult,
			StopReason: agentrun.StopEndTurn,
			Timestamp:  time.Now(),
		}, nil
	}

	return agentrun.Message{
		Type:      agentrun.MessageText,
		Content:   line,
		Timestamp: time.Now(),
	}, nil
}
