// Package agentrun provides composable interfaces for running AI agent sessions.
//
// agentrun is a zero-dependency Go library that abstracts over different AI agent
// runtimes (CLI subprocesses, API clients) with a uniform Engine/Process model.
//
// The primary types defined in this package are:
//
//   - [Engine] — starts and validates agent sessions
//   - [Process] — an active session handle with output channel
//   - [Session] — minimal session state passed to engines
//   - [Message] — structured output from agent processes
//
// Quick start:
//
//	engine := cli.NewEngine(claude.New())
//	proc, err := engine.Start(ctx, agentrun.Session{ID: "s1", Prompt: "Hello"})
//	for msg := range proc.Output() { ... }
package agentrun
