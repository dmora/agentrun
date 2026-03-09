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
		opts:    EngineOptions{MaxLineSize: 0},
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
	// Mid-turn messages carry per-call fill.
	wantCall0 := 5000 + 3000 + 200
	if msgs[0].Usage.ContextUsedTokens != wantCall0 {
		t.Errorf("msg[0] ContextUsedTokens = %d, want %d", msgs[0].Usage.ContextUsedTokens, wantCall0)
	}
	wantCall1 := 7000 + 4000 + 100
	if msgs[1].Usage.ContextUsedTokens != wantCall1 {
		t.Errorf("msg[1] ContextUsedTokens = %d, want %d", msgs[1].Usage.ContextUsedTokens, wantCall1)
	}
	// Result: peak across calls = call 2's fill.
	if result.Usage.ContextUsedTokens != wantCall1 {
		t.Errorf("result ContextUsedTokens = %d, want %d", result.Usage.ContextUsedTokens, wantCall1)
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
	want := 5000 + 3000
	if result.Usage.ContextUsedTokens != want {
		t.Errorf("ContextUsedTokens = %d, want %d", result.Usage.ContextUsedTokens, want)
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
	want := 9000 + 2000 + 500
	if result.Usage.ContextUsedTokens != want {
		t.Errorf("ContextUsedTokens = %d, want %d", result.Usage.ContextUsedTokens, want)
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
	// Turn 1 mid-turn: per-call fill = 7000+4000 = 11000
	wantTurn1 := 7000 + 4000
	if msgs[0].Usage.ContextUsedTokens != wantTurn1 {
		t.Errorf("turn 1 mid-turn ContextUsedTokens = %d, want %d", msgs[0].Usage.ContextUsedTokens, wantTurn1)
	}
	if results[0].Usage.ContextUsedTokens != wantTurn1 {
		t.Errorf("turn 1 result ContextUsedTokens = %d, want %d", results[0].Usage.ContextUsedTokens, wantTurn1)
	}
	// Turn 2 mid-turn: per-call fill = 8000+1000 = 9000 (not stale 11000)
	wantTurn2 := 8000 + 1000
	if msgs[2].Usage.ContextUsedTokens != wantTurn2 {
		t.Errorf("turn 2 mid-turn ContextUsedTokens = %d, want %d", msgs[2].Usage.ContextUsedTokens, wantTurn2)
	}
	if results[1].Usage.ContextUsedTokens != wantTurn2 {
		t.Errorf("turn 2 result ContextUsedTokens = %d, want %d", results[1].Usage.ContextUsedTokens, wantTurn2)
	}
}

func TestApplyContextFill_NoPerCallUsage(t *testing.T) {
	// No per-call assistant messages with Usage → ContextUsedTokens stays 0
	// ("not reported" per omitempty semantics). The CLI engine does not
	// fabricate a value from result-level totals.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 5000, OutputTokens: 1000, CacheReadTokens: 500}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != 0 {
		t.Errorf("ContextUsedTokens = %d, want 0 (not reported without per-call data)", result.Usage.ContextUsedTokens)
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
	// Aborted turn's mid-turn message still gets per-call fill.
	wantAborted := 10000 + 5000
	if msgs[0].Usage.ContextUsedTokens != wantAborted {
		t.Errorf("aborted turn msg[0] ContextUsedTokens = %d, want %d", msgs[0].Usage.ContextUsedTokens, wantAborted)
	}
	// Turn 2 result uses turn 2's fill, not stale turn 1 value.
	wantTurn2 := 3000 + 1000
	if result.Usage.ContextUsedTokens != wantTurn2 {
		t.Errorf("ContextUsedTokens = %d, want %d (stale leak from aborted turn)", result.Usage.ContextUsedTokens, wantTurn2)
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
	// Per-call max from the first MessageText must survive through the
	// synthetic parse error and apply to the result.
	want := 9000 + 2000
	if result.Usage.ContextUsedTokens != want {
		t.Errorf("ContextUsedTokens = %d, want %d (synthetic error must not reset)", result.Usage.ContextUsedTokens, want)
	}
}

func TestApplyContextFill_BackendProvided(t *testing.T) {
	// Result already has ContextUsedTokens → no override.
	const backendProvided = 99999
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 5000, CacheReadTokens: 3000}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 5000, OutputTokens: 1000, ContextUsedTokens: backendProvided}},
	})
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != backendProvided {
		t.Errorf("ContextUsedTokens = %d, want %d (backend-provided, no override)", result.Usage.ContextUsedTokens, backendProvided)
	}
}

