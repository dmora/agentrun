module github.com/dmora/agentrun/examples

go 1.24

// replace directive is required — this module builds against local source.
// Remove only after the library is published and version-tagged.
replace github.com/dmora/agentrun => ../

require github.com/dmora/agentrun v0.0.0-00010101000000-000000000000
