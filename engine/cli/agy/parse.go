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
//
// The id is captured structurally (any non-quote run), not by shape, so a
// differently-shaped id (see agy.go's unbounded sed capture) still consumes
// the line as plumbing instead of falling through to the MessageText branch
// below. isConversationID applies the shape check separately, deciding only
// whether the id is trustworthy enough to surface and auto-store.
var agySessionSentinel = regexp.MustCompile(`^\{"type":"agy_session","id":"([^"]*)"\}$`)

// conversationIDShape matches the canonical id shape the wrapper's "Created
// conversation <id>" capture is expected to produce.
var conversationIDShape = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

// isConversationID reports whether id matches the canonical conversation-id shape.
func isConversationID(id string) bool {
	return conversationIDShape.MatchString(id)
}

// ParseLine implements cli.Parser for the agy backend. It maps plain-text lines
// to agentrun.MessageText, recognizes the synthesized MessageResult sentinel, and
// turns the agy_session sentinel into MessageInit, capturing the conversation ID
// (write-once) when it matches the expected shape.
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

	// The wrapper's conversation-ID sentinel is engine plumbing, not agent
	// output: it always becomes MessageInit, never MessageText, regardless of
	// whether the id is trustworthy enough to store. An empty ResumeID here
	// signals "no ID available" per the OptionResumeID doc convention.
	if m := agySessionSentinel.FindStringSubmatch(trimmed); m != nil {
		msg := agentrun.Message{Type: agentrun.MessageInit, Timestamp: time.Now()}
		if id := m[1]; isConversationID(id) {
			b.resumeID.CompareAndSwap(nil, &id) // write-once
			msg.ResumeID = id
		}
		return msg, nil
	}

	return agentrun.Message{
		Type:      agentrun.MessageText,
		Content:   line,
		Timestamp: time.Now(),
	}, nil
}
