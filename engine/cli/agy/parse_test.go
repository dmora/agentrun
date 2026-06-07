package agy

import (
	"errors"
	"testing"

	"github.com/dmora/agentrun"
	"github.com/dmora/agentrun/engine/cli"
)

func TestParseLine(t *testing.T) {
	b := New()

	tests := []struct {
		name    string
		line    string
		want    agentrun.Message
		wantErr error
	}{
		{
			name:    "empty line",
			line:    "   \t  ",
			wantErr: cli.ErrSkipLine,
		},
		{
			name: "result sentinel",
			line: `{"type":"result","stop_reason":"end_turn"}`,
			want: agentrun.Message{
				Type:       agentrun.MessageResult,
				StopReason: agentrun.StopEndTurn,
			},
		},
		{
			name: "text output",
			line: "Here is the code you requested:",
			want: agentrun.Message{
				Type:    agentrun.MessageText,
				Content: "Here is the code you requested:",
			},
		},
		{
			name: "json-like text",
			line: `{"type":"not_result"}`,
			want: agentrun.Message{
				Type:    agentrun.MessageText,
				Content: `{"type":"not_result"}`,
			},
		},
		{
			name:    "agy_session sentinel is captured, not surfaced",
			line:    `{"type":"agy_session","id":"d8e79181-5db2-4ea9-88e2-eea15ddab587"}`,
			wantErr: cli.ErrSkipLine,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := b.ParseLine(tc.line)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ParseLine(%q) err = %v, want %v", tc.line, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLine(%q) unexpected error: %v", tc.line, err)
			}
			if got.Type != tc.want.Type {
				t.Errorf("Type = %v, want %v", got.Type, tc.want.Type)
			}
			if got.Content != tc.want.Content {
				t.Errorf("Content = %v, want %v", got.Content, tc.want.Content)
			}
			if got.StopReason != tc.want.StopReason {
				t.Errorf("StopReason = %v, want %v", got.StopReason, tc.want.StopReason)
			}
		})
	}
}

func FuzzParseLine(f *testing.F) {
	f.Add("")
	f.Add(`{"type":"result","stop_reason":"end_turn"}`)
	f.Add("Some random text output")
	f.Add("\x00\x00\x00")
	f.Add(`{"type":"agy_session","id":"d8e79181-5db2-4ea9-88e2-eea15ddab587"}`)

	b := New()
	f.Fuzz(func(_ *testing.T, line string) {
		// ParseLine should never panic
		_, _ = b.ParseLine(line)
	})
}
