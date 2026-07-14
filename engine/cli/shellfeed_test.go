//go:build !windows

package cli

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/dmora/agentrun"
)

// shellFeedCapableBackend is callFillBackend plus ShellFeedBackend, for
// resolveCapabilities tests below. Embedding keeps it a Backend without
// redeclaring SpawnArgs/ParseLine.
type shellFeedCapableBackend struct{ callFillBackend }

func (shellFeedCapableBackend) ShellFeed() {}

func TestResolveCapabilities_ShellFeed(t *testing.T) {
	t.Run("absent_by_default", func(t *testing.T) {
		caps := resolveCapabilities(&callFillBackend{})
		if caps.shellFeed {
			t.Error("shellFeed must be false for a backend not implementing ShellFeedBackend")
		}
	})
	t.Run("present_when_implemented", func(t *testing.T) {
		caps := resolveCapabilities(&shellFeedCapableBackend{})
		if !caps.shellFeed {
			t.Error("shellFeed must be true for a backend implementing ShellFeedBackend")
		}
	})
}

// runScanLinesWithCaps mirrors runScanLinesWithParser (process_callfill_test.go)
// but also accepts caps: the ShellFeedBackend gate lives in the interaction
// between EngineOptions.ShellTracking and capabilities.shellFeed (scanLines),
// which the opts-only helper cannot exercise.
func runScanLinesWithCaps(t *testing.T, opts EngineOptions, caps capabilities, messages []agentrun.Message) []agentrun.Message {
	t.Helper()

	idx := 0
	backend := &callFillBackend{parseFn: func(_ string) (agentrun.Message, error) {
		m := messages[idx]
		idx++
		return m, nil
	}}

	r, w := io.Pipe()
	go func() {
		for range messages {
			fmt.Fprintln(w, "x")
		}
		w.Close()
	}()

	p := &process{
		backend: backend,
		opts:    opts,
		caps:    caps,
		output:  make(chan agentrun.Message, 64),
	}
	done := make(chan error, 1)
	go func() {
		done <- p.scanLines(context.Background(), r)
	}()
	if err := <-done; err != nil {
		t.Fatalf("scanLines error: %v", err)
	}
	close(p.output)
	out := make([]agentrun.Message, 0, len(messages))
	for m := range p.output {
		out = append(out, m)
	}
	return out
}

// TestShellFeedGate_WithShellTrackingInertWithoutCapability pins the ADR-2/
// I10 fix directly: WithShellTracking alone must not seed BackgroundShell on
// a backend that never declares ShellFeedBackend — even though the option
// is accepted (no error), Codex/OpenCode/agy-shaped backends must never see
// a stamped {0,0} for a kind they structurally cannot report.
// WithSubagentTools' fallback path is backend-agnostic and must be
// unaffected by the gate.
func TestShellFeedGate_WithShellTrackingInertWithoutCapability(t *testing.T) {
	opts := EngineOptions{
		SubagentTools: map[string]struct{}{"Agent": {}},
		ShellTracking: true, // consumer opts in...
	}
	caps := capabilities{shellFeed: false} // ...but the backend never declares the capability

	out := runScanLinesWithCaps(t, opts, caps, []agentrun.Message{
		{Type: agentrun.MessageResult},
	})

	res := out[len(out)-1]
	if res.Background == nil {
		t.Fatal("Background must be non-nil: WithSubagentTools alone still enables tracking")
	}
	if tc, ok := res.Background.Kind(agentrun.BackgroundShell); ok {
		t.Errorf("BackgroundShell must be absent (not even {0,0}) without ShellFeedBackend, got %+v", tc)
	}
	if subTC, ok := res.Background.Kind(agentrun.BackgroundSubagent); !ok || subTC.Started != 0 || subTC.Finished != 0 {
		t.Errorf("BackgroundSubagent must still be tracked (fallback works on any backend), got %+v ok=%v", subTC, ok)
	}
}

// TestShellFeedGate_SeedsShellWhenBackendDeclaresCapability is the positive
// counterpart: once the backend declares ShellFeedBackend, WithShellTracking
// takes effect exactly as before — BackgroundShell appears seeded {0,0} at
// the very first stamp (see TestBackgroundTracker_SeededKindsAppearQuiescentBeforeAnyActivity
// for the same contract at the tracker-unit level).
func TestShellFeedGate_SeedsShellWhenBackendDeclaresCapability(t *testing.T) {
	opts := EngineOptions{ShellTracking: true}
	caps := capabilities{shellFeed: true}

	out := runScanLinesWithCaps(t, opts, caps, []agentrun.Message{
		{Type: agentrun.MessageResult},
	})

	res := out[len(out)-1]
	shellTC, ok := res.Background.Kind(agentrun.BackgroundShell)
	if !ok || shellTC.Started != 0 || shellTC.Finished != 0 {
		t.Errorf("BackgroundShell must be seeded {0,0} once the backend declares ShellFeedBackend, got %+v ok=%v", shellTC, ok)
	}
}
