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

func FuzzEffortValid(f *testing.F) {
	f.Add("low")
	f.Add("medium")
	f.Add("high")
	f.Add("max")
	f.Add("")
	f.Add("LOW")
	f.Add("xhigh")
	f.Add("minimal")
	f.Add("invalid\x00value")

	f.Fuzz(func(_ *testing.T, val string) {
		e := Effort(val)
		_ = e.Valid()
	})
}

func FuzzParseListOption(f *testing.F) {
	f.Add("/foo\n/bar\n/baz")
	f.Add("")
	f.Add("\n\n\n")
	f.Add("/path/with spaces\n/normal")
	f.Add("/null\x00byte\n/clean")
	f.Add("  /trimmed  \n  /also  ")
	f.Add("/single")

	f.Fuzz(func(t *testing.T, val string) {
		opts := map[string]string{"k": val}
		result := ParseListOption(opts, "k")
		for _, entry := range result {
			if entry == "" {
				t.Error("ParseListOption should not return empty entries")
			}
			if containsNull(entry) {
				t.Error("ParseListOption should not return entries with null bytes")
			}
		}
	})
}

// containsNull reports whether s contains any null bytes.
func containsNull(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

func FuzzValidateEnv(f *testing.F) {
	f.Add("KEY", "value")
	f.Add("", "value")
	f.Add("A=B", "value")
	f.Add("KEY", "val\x00ue")
	f.Add("K\x00EY", "value")
	f.Add("日本語", "こんにちは")

	f.Fuzz(func(_ *testing.T, key, val string) {
		env := map[string]string{key: val}
		_ = ValidateEnv(env)
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
