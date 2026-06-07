package agy

import (
	"regexp"
	"strings"
	"time"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// resultSentinel is the line the shell wrapper prints on a clean turn, mapped to
// MessageResult. Must stay in sync with the printf in shellWrapper.
const resultSentinel = `{"type":"result","stop_reason":"end_turn"}`

// agySessionSentinel matches the conversation-ID line the shell wrapper emits
// after a new conversation is created, e.g.
//
//	{"type":"agy_session","id":"d8e79181-5db2-4ea9-88e2-eea15ddab587"}
var agySessionSentinel = regexp.MustCompile(`^\{"type":"agy_session","id":"([0-9a-fA-F-]{36})"\}$`)

// ParseLine implements cli.Parser for the agy backend. It maps plain-text lines
// to agentrun.MessageText, recognizes the synthesized MessageResult sentinel, and
// captures the conversation ID (write-once) from the agy_session sentinel.
func (b *Backend) ParseLine(line string) (agentrun.Message, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return agentrun.Message{}, cli.ErrSkipLine
	}

	if trimmed == resultSentinel {
		return agentrun.Message{
			Type:       agentrun.MessageResult,
			StopReason: agentrun.StopEndTurn,
			Timestamp:  time.Now(),
		}, nil
	}

	// Capture the conversation ID emitted by the wrapper; it is engine plumbing,
	// not agent output, so it is not surfaced to the consumer.
	if m := agySessionSentinel.FindStringSubmatch(trimmed); m != nil {
		id := m[1]
		b.resumeID.CompareAndSwap(nil, &id) // write-once
		return agentrun.Message{}, cli.ErrSkipLine
	}

	return agentrun.Message{
		Type:      agentrun.MessageText,
		Content:   line,
		Timestamp: time.Now(),
	}, nil
}
