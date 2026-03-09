//go:build !windows

package main

import (
	"fmt"
	"io"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/acp"
	"github.com/dmora/agentrun/engine/cli"
)

// cliBackendNames lists CLI backend names (excludes ACP).
var cliBackendNames = []string{backendClaude, backendCodex, backendOpenCode}

// validBackends lists all known backend names for validation and enumeration.
var validBackends = []string{backendClaude, backendCodex, backendOpenCode, backendACP}

// makeEngine creates an engine for the given backend name.
// Returns the engine, whether it uses spawn-per-turn semantics, and any error.
// CLI backends are constructed via newCLIBackend (single source of truth).
// spawnPerTurn is derived from the Streamer capability: streaming backends use
// RunTurn (send+drain), spawn-per-turn backends drain Output() directly.
func makeEngine(backend string, stderrW io.Writer, acpBin string, acpExtraArgs []string) (agentrun.Engine, bool, error) {
	if b, err := newCLIBackend(backend); err == nil {
		_, isStreamer := b.(cli.Streamer)
		return cli.NewEngine(b, cli.WithStderrWriter(stderrW)), !isStreamer, nil
	}
	if backend == backendACP {
		if acpBin == "" {
			return nil, false, fmt.Errorf("ACP binary not configured (set --acp-binary or AGENTRUN_ACP_BINARY)")
		}
		opts := []acp.EngineOption{acp.WithBinary(acpBin), acp.WithStderrWriter(stderrW)}
		if len(acpExtraArgs) > 0 {
			opts = append(opts, acp.WithArgs(acpExtraArgs...))
		}
		return acp.NewEngine(opts...), false, nil
	}
	return nil, false, fmt.Errorf("unknown backend %q (valid: %s, %s, %s, %s)",
		backend, backendClaude, backendCodex, backendOpenCode, backendACP)
}
