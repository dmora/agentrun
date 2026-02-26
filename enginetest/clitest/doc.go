// Package clitest provides compliance test suites for [cli.Backend] implementations.
//
// Test authors call [RunBackendTests] with a factory function that returns the
// implementation under test. The suite discovers optional capabilities
// ([cli.Resumer], [cli.Streamer], [cli.InputFormatter]) via type assertion.
//
// Example usage in a backend test file:
//
//	package mybackend_test
//
//	import (
//	    "testing"
//	    "github.com/dmora/agentrun/engine/cli"
//	    "github.com/dmora/agentrun/engine/cli/mybackend"
//	    "github.com/dmora/agentrun/enginetest/clitest"
//	)
//
//	func TestCompliance(t *testing.T) {
//	    clitest.RunBackendTests(t, func() cli.Backend {
//	        return mybackend.New()
//	    })
//	}
package clitest
