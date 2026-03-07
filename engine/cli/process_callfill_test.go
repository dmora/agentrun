package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/dmora/agentrun"
)

// callFillBackend is a minimal Backend for callfill integration tests.
// parseFn maps each line to a Message (including Usage and Type).
type callFillBackend struct {
	parseFn func(string) (agentrun.Message, error)
}

func (b *callFillBackend) SpawnArgs(_ agentrun.Session) (string, []string) { return "", nil }
func (b *callFillBackend) ParseLine(line string) (agentrun.Message, error) {
	return b.parseFn(line)
}

// runScanLinesWithParser creates a minimal process, feeds lineCount lines
// via a pipe, and collects all emitted messages. The provided parseFn is
// called for each line, allowing tests to simulate ParseLine errors.
func runScanLinesWithParser(t *testing.T, lineCount int, parseFn func(string) (agentrun.Message, error)) []agentrun.Message {
	t.Helper()

	backend := &callFillBackend{parseFn: parseFn}

	r, w := io.Pipe()
	go func() {
		for range lineCount {
			fmt.Fprintln(w, "x")
		}
		w.Close()
	}()

	p := &process{
		backend: backend,
		opts:    EngineOptions{ScannerBuffer: 64 * 1024},
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
	out := make([]agentrun.Message, 0, lineCount)
	for m := range p.output {
		out = append(out, m)
	}
	return out
}

// runScanLines is a convenience wrapper for tests where all lines parse
// successfully. Each message is returned in order with a nil error.
func runScanLines(t *testing.T, messages []agentrun.Message) []agentrun.Message {
	t.Helper()
	idx := 0
	return runScanLinesWithParser(t, len(messages), func(_ string) (agentrun.Message, error) {
		m := messages[idx]
		idx++
		return m, nil
	})
}

func findResult(msgs []agentrun.Message) *agentrun.Message {
	for i := range msgs {
		if msgs[i].Type == agentrun.MessageResult {
			return &msgs[i]
		}
	}
	return nil
}

func findResults(msgs []agentrun.Message) []*agentrun.Message {
	var results []*agentrun.Message
	for i := range msgs {
		if msgs[i].Type == agentrun.MessageResult {
			results = append(results, &msgs[i])
		}
	}
	return results
}

func TestApplyContextFill_MultiCallTurn(t *testing.T) {
	// Two assistant messages with different InputTokens → ContextUsedTokens = max.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 5000, CacheReadTokens: 3000, CacheWriteTokens: 200}},
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 7000, CacheReadTokens: 4000, CacheWriteTokens: 100}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 12000, OutputTokens: 2000, CacheReadTokens: 7000}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	// max fill: call 2 → 7000+4000+100 = 11100
	if result.Usage.ContextUsedTokens != 11100 {
		t.Errorf("ContextUsedTokens = %d, want 11100", result.Usage.ContextUsedTokens)
	}
}

func TestApplyContextFill_SingleCallTurn(t *testing.T) {
	// One assistant message → ContextUsedTokens = that call's fill.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 5000, CacheReadTokens: 3000}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 5000, OutputTokens: 1000, CacheReadTokens: 3000}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != 8000 {
		t.Errorf("ContextUsedTokens = %d, want 8000", result.Usage.ContextUsedTokens)
	}
}

func TestApplyContextFill_ThinkingMessage(t *testing.T) {
	// MessageThinking with Usage tracked just like MessageText.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageThinking, Usage: &agentrun.Usage{InputTokens: 9000, CacheReadTokens: 2000, CacheWriteTokens: 500}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 9000, OutputTokens: 500, CacheReadTokens: 2000, CacheWriteTokens: 500}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	// fill: 9000+2000+500 = 11500
	if result.Usage.ContextUsedTokens != 11500 {
		t.Errorf("ContextUsedTokens = %d, want 11500", result.Usage.ContextUsedTokens)
	}
}

