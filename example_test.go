package agentrun_test

import (
	"fmt"
	"time"

	"github.com/dmora/agentrun"
)

func ExampleResolveOptions() {
	opts := agentrun.ResolveOptions(
		agentrun.WithPrompt("Hello, agent"),
		agentrun.WithModel("claude-sonnet-4-5-20250514"),
		agentrun.WithTimeout(30*time.Second),
	)
	fmt.Println(opts.Prompt)
	fmt.Println(opts.Model)
	fmt.Println(opts.Timeout)
	// Output:
	// Hello, agent
	// claude-sonnet-4-5-20250514
	// 30s
}

func ExampleResolveOptions_empty() {
	opts := agentrun.ResolveOptions()
	fmt.Println(opts.Prompt == "")
	fmt.Println(opts.Model == "")
	fmt.Println(opts.Timeout)
	// Output:
	// true
	// true
	// 0s
}

func ExampleWithPrompt() {
	opts := agentrun.ResolveOptions(agentrun.WithPrompt("Summarize this code"))
	fmt.Println(opts.Prompt)
	// Output: Summarize this code
}

func ExampleWithModel() {
	opts := agentrun.ResolveOptions(agentrun.WithModel("claude-sonnet-4-5-20250514"))
	fmt.Println(opts.Model)
	// Output: claude-sonnet-4-5-20250514
}

func ExampleWithTimeout() {
	opts := agentrun.ResolveOptions(agentrun.WithTimeout(10 * time.Second))
	fmt.Println(opts.Timeout)
	// Output: 10s
}
