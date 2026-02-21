package cli_test

import (
	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

// Compile-time interface satisfaction checks.
// These fail the build if any signature drifts.

type stubSpawner struct{}

func (stubSpawner) SpawnArgs(_ agentrun.Session) (string, []string) { return "", nil }

var _ cli.Spawner = stubSpawner{}

type stubParser struct{}

func (stubParser) ParseLine(_ string) (agentrun.Message, error) { return agentrun.Message{}, nil }

var _ cli.Parser = stubParser{}

type stubResumer struct{}

func (stubResumer) ResumeArgs(_ agentrun.Session, _ string) (string, []string, error) {
	return "", nil, nil
}

var _ cli.Resumer = stubResumer{}

type stubStreamer struct{}

func (stubStreamer) StreamArgs(_ agentrun.Session) (string, []string) { return "", nil }

var _ cli.Streamer = stubStreamer{}

type stubInputFormatter struct{}

func (stubInputFormatter) FormatInput(_ string) ([]byte, error) { return nil, nil }

var _ cli.InputFormatter = stubInputFormatter{}
