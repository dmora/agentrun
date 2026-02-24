// Package display provides shared message formatting for agentrun examples.
// It lives under examples/internal so it is not importable by external code.
package display

import (
	"fmt"
	"os"
	"time"

	"github.com/dmora/agentrun"
)

// PrintMessage formats and prints a single message to stdout/stderr.
// System and EOF messages are silently ignored.
func PrintMessage(msg agentrun.Message) {
	switch msg.Type {
	case agentrun.MessageInit:
		if msg.Content != "" {
			fmt.Printf("[init]    session ID: %s\n", msg.Content)
		} else {
			fmt.Println("[init]    (session started)")
		}
	case agentrun.MessageThinking:
		fmt.Printf("[think]   %s\n", msg.Content)
	case agentrun.MessageText:
		fmt.Printf("[text]    %s\n", msg.Content)
	case agentrun.MessageResult:
		fmt.Printf("[result]  %s\n", msg.Content)
	case agentrun.MessageError:
		fmt.Fprintf(os.Stderr, "[error]   %s\n", msg.Content)
	case agentrun.MessageToolResult:
		fmt.Printf("[tool]    %s\n", msg.Tool.Name)
	case agentrun.MessageSystem, agentrun.MessageEOF:
		// silent — system status and EOF are infrastructure signals
	default:
		fmt.Printf("[%s]  %s\n", msg.Type, msg.Content)
	}
}

// PrintRaw prints a diagnostic line for every message, showing the parsed
// type, content preview, and raw stdout line. Used by the stream-inspector
// example for protocol debugging.
func PrintRaw(msg agentrun.Message) {
	ts := msg.Timestamp.Format(time.TimeOnly + ".000")
	content := msg.Content
	if len(content) > 120 {
		content = content[:120] + "..."
	}
	raw := msg.RawLine
	if len(raw) > 200 {
		raw = raw[:200] + "..."
	}
	fmt.Printf("[%s] %-18s %s\n", ts, msg.Type, content)
	if raw != "" {
		fmt.Printf("           raw: %s\n", raw)
	}
}
