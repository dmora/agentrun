// Package enginetest provides compliance test suites for agentrun Engine and
// Backend implementations.
//
// Test authors call the exported Run*Tests functions (e.g., RunSpawnerTests,
// RunParserTests) with a factory function that returns the implementation under
// test. This ensures all backends satisfy the same behavioral contract.
//
// Example usage in a backend test file:
//
//	func TestClaudeBackend(t *testing.T) {
//	    enginetest.RunSpawnerTests(t, func() cli.Spawner {
//	        return claude.New()
//	    })
//	}
package enginetest
