//go:build ignore

// Command mock-replay simulates the Claude CLI by replaying a captured
// stream-json transcript. The transcript path is read from the MOCK_FIXTURE
// environment variable. Like real Claude in --input-format stream-json mode it
// waits for one stdin line before emitting, then writes every transcript line
// to stdout and exits (stdout close signals the reader to stop).
//
// Used by subagent tracking integration tests to drive the real cli engine +
// claude parser + tracker over decisive probe transcripts.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
)

func main() {
	path := os.Getenv("MOCK_FIXTURE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "mock-replay: MOCK_FIXTURE not set")
		os.Exit(1)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mock-replay: read fixture: %v\n", err)
		os.Exit(1)
	}

	// Wait for the first stdin message, like real Claude in stream-json mode.
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 1<<24)
	if !in.Scan() {
		fmt.Fprintln(os.Stderr, "mock-replay: no input received")
		os.Exit(1)
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			fmt.Fprintln(out, line)
		}
	}
}
