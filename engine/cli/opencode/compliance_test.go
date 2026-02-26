package opencode_test

import (
	"testing"

	"github.com/dmora/agentrun/engine/cli"
	"github.com/dmora/agentrun/engine/cli/opencode"
	"github.com/dmora/agentrun/enginetest/clitest"
)

func TestCompliance(t *testing.T) {
	clitest.RunBackendTests(t, func() cli.Backend {
		return opencode.New()
	})
}
