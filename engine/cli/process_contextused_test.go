package cli

import (
	"testing"

	"github.com/dmora/agentrun"
)

func TestCallContextFill(t *testing.T) {
	tests := []struct {
		name string
		u    *agentrun.Usage
		want int
	}{
		{name: "nil usage", u: nil, want: 0},
		{
			name: "basic fill",
			u:    &agentrun.Usage{InputTokens: 5000, CacheReadTokens: 3000, CacheWriteTokens: 200},
			want: 8200,
		},
		{
			name: "excludes output and thinking",
			u:    &agentrun.Usage{InputTokens: 5000, OutputTokens: 2000, ThinkingTokens: 500},
			want: 5000,
		},
		{
			name: "negative clamping",
			u:    &agentrun.Usage{InputTokens: -100, CacheReadTokens: 300},
			want: 300,
		},
		{name: "all zeros", u: &agentrun.Usage{}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := callContextFill(tt.u)
			if got != tt.want {
				t.Errorf("callContextFill() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEnrichContextUsed(t *testing.T) {
	tests := []struct {
		name     string
		usage    *agentrun.Usage
		wantUsed int
	}{
		{
			name:     "nil usage",
			usage:    nil,
			wantUsed: 0,
		},
		{
			name:     "basic sum",
			usage:    &agentrun.Usage{InputTokens: 1000, OutputTokens: 200},
			wantUsed: 1200,
		},
		{
			name:     "with thinking",
			usage:    &agentrun.Usage{InputTokens: 1000, OutputTokens: 200, ThinkingTokens: 500},
			wantUsed: 1700,
		},
		{
			name: "with cache tokens",
			usage: &agentrun.Usage{
				InputTokens:      1000,
				OutputTokens:     200,
				CacheReadTokens:  800,
				CacheWriteTokens: 50,
			},
			wantUsed: 2050,
		},
		{
			name:     "no override",
			usage:    &agentrun.Usage{InputTokens: 1000, OutputTokens: 200, ContextUsedTokens: 5000},
			wantUsed: 5000,
		},
		{
			name:     "no override negative",
			usage:    &agentrun.Usage{InputTokens: 500, OutputTokens: 200, ContextUsedTokens: -1},
			wantUsed: -1,
		},
		{
			name:     "all zeros",
			usage:    &agentrun.Usage{},
			wantUsed: 0,
		},
		{
			name:     "negative clamp",
			usage:    &agentrun.Usage{InputTokens: -100, OutputTokens: 200},
			wantUsed: 200,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enrichContextUsed(tt.usage)
			if tt.usage == nil {
				return // nil case: just verify no panic
			}
			if tt.usage.ContextUsedTokens != tt.wantUsed {
				t.Errorf("ContextUsedTokens = %d, want %d", tt.usage.ContextUsedTokens, tt.wantUsed)
			}
		})
	}
}
