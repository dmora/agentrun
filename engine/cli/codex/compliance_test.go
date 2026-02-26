package codex_test

import (
	"testing"

	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/codex"
	"github.com/dmora/agentrun/enginetest/clitest"
)

func TestCompliance(t *testing.T) {
	clitest.RunBackendTests(t, func() cli.Backend {
		return codex.New()
	})
}
