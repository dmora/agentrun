package cli

// This file will define the consumer-side interfaces for CLI backends:
//
//   - Spawner   — builds and starts subprocess commands
//   - Parser    — transforms raw output lines into agentrun.Message values
//   - Resumer   — optional: resumes an existing session (type-assertion capability)
//   - Streamer  — optional: attaches to a running subprocess output stream
//
// Interfaces are defined here (at the consumer side) rather than in backend
// packages, following Go interface ownership conventions. Backend packages
// (claude, opencode) provide concrete implementations.
