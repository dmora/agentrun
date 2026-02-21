package agentrun

import (
	"encoding/json"
	"testing"
	"time"
)

func FuzzResolveOptions(f *testing.F) {
	f.Add("prompt1", "model1", int64(5))
	f.Add("", "", int64(0))
	f.Add("very long prompt with special chars: !@#$%^&*()", "claude-sonnet-4-5-20250514", int64(-1))

	f.Fuzz(func(t *testing.T, prompt, model string, timeoutNs int64) {
		opts := ResolveOptions(
			WithPrompt(prompt),
			WithModel(model),
			WithTimeout(time.Duration(timeoutNs)),
		)
		if opts.Prompt != prompt {
			t.Errorf("Prompt mismatch: got %q, want %q", opts.Prompt, prompt)
		}
		if opts.Model != model {
			t.Errorf("Model mismatch: got %q, want %q", opts.Model, model)
		}
	})
}

func FuzzMessageJSON(f *testing.F) {
	f.Add([]byte(`{"type":"text","content":"hello","timestamp":"2025-01-15T10:30:00Z"}`))
	f.Add([]byte(`{"type":"eof"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`invalid json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return // invalid JSON is fine, panics are bugs
		}
		// Round-trip: marshal then unmarshal should not panic.
		out, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal failed after successful unmarshal: %v", err)
		}
		var msg2 Message
		if err := json.Unmarshal(out, &msg2); err != nil {
			t.Fatalf("round-trip unmarshal failed: %v", err)
		}
	})
}
