//go:build !windows

package cli

import (
	"testing"
	"time"

	"github.com/dmora/agentrun"
)

func TestSynthesizeContextWindow(t *testing.T) {
	ts := time.Now()

	tests := []struct {
		name string
		msg  agentrun.Message
		want bool // whether synthesis should produce a message
	}{
		{
			name: "mid-turn text with usage",
			msg: agentrun.Message{
				Type:      agentrun.MessageText,
				Usage:     &agentrun.Usage{ContextUsedTokens: 45000},
				Timestamp: ts,
			},
			want: true,
		},
		{
			name: "mid-turn thinking with usage",
			msg: agentrun.Message{
				Type:      agentrun.MessageThinking,
				Usage:     &agentrun.Usage{ContextUsedTokens: 30000},
				Timestamp: ts,
			},
			want: true,
		},
		{
			name: "size only",
			msg: agentrun.Message{
				Type:      agentrun.MessageText,
				Usage:     &agentrun.Usage{ContextSizeTokens: 200000},
				Timestamp: ts,
			},
			want: true,
		},
		{
			name: "both fields",
			msg: agentrun.Message{
				Type:      agentrun.MessageText,
				Usage:     &agentrun.Usage{ContextUsedTokens: 45000, ContextSizeTokens: 200000},
				Timestamp: ts,
			},
			want: true,
		},
		{
			name: "result — no synthesis",
			msg: agentrun.Message{
				Type:      agentrun.MessageResult,
				Usage:     &agentrun.Usage{ContextUsedTokens: 50000},
				Timestamp: ts,
			},
			want: false,
		},
		{
			name: "init — no synthesis",
			msg: agentrun.Message{
				Type:      agentrun.MessageInit,
				Usage:     &agentrun.Usage{ContextUsedTokens: 10000},
				Timestamp: ts,
			},
			want: false,
		},
		{
			name: "nil usage — no synthesis",
			msg: agentrun.Message{
				Type:      agentrun.MessageText,
				Timestamp: ts,
			},
			want: false,
		},
		{
			name: "zero fill — no synthesis",
			msg: agentrun.Message{
				Type:      agentrun.MessageText,
				Usage:     &agentrun.Usage{InputTokens: 100},
				Timestamp: ts,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := synthesizeContextWindow(tt.msg)
			if tt.want && got == nil {
				t.Fatal("expected synthesized message, got nil")
			}
			if !tt.want && got != nil {
				t.Fatalf("expected nil, got %+v", got)
			}
			if got == nil {
				return
			}
			if got.Type != agentrun.MessageContextWindow {
				t.Errorf("type = %q, want %q", got.Type, agentrun.MessageContextWindow)
			}
			if got.Usage == nil {
				t.Fatal("Usage should not be nil")
			}
			if got.Usage.ContextUsedTokens != tt.msg.Usage.ContextUsedTokens {
				t.Errorf("ContextUsedTokens = %d, want %d", got.Usage.ContextUsedTokens, tt.msg.Usage.ContextUsedTokens)
			}
			if got.Usage.ContextSizeTokens != tt.msg.Usage.ContextSizeTokens {
				t.Errorf("ContextSizeTokens = %d, want %d", got.Usage.ContextSizeTokens, tt.msg.Usage.ContextSizeTokens)
			}
			if got.Timestamp != tt.msg.Timestamp {
				t.Errorf("timestamp mismatch")
			}
			// Other usage fields should be zero.
			if got.Usage.InputTokens != 0 || got.Usage.OutputTokens != 0 || got.Usage.CostUSD != 0 {
				t.Error("non-context-fill usage fields should be zero")
			}
		})
	}
}
