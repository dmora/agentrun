package agentrun

import "testing"

func TestContextFill(t *testing.T) {
	tests := []struct {
		name     string
		msg      Message
		wantUsed int
		wantSize int
		wantOK   bool
	}{
		{
			name: "nil usage",
			msg:  Message{},
		},
		{
			name: "both fields",
			msg: Message{
				Usage: &Usage{ContextUsedTokens: 45000, ContextSizeTokens: 200000},
			},
			wantUsed: 45000,
			wantSize: 200000,
			wantOK:   true,
		},
		{
			name: "used only",
			msg: Message{
				Usage: &Usage{ContextUsedTokens: 45000},
			},
			wantUsed: 45000,
			wantOK:   true,
		},
		{
			name: "size only",
			msg: Message{
				Usage: &Usage{ContextSizeTokens: 200000},
			},
			wantSize: 200000,
			wantOK:   true,
		},
		{
			name: "zero context fields",
			msg: Message{
				Usage: &Usage{InputTokens: 100},
			},
		},
		{
			name: "on MessageResult",
			msg: Message{
				Type:  MessageResult,
				Usage: &Usage{ContextUsedTokens: 50000},
			},
			wantUsed: 50000,
			wantOK:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			used, size, ok := ContextFill(tt.msg)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if used != tt.wantUsed {
				t.Errorf("used = %d, want %d", used, tt.wantUsed)
			}
			if size != tt.wantSize {
				t.Errorf("size = %d, want %d", size, tt.wantSize)
			}
		})
	}
}
