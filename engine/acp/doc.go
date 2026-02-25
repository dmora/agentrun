// Package acp provides an Agent Client Protocol (ACP) engine for agentrun.
//
// Unlike CLI backends that parse per-tool stdout formats, ACP uses JSON-RPC 2.0
// over stdin/stdout with a persistent subprocess. The subprocess stays alive
// across turns — MCP servers boot once, subsequent turns are instant.
//
// This implementation targets ACP spec v0.10.8 (protocol version 1).
//
// ACP is a standardized protocol supported by OpenCode, Goose, OpenHands, and
// other agent runtimes. One engine handles all ACP-speaking agents, parameterized
// by binary name.
//
//	engine := acp.NewEngine(acp.WithBinary("opencode"), acp.WithArgs("acp"))
//	proc, err := engine.Start(ctx, session)
package acp
