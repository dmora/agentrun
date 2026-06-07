package agy_test

import (
	"testing"

	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/agy"
	"github.com/dmora/agentrun/enginetest/clitest"
)

func TestCompliance(t *testing.T) {
	factory := func() cli.Backend {
		return agy.New()
	}

	t.Run("Spawner", func(t *testing.T) {
		clitest.RunSpawnerTests(t, func() cli.Spawner { return factory() })
	})

	// Note: We deliberately do NOT run clitest.RunParserTests here.
	// clitest.RunParserTests assumes that the backend expects JSON and asserts
	// that passing "not json" returns an error. The agy CLI backend is
	// plain-text based and treats any non-JSON line as agentrun.MessageText.
	// We verify its parser logic extensively in parse_test.go instead.

	t.Run("Resumer", func(t *testing.T) {
		clitest.RunResumerTests(t, func() cli.Resumer { return factory().(cli.Resumer) })
	})
}
