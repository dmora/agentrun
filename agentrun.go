// Package agentrun provides composable interfaces for running AI agent sessions.
//
// agentrun is a zero-dependency Go library that abstracts over different AI agent
// runtimes (CLI subprocesses, API clients) with a uniform [Engine]/[Process] model.
//
// # Core Types
//
//   - [Engine] — starts and validates agent sessions
//   - [Process] — an active session handle with message output channel
//   - [Session] — minimal session state passed to engines (value type)
//   - [Message] — structured output from agent processes
//   - [Option] — functional options for [Engine.Start]
//
// # Quick Start
//
//	engine := cli.NewEngine(claude.New())
//	proc, err := engine.Start(ctx, agentrun.Session{
//	    ID:     "s1",
//	    Prompt: "Hello",
//	})
//	if err != nil { log.Fatal(err) }
//	for msg := range proc.Output() {
//	    fmt.Println(msg.Content)
//	}
package agentrun