func TestApplyContextFill_MidTurnDelta(t *testing.T) {
	// Delta messages don't carry Usage in practice (parsers don't set it),
	// but if one did, applyContextFill treats it like any non-result message.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageTextDelta, Usage: &agentrun.Usage{InputTokens: 4000, CacheReadTokens: 2000, CacheWriteTokens: 100}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 4000, OutputTokens: 500}},
	})
	want := 4000 + 2000 + 100
	if msgs[0].Usage.ContextUsedTokens != want {
		t.Errorf("delta ContextUsedTokens = %d, want %d", msgs[0].Usage.ContextUsedTokens, want)
	}
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != want {
		t.Errorf("result ContextUsedTokens = %d, want %d", result.Usage.ContextUsedTokens, want)
	}
}

func TestApplyContextFill_MidTurnMessages(t *testing.T) {
	// Two mid-turn messages with different usage. First has higher fill than
	// second, proving mid-turn gets per-call fill (not running peak).
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 7000, CacheReadTokens: 4000, CacheWriteTokens: 100}},
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 5000, CacheReadTokens: 3000, CacheWriteTokens: 200}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 12000, OutputTokens: 2000}},
	})
	wantMsg0 := 7000 + 4000 + 100
	if msgs[0].Usage.ContextUsedTokens != wantMsg0 {
		t.Errorf("msg[0] ContextUsedTokens = %d, want %d", msgs[0].Usage.ContextUsedTokens, wantMsg0)
	}
	// msg[1] gets its own per-call fill, NOT the peak-so-far from msg[0].
	wantMsg1 := 5000 + 3000 + 200
	if msgs[1].Usage.ContextUsedTokens != wantMsg1 {
		t.Errorf("msg[1] ContextUsedTokens = %d, want %d (per-call, not peak)", msgs[1].Usage.ContextUsedTokens, wantMsg1)
	}
	// Result: peak across both calls.
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != wantMsg0 {
		t.Errorf("result ContextUsedTokens = %d, want %d (peak)", result.Usage.ContextUsedTokens, wantMsg0)
	}
}

func TestApplyContextFill_MidTurnBackendProvided(t *testing.T) {
	// Mid-turn message already has ContextUsedTokens → no override.
	const backendProvided = 99999
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{InputTokens: 5000, CacheReadTokens: 3000, ContextUsedTokens: backendProvided}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 5000, OutputTokens: 1000}},
	})
	// Mid-turn: backend-provided value preserved.
	if msgs[0].Usage.ContextUsedTokens != backendProvided {
		t.Errorf("mid-turn ContextUsedTokens = %d, want %d (backend-provided)", msgs[0].Usage.ContextUsedTokens, backendProvided)
	}
	// Result: computed fill, not the backend-provided mid-turn value.
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	wantResult := 5000 + 3000
	if result.Usage.ContextUsedTokens != wantResult {
		t.Errorf("result ContextUsedTokens = %d, want %d (computed fill)", result.Usage.ContextUsedTokens, wantResult)
	}
}

func TestApplyContextFill_MidTurnThinking(t *testing.T) {
	// MessageThinking with usage gets per-call fill, same as MessageText.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageThinking, Usage: &agentrun.Usage{InputTokens: 9000, CacheReadTokens: 2000, CacheWriteTokens: 500}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{InputTokens: 9000, OutputTokens: 500}},
	})
	want := 9000 + 2000 + 500
	if msgs[0].Usage.ContextUsedTokens != want {
		t.Errorf("thinking ContextUsedTokens = %d, want %d", msgs[0].Usage.ContextUsedTokens, want)
	}
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != want {
		t.Errorf("result ContextUsedTokens = %d, want %d", result.Usage.ContextUsedTokens, want)
	}
}

func TestApplyContextFill_MidTurnZeroFill(t *testing.T) {
	// Mid-turn message with Usage where all input-side fields are zero.
	// Computed fill is 0, so ContextUsedTokens stays 0 (== 0 guard is a no-op
	// because the value being written is also 0). Result also stays 0.
	msgs := runScanLines(t, []agentrun.Message{
		{Type: agentrun.MessageText, Usage: &agentrun.Usage{OutputTokens: 500}},
		{Type: agentrun.MessageResult, Usage: &agentrun.Usage{OutputTokens: 500}},
	})
	if msgs[0].Usage.ContextUsedTokens != 0 {
		t.Errorf("mid-turn ContextUsedTokens = %d, want 0 (zero fill)", msgs[0].Usage.ContextUsedTokens)
	}
	result := findResult(msgs)
	if result == nil {
		t.Fatal("no MessageResult found")
	}
	if result.Usage.ContextUsedTokens != 0 {
		t.Errorf("result ContextUsedTokens = %d, want 0 (zero fill)", result.Usage.ContextUsedTokens)
	}
}
