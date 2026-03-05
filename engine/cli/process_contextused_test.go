package cli

import (
	"testing"

	"github.com/dmora/agentrun"
)

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
