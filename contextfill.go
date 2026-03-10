package agentrun

// ContextFill extracts context fill information from a message.
// Returns the fill level (used tokens), capacity (size tokens), and
// whether the message contains context fill data.
//
// Works for all message types and backends. ok is false when no
// context fill data is present.
func ContextFill(msg Message) (used, size int, ok bool) {
	if msg.Usage == nil {
		return 0, 0, false
	}
	if msg.Usage.ContextUsedTokens > 0 || msg.Usage.ContextSizeTokens > 0 {
		return msg.Usage.ContextUsedTokens, msg.Usage.ContextSizeTokens, true
	}
	return 0, 0, false
}
