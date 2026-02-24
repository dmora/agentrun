// Package opencode provides an OpenCode CLI backend for agentrun.
//
// This backend implements the cli.Spawner and cli.Parser interfaces to drive
// OpenCode as a subprocess, translating its output into agentrun.Message values.
//
// Note: backends must implement at least one send path (Streamer+InputFormatter
// or Resumer) for cli.Engine.Start to succeed. See agentrun.ErrSendNotSupported.
package opencode
