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
		Usage:     &Usage{InputTokens: 100, OutputTokens: 50},
		ResumeID:  "ses_abc123",
		Raw:       json.RawMessage(`{"raw":true}`),
		Timestamp: ts,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Type != MessageText {
		t.Errorf("Type: want %q, got %q", MessageText, got.Type)
	}
	if got.Content != "hi there" {
		t.Errorf("Content: want 'hi there', got %q", got.Content)
	}
	if got.Tool == nil || got.Tool.Name != "read_file" {
		t.Errorf("Tool.Name: want read_file, got %v", got.Tool)
	}
	if got.Usage == nil || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 50 {
		t.Errorf("Usage: want {100,50}, got %v", got.Usage)
	}
	if got.ResumeID != "ses_abc123" {
		t.Errorf("ResumeID: want 'ses_abc123', got %q", got.ResumeID)
	}
	if !got.Timestamp.Equal(ts) {
		t.Errorf("Timestamp: want %v, got %v", ts, got.Timestamp)
	}
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
	for _, key := range []string{"content", "tool", "usage", "resume_id", "raw"} {
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
