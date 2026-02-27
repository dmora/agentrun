package agentrun

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestResolveOptions_Zero(t *testing.T) {
	got := ResolveOptions()
	if got.Prompt != "" || got.Model != "" || got.Timeout != 0 {
		t.Fatalf("zero opts: want zero StartOptions, got %+v", got)
	}
}

func TestResolveOptions_Single(t *testing.T) {
	got := ResolveOptions(WithPrompt("single-prompt"))
	if got.Prompt != "single-prompt" {
		t.Fatalf("want Prompt=single-prompt, got %q", got.Prompt)
	}
	if got.Model != "" || got.Timeout != 0 {
		t.Fatalf("other fields should be zero, got %+v", got)
	}
}

func TestResolveOptions_LastWriterWins(t *testing.T) {
	got := ResolveOptions(
		WithPrompt("first"),
		WithPrompt("second"),
	)
	if got.Prompt != "second" {
		t.Fatalf("want last-writer-wins Prompt=second, got %q", got.Prompt)
	}
}

func TestResolveOptions_NilOptionSkipped(t *testing.T) {
	got := ResolveOptions(nil, WithModel("gpt-4"), nil)
	if got.Model != "gpt-4" {
		t.Fatalf("want Model=gpt-4, got %q", got.Model)
	}
}

func TestWithPrompt(t *testing.T) {
	got := ResolveOptions(WithPrompt("p"))
	if got.Prompt != "p" {
		t.Fatalf("want Prompt=p, got %q", got.Prompt)
	}
	if got.Model != "" || got.Timeout != 0 {
		t.Fatal("WithPrompt should not set other fields")
	}
}

func TestWithModel(t *testing.T) {
	got := ResolveOptions(WithModel("m"))
	if got.Model != "m" {
		t.Fatalf("want Model=m, got %q", got.Model)
	}
	if got.Prompt != "" || got.Timeout != 0 {
		t.Fatal("WithModel should not set other fields")
	}
}

func TestWithTimeout(t *testing.T) {
	got := ResolveOptions(WithTimeout(5 * time.Second))
	if got.Timeout != 5*time.Second {
		t.Fatalf("want Timeout=5s, got %v", got.Timeout)
	}
	if got.Prompt != "" || got.Model != "" {
		t.Fatal("WithTimeout should not set other fields")
	}
}

func TestResolveOptions_AllOptions(t *testing.T) {
	got := ResolveOptions(
		WithPrompt("all-prompt"),
		WithModel("all-model"),
		WithTimeout(10*time.Second),
	)
	if got.Prompt != "all-prompt" || got.Model != "all-model" || got.Timeout != 10*time.Second {
		t.Fatalf("want all fields set, got %+v", got)
	}
}

func TestSentinelErrors_Identity(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrUnavailable", ErrUnavailable},
		{"ErrTerminated", ErrTerminated},
		{"ErrSessionNotFound", ErrSessionNotFound},
		{"ErrSendNotSupported", ErrSendNotSupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.err) {
				t.Fatalf("errors.Is(%v, %v) should be true", tt.name, tt.name)
			}
		})
	}
}

func TestSentinelErrors_Wrapping(t *testing.T) {
	tests := []struct {
		name     string
		sentinel error
	}{
		{"ErrUnavailable", ErrUnavailable},
		{"ErrTerminated", ErrTerminated},
		{"ErrSessionNotFound", ErrSessionNotFound},
		{"ErrSendNotSupported", ErrSendNotSupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			//nolint:errorlint // testing fmt.Errorf wrapping intentionally
			wrapped := errors.Join(errors.New("context"), tt.sentinel)
			if !errors.Is(wrapped, tt.sentinel) {
				t.Fatalf("wrapped error should match %v via errors.Is", tt.name)
			}
		})
	}
}

