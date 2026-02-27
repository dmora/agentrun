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
		if msg.ResumeID != "" {
			fmt.Printf("[init]    session ID: %s\n", msg.ResumeID)
		} else {
			fmt.Println("[init]    (session started)")
		}
	case agentrun.MessageThinking:
		fmt.Printf("[think]   %s\n", msg.Content)
	case agentrun.MessageText:
		fmt.Printf("[text]    %s\n", msg.Content)
	case agentrun.MessageResult:
		fmt.Printf("[result]  %s\n", msg.Content)
		printResultDetails(msg)
	case agentrun.MessageError:
		printError(msg)
	case agentrun.MessageToolResult:
		fmt.Printf("[tool]    %s\n", msg.Tool.Name)
	case agentrun.MessageContextWindow:
		printContextWindow(msg)
	case agentrun.MessageSystem, agentrun.MessageEOF:
		// silent — system status and EOF are infrastructure signals
	default:
		fmt.Printf("[%s]  %s\n", msg.Type, msg.Content)
	}
}

// printContextWindow prints context window fill state.
func printContextWindow(msg agentrun.Message) {
	if msg.Usage == nil {
		return
	}
	u := msg.Usage
	fmt.Printf("[context]  %d/%d tokens", u.ContextUsedTokens, u.ContextSizeTokens)
	if u.ContextSizeTokens > 0 {
		pct := float64(u.ContextUsedTokens) / float64(u.ContextSizeTokens) * 100
		fmt.Printf(" (%.0f%%)", pct)
	}
	fmt.Println()
}

// printResultDetails prints StopReason and Usage details for result messages.
func printResultDetails(msg agentrun.Message) {
	if msg.StopReason != "" {
		fmt.Printf("          stop_reason: %s\n", msg.StopReason)
	}
	if msg.Usage == nil {
		return
	}
	u := msg.Usage
	fmt.Printf("          usage: in=%d out=%d", u.InputTokens, u.OutputTokens)
	if u.CacheReadTokens > 0 {
		fmt.Printf(" cache_read=%d", u.CacheReadTokens)
	}
	if u.CostUSD > 0 {
		fmt.Printf(" cost=$%.4f", u.CostUSD)
	}
	fmt.Println()
}

// printError prints an error message, including ErrorCode when present.
func printError(msg agentrun.Message) {
	if msg.ErrorCode != "" {
		fmt.Fprintf(os.Stderr, "[error]   [%s] %s\n", msg.ErrorCode, msg.Content)
	} else {
		fmt.Fprintf(os.Stderr, "[error]   %s\n", msg.Content)
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
	fmt.Printf("[%s] %-18s %s\n", ts, msg.Type, content)
	printRawMetadata(msg)
}

// printRawMetadata prints ErrorCode, StopReason, Usage, and Raw when present.
func printRawMetadata(msg agentrun.Message) {
	if msg.ErrorCode != "" {
		fmt.Printf("           error_code: %s\n", msg.ErrorCode)
	}
	if msg.StopReason != "" {
		fmt.Printf("           stop_reason: %s\n", msg.StopReason)
	}
	if msg.Usage != nil {
		u := msg.Usage
		fmt.Printf("           usage: in=%d out=%d", u.InputTokens, u.OutputTokens)
		if u.CacheReadTokens > 0 {
			fmt.Printf(" cache_read=%d", u.CacheReadTokens)
		}
		if u.CostUSD > 0 {
			fmt.Printf(" cost=$%.4f", u.CostUSD)
		}
		if u.ContextSizeTokens > 0 {
			fmt.Printf(" ctx=%d/%d", u.ContextUsedTokens, u.ContextSizeTokens)
		}
		fmt.Println()
	}
	if len(msg.Raw) > 0 {
		raw := string(msg.Raw)
		if len(raw) > 200 {
			raw = raw[:200] + "..."
		}
		fmt.Printf("           raw: %s\n", raw)
	}
}