func TestApplyContextFill_TwoTurnsReset(t *testing.T) {
	// Two turns through the same scanLines (streamer mode).
	// Turn 1: maxCallFill=11000, turn 2: maxCallFill=9000.
	// Turn 2 must use 9000, not stale 11000.
	msgs := runScanLines(t, []agentrun.Message{
		// Turn 1
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 7000, CacheReadTokens: 4000}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 7000, OutputTokens: 1000, CacheReadTokens: 4000}},
		// Turn 2
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 8000, CacheReadTokens: 1000}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 8000, OutputTokens: 500, CacheReadTokens: 1000}},
	})
	results := findResults(msgs)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	// Turn 1: fill = 7000+4000 = 11000
	if results[0].Usage.ContextUsedTokens != 11000 {
		t.Errorf("turn 1 ContextUsedTokens = %d, want 11000", results[0].Usage.ContextUsedTokens)
	}
	// Turn 2: fill = 8000+1000 = 9000 (not stale 11000)
	if results[1].Usage.ContextUsedTokens != 9000 {
		t.Errorf("turn 2 ContextUsedTokens = %d, want 9000", results[1].Usage.ContextUsedTokens)
	}
}

func TestApplyContextFill_NoPerCallUsage(t *testing.T) {
	// No assistant messages with Usage → falls back to enrichContextUsed sum.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 5000, OutputTokens: 1000, CacheReadTokens: 500}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	// enrichContextUsed sums all fields: 5000+1000+500 = 6500
	if result.Usage.ContextUsedTokens != 6500 {
		t.Errorf("ContextUsedTokens = %d, want 6500 (enrichContextUsed fallback)", result.Usage.ContextUsedTokens)
	}
}

func TestApplyContextFill_ErrorResetsAccumulator(t *testing.T) {
	// Turn 1: assistant text with high usage, then error (no result).
	// Turn 2: assistant text with lower usage, then result.
	// Turn 2's result must use turn 2's fill, not stale turn 1 value.
	msgs := runScanLines(t, []agentrun.Message{
		// Turn 1 — aborted by error
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 10000, CacheReadTokens: 5000}},
		{Type: agentrun.MessageError, Content: "rate_limit"},
		// Turn 2 — succeeds
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 3000, CacheReadTokens: 1000}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 3000, OutputTokens: 500, CacheReadTokens: 1000}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	// fill: 3000+1000 = 4000 (not stale 15000 from turn 1)
	if result.Usage.ContextUsedTokens != 4000 {
		t.Errorf("ContextUsedTokens = %d, want 4000 (stale leak from aborted turn)", result.Usage.ContextUsedTokens)
	}
}

func TestApplyContextFill_SyntheticParseErrorPreservesMax(t *testing.T) {
	// A recoverable ParseLine error mid-turn synthesizes MessageError in
	// scanLines. This must NOT reset maxCallFill — the earlier per-call
	// usage should survive through the final result.
	type lineSpec struct {
		msg agentrun.Message
		err error
	}
	specs := []lineSpec{
		{msg: agentrun.Message{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 9000, CacheReadTokens: 2000}}},
		{err: errors.New("malformed JSON")}, // synthetic error — recoverable
		{msg: agentrun.Message{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 9000, OutputTokens: 500, CacheReadTokens: 2000}}},
	}
	idx := 0
	msgs := runScanLinesWithParser(t, len(specs), func(_ string) (agentrun.Message, error) {
		s := specs[idx]
		idx++
		return s.msg, s.err
	})

	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	// Per-call max from the first MessageText (9000+2000 = 11000) must survive
	// through the synthetic parse error and apply to the result.
	if result.Usage.ContextUsedTokens != 11000 {
		t.Errorf("ContextUsedTokens = %d, want 11000 (synthetic error must not reset)", result.Usage.ContextUsedTokens)
	}
}

func TestApplyContextFill_BackendProvided(t *testing.T) {
	// Result already has ContextUsedTokens → no override.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 5000, CacheReadTokens: 3000}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 5000, OutputTokens: 1000, ContextUsedTokens: 99999}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != 99999 {
		t.Errorf("ContextUsedTokens = %d, want 99999 (backend-provided, no override)", result.Usage.ContextUsedTokens)
	}
}