func TestSentinelErrors_Distinct(t *testing.T) {
	if errors.Is(ErrUnavailable, ErrTerminated) {
		t.Fatal("ErrUnavailable should not match ErrTerminated")
	}
	if errors.Is(ErrUnavailable, ErrSessionNotFound) {
		t.Fatal("ErrUnavailable should not match ErrSessionNotFound")
	}
	if errors.Is(ErrTerminated, ErrSessionNotFound) {
		t.Fatal("ErrTerminated should not match ErrSessionNotFound")
	}
}

func TestMessageJSON_Full(t *testing.T) {
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	msg := Message{
		Type:    MessageText,
		Content: "hi there",
		Tool: &ToolCall{
			Name:  "read_file",
			Input: json.RawMessage(`{"path":"foo.go"}`),
		},
		Usage: &Usage{
			InputTokens:      100,
			OutputTokens:     50,
			CacheReadTokens:  25,
			CacheWriteTokens: 10,
			ThinkingTokens:   5,
			CostUSD:          0.0042,
		},
		StopReason: StopEndTurn,
		ErrorCode:  "rate_limit",
		ResumeID:   "ses_abc123",
		Raw:        json.RawMessage(`{"raw":true}`),
		Timestamp:  ts,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	t.Run("BaseFields", func(t *testing.T) {
		if got.Type != MessageText {
			t.Errorf("Type: want %q, got %q", MessageText, got.Type)
		}
		if got.Content != "hi there" {
			t.Errorf("Content: want 'hi there', got %q", got.Content)
		}
		if got.Tool == nil || got.Tool.Name != "read_file" {
			t.Errorf("Tool.Name: want read_file, got %v", got.Tool)
		}
		if got.StopReason != StopEndTurn {
			t.Errorf("StopReason: want %q, got %q", StopEndTurn, got.StopReason)
		}
		if got.ErrorCode != "rate_limit" {
			t.Errorf("ErrorCode: want 'rate_limit', got %q", got.ErrorCode)
		}
		if got.ResumeID != "ses_abc123" {
			t.Errorf("ResumeID: want 'ses_abc123', got %q", got.ResumeID)
		}
		if !got.Timestamp.Equal(ts) {
			t.Errorf("Timestamp: want %v, got %v", ts, got.Timestamp)
		}
	})

	t.Run("Usage", func(t *testing.T) {
		if got.Usage == nil {
			t.Fatal("Usage should be populated")
		}
		if got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 50 {
			t.Errorf("Usage base: want {100,50}, got {%d,%d}", got.Usage.InputTokens, got.Usage.OutputTokens)
		}
		if got.Usage.CacheReadTokens != 25 {
			t.Errorf("CacheReadTokens: want 25, got %d", got.Usage.CacheReadTokens)
		}
		if got.Usage.CacheWriteTokens != 10 {
			t.Errorf("CacheWriteTokens: want 10, got %d", got.Usage.CacheWriteTokens)
		}
		if got.Usage.ThinkingTokens != 5 {
			t.Errorf("ThinkingTokens: want 5, got %d", got.Usage.ThinkingTokens)
		}
		if got.Usage.CostUSD != 0.0042 {
			t.Errorf("CostUSD: want 0.0042, got %f", got.Usage.CostUSD)
		}
	})
}

func TestMessageJSON_Minimal(t *testing.T) {
	msg := Message{Type: MessageEOF}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify omitempty fields are absent.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	if _, ok := raw["type"]; !ok {
		t.Error("type field should be present")
	}
	// Timestamp is always present (time.Time is not omitempty-compatible).
	for _, key := range []string{"content", "tool", "usage", "stop_reason", "error_code", "resume_id", "raw"} {
		if _, ok := raw[key]; ok {
			t.Errorf("field %q should be omitted on minimal message", key)
		}
	}
}

func TestModeValid(t *testing.T) {
	valid := []Mode{ModePlan, ModeAct}
	for _, m := range valid {
		if !m.Valid() {
			t.Errorf("Mode(%q).Valid() = false, want true", m)
		}
	}
	invalid := []Mode{"", "invalid", "PLAN", "Act"}
	for _, m := range invalid {
		if m.Valid() {
			t.Errorf("Mode(%q).Valid() = true, want false", m)
		}
	}
}

func TestHITLValid(t *testing.T) {
	valid := []HITL{HITLOn, HITLOff}
	for _, h := range valid {
		if !h.Valid() {
			t.Errorf("HITL(%q).Valid() = false, want true", h)
		}
	}
	invalid := []HITL{"", "invalid", "ON", "Off"}
	for _, h := range invalid {
		if h.Valid() {
			t.Errorf("HITL(%q).Valid() = true, want false", h)
		}
	}
}

func TestSessionJSON_RoundTrip(t *testing.T) {
	s := Session{
		ID:     "s1",
		CWD:    "/tmp",
		Model:  "claude",
		Prompt: "greet me",
		Options: map[string]string{
			"mode":        "auto",
			OptionAgentID: "agent-1",
		},
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != s.ID || got.CWD != s.CWD {
		t.Errorf("identity fields mismatch: got %+v", got)
	}
	if got.Options["mode"] != "auto" {
		t.Errorf("Options[mode]: want auto, got %q", got.Options["mode"])
	}
	if got.Options[OptionAgentID] != "agent-1" {
		t.Errorf("Options[OptionAgentID]: want agent-1, got %q", got.Options[OptionAgentID])
	}
}

func TestSessionOptions_ResumeID_RoundTrip(t *testing.T) {
	s := Session{
		ID:  "s1",
		CWD: "/tmp",
		Options: map[string]string{
			OptionResumeID: "ses_abc123def456ghi789jkl",
		},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Options[OptionResumeID] != "ses_abc123def456ghi789jkl" {
		t.Errorf("Options[OptionResumeID] = %q, want %q", got.Options[OptionResumeID], "ses_abc123def456ghi789jkl")
	}
}

func TestEffortValid(t *testing.T) {
	valid := []Effort{EffortLow, EffortMedium, EffortHigh, EffortMax}
	for _, e := range valid {
		if !e.Valid() {
			t.Errorf("Effort(%q).Valid() = false, want true", e)
		}
	}
	invalid := []Effort{"", "invalid", "LOW", "Medium", "xhigh"}
	for _, e := range invalid {
		if e.Valid() {
			t.Errorf("Effort(%q).Valid() = true, want false", e)
		}
	}
}

func TestMergeEnv_Empty(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/user"}
	got := MergeEnv(base, nil)
	if got != nil {
		t.Fatalf("MergeEnv with nil extra should return nil, got %v", got)
	}
	got = MergeEnv(base, map[string]string{})
	if got != nil {
		t.Fatalf("MergeEnv with empty extra should return nil, got %v", got)
	}
}

func TestMergeEnv_Appends(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/user"}
	extra := map[string]string{"FOO": "bar"}
	got := MergeEnv(base, extra)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d: %v", len(got), got)
	}
	if got[0] != "PATH=/usr/bin" || got[1] != "HOME=/home/user" {
		t.Errorf("base entries should be preserved: %v", got[:2])
	}
	if got[2] != "FOO=bar" {
		t.Errorf("extra entry: want FOO=bar, got %q", got[2])
	}
}

func TestMergeEnv_OverrideByAppend(t *testing.T) {
	// When the same key exists in base and extra, the last entry wins
	// per exec.Cmd.Env behavior (later entries override earlier ones).
	base := []string{"FOO=original"}
	extra := map[string]string{"FOO": "override"}
	got := MergeEnv(base, extra)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}
	// Both entries present; exec.Cmd uses the last one.
	if got[0] != "FOO=original" {
		t.Errorf("first: want FOO=original, got %q", got[0])
	}
	if got[1] != "FOO=override" {
		t.Errorf("second: want FOO=override, got %q", got[1])
	}
}

func TestMergeEnv_NilBase(t *testing.T) {
	extra := map[string]string{"FOO": "bar"}
	got := MergeEnv(nil, extra)
	if len(got) != 1 || got[0] != "FOO=bar" {
		t.Fatalf("want [FOO=bar], got %v", got)
	}
}

func TestMergeEnv_UnicodeValues(t *testing.T) {
	base := []string{"LANG=en_US.UTF-8"}
	extra := map[string]string{"GREETING": "こんにちは"}
	got := MergeEnv(base, extra)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[1] != "GREETING=こんにちは" {
		t.Errorf("want GREETING=こんにちは, got %q", got[1])
	}
}

func TestSessionJSON_WithEnv(t *testing.T) {
	s := Session{
		ID:  "s1",
		CWD: "/tmp",
		Env: map[string]string{"FOO": "bar"},
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Session
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Env["FOO"] != "bar" {
		t.Errorf("Env[FOO] = %q, want bar", got.Env["FOO"])
	}
}

func TestSessionJSON_EnvOmitEmpty(t *testing.T) {
	s := Session{ID: "s1", CWD: "/tmp"}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["env"]; ok {
		t.Error("env field should be omitted when nil")
	}
}

func TestSessionEnv_MapAliasing(t *testing.T) {
	original := Session{
		ID:  "s1",
		Env: map[string]string{"key": "val"},
	}
	copied := original
	copied.Env["key"] = "changed"

	// Document: shallow copy shares the Env map (same as Options).
	if original.Env["key"] != "changed" {
		t.Fatal("Session is a value type with a reference-type map; shallow copy shares Env")
	}
}

func TestSessionClone(t *testing.T) {
	original := Session{
		ID:      "s1",
		Options: map[string]string{"key": "original"},
		Env:     map[string]string{"FOO": "bar"},
	}
	cloned := original.Clone()

	cloned.Options["key"] = "modified"
	cloned.Env["FOO"] = "modified"

	if original.Options["key"] != "original" {
		t.Error("Clone did not deep-copy Options")
	}
	if original.Env["FOO"] != "bar" {
		t.Error("Clone did not deep-copy Env")
	}
}

func TestSessionClone_NilMaps(t *testing.T) {
	original := Session{ID: "s1"}
	cloned := original.Clone()

	// Clone of nil maps should remain nil (not empty map).
	if cloned.Options != nil {
		t.Error("Clone of nil Options should be nil")
	}
	if cloned.Env != nil {
		t.Error("Clone of nil Env should be nil")
	}
}

func TestSessionOptions_MapAliasing(t *testing.T) {
	original := Session{
		ID:      "s1",
		Options: map[string]string{"key": "val"},
	}
	copied := original
	copied.Options["key"] = "mutated"

	// Document: shallow copy shares the map. This test documents the behavior.
	if original.Options["key"] != "mutated" {
		t.Fatal("Session is a value type with a reference-type map; shallow copy shares Options")
	}
}

// --- Usage JSON tests ---

func TestUsage_JSON_OmitemptyNewFields(t *testing.T) {
	u := Usage{InputTokens: 10, OutputTokens: 5}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"cache_read_tokens", "cache_write_tokens", "thinking_tokens", "cost_usd"} {
		if _, ok := raw[key]; ok {
			t.Errorf("field %q should be omitted when zero", key)
		}
	}
	// Existing fields always present (no omitempty).
	for _, key := range []string{"input_tokens", "output_tokens"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("field %q should always be present", key)
		}
	}
}

func TestUsage_JSON_WithAllFields(t *testing.T) {
	u := Usage{
		InputTokens:      100,
		OutputTokens:     50,
		CacheReadTokens:  25,
		CacheWriteTokens: 10,
		ThinkingTokens:   5,
		CostUSD:          0.0042,
	}
	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Usage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != u {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, u)
	}
}

func TestStopReasonConstants(t *testing.T) {
	tests := []struct {
		name string
		sr   StopReason
		want string
	}{
		{"EndTurn", StopEndTurn, "end_turn"},
		{"MaxTokens", StopMaxTokens, "max_tokens"},
		{"ToolUse", StopToolUse, "tool_use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.sr) != tt.want {
				t.Errorf("StopReason = %q, want %q", tt.sr, tt.want)
			}
		})
	}
}
