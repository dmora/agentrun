//go:build ignore

// Command mock-streaming simulates Claude CLI streaming output for integration
// tests. It reads one line from stdin (like real Claude in --input-format
// stream-json mode), then writes a realistic streaming sequence to stdout
// and exits.
package main

import (
	"bufio"
	"fmt"
	"os"
)

func main() {
	// Wait for first stdin message, like real Claude in stream-json mode.
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		fmt.Fprintln(os.Stderr, "mock-streaming: no input received")
		os.Exit(1)
	}

	// Emit realistic streaming sequence with all 3 delta types:
	// thinking_delta, text_delta, and input_json_delta.
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"mock-session","model":"claude-sonnet-4-5-20250514"}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant"}}}`,
		// Thinking block.
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" think"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"ErUBCkYIAxgCIkD"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		// Text block.
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Hello"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":" world"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":1}}`,
		// Tool use block.
		`{"type":"stream_event","event":{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_1","name":"read_file"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"foo.txt\"}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":2}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"}}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`,
		`{"type":"result","result":"Hello world"}`,
	}

	for _, line := range lines {
		fmt.Println(line)
	}

	// Exit immediately — stdout close signals the reader to stop.
}
