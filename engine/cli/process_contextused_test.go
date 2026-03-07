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
