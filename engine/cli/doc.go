// Package cli provides a CLI subprocess transport adapter for agentrun engines.
//
// This package defines the consumer-side interfaces (Spawner, Parser, Resumer,
// Streamer) and a generic CLIEngine that orchestrates subprocess lifecycle.
// Concrete backends (claude, opencode) implement these interfaces.
package cli
