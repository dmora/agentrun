// Package cli provides a CLI subprocess transport adapter for agentrun engines.
//
// A Backend implements [Spawner] and [Parser] to define how subprocesses are
// launched and how their stdout is parsed into [agentrun.Message] values.
// Optional capabilities ([Resumer], [Streamer], [InputFormatter]) are discovered
// via type assertion at runtime.
//
// [NewEngine] wraps a Backend into an [agentrun.Engine]. The returned [Engine]
// manages subprocess lifecycle, message pumping, graceful shutdown (SIGTERM then
// SIGKILL), and the Resumer subprocess-replacement pattern for multi-turn sessions.
//
// # Platform Support
//
// The [Engine] and process types use Unix signals (SIGTERM, SIGKILL) for
// subprocess lifecycle management and are not available on Windows. The interface
// types ([Backend], [Spawner], [Parser], [Resumer], [Streamer], [InputFormatter])
// and option types are available on all platforms.
//
// # Consumer Obligations
//
// Callers must either drain the [agentrun.Process.Output] channel to completion
// or call [agentrun.Process.Stop] to release subprocess resources. Failing to
// do so may leave the subprocess running and leak goroutines.
//
// Concrete backends (claude, opencode) implement the Backend interface.
package cli
