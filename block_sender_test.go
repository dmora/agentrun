package agentrun

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTextBlock(t *testing.T) {
	b := TextBlock("hello world")
	if b.Type != "text" {
		t.Errorf("expected type 'text', got %q", b.Type)
	}
	if b.Text != "hello world" {
		t.Errorf("expected text 'hello world', got %q", b.Text)
	}
	if b.Source != nil {
		t.Errorf("expected nil Source, got %s", b.Source)
	}
}

func TestImageBase64Block(t *testing.T) {
	b := ImageBase64Block("image/png", "SGVsbG8=")
	if b.Type != "image" {
		t.Errorf("expected type 'image', got %q", b.Type)
	}
	if b.Text != "" {
		t.Errorf("expected empty text, got %q", b.Text)
	}
	if b.Source == nil {
		t.Fatal("expected non-nil Source")
	}

	var src struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(b.Source, &src); err != nil {
		t.Fatalf("failed to unmarshal source: %v", err)
	}
	if src.Type != "base64" {
		t.Errorf("expected source type 'base64', got %q", src.Type)
	}
	if src.MediaType != "image/png" {
		t.Errorf("expected source media_type 'image/png', got %q", src.MediaType)
	}
	if src.Data != "SGVsbG8=" {
		t.Errorf("expected source data 'SGVsbG8=', got %q", src.Data)
	}
}

func TestTextFromBlocks(t *testing.T) {
	tests := []struct {
		name   string
		blocks []ContentBlock
		want   string
	}{
		{
			name:   "empty",
			blocks: nil,
			want:   "",
		},
		{
			name: "text only",
			blocks: []ContentBlock{
				TextBlock("hello"),
				TextBlock("world"),
			},
			want: "hello\nworld",
		},
		{
			name: "mixed with image",
			blocks: []ContentBlock{
				TextBlock("hello"),
				ImageBase64Block("image/png", "data"),
				TextBlock("world"),
			},
			want: "hello\nworld",
		},
		{
			name: "empty blocks ignored",
			blocks: []ContentBlock{
				TextBlock(""),
				TextBlock("hello"),
			},
			want: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TextFromBlocks(tt.blocks)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateBlocks(t *testing.T) {
	longData := strings.Repeat("a", MaxBase64Size+1)

	tests := []struct {
		name    string
		blocks  []ContentBlock
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil blocks",
			blocks:  nil,
			wantErr: true,
			errMsg:  "no content blocks",
		},
		{
			name:    "empty blocks",
			blocks:  []ContentBlock{},
			wantErr: true,
			errMsg:  "no content blocks",
		},
		{
			name: "valid text",
			blocks: []ContentBlock{
				TextBlock("hello"),
			},
			wantErr: false,
		},
		{
			name: "empty type",
			blocks: []ContentBlock{
				{Type: ""},
			},
			wantErr: true,
			errMsg:  "empty type",
		},
		{
			name: "valid image",
			blocks: []ContentBlock{
				ImageBase64Block("image/png", "SGVsbG8="),
			},
			wantErr: false,
		},
		{
			name: "image missing source",
			blocks: []ContentBlock{
				{Type: "image"},
			},
			wantErr: true,
			errMsg:  "missing source",
		},
		{
			name: "image invalid source JSON",
			blocks: []ContentBlock{
				{Type: "image", Source: []byte("{invalid JSON")},
			},
			wantErr: true,
			errMsg:  "invalid image source JSON",
		},
		{
			name: "image unsupported source type",
			blocks: []ContentBlock{
				{Type: "image", Source: []byte(`{"type": "url", "url": "http://example.com"}`)},
			},
			wantErr: true,
			errMsg:  "unsupported image source type",
		},
		{
			name: "image unsupported media type",
			blocks: []ContentBlock{
				ImageBase64Block("image/tiff", "data"),
			},
			wantErr: true,
			errMsg:  "unsupported media type",
		},
		{
			name: "image exceeds size limit",
			blocks: []ContentBlock{
				ImageBase64Block("image/png", longData),
			},
			wantErr: true,
			errMsg:  "exceeds 15 MiB limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBlocks(tt.blocks)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBlocks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
			}
		})
	}
}

type mockBlockSender struct {
	*mockProcess
	sendBlocksFn func(ctx context.Context, blocks ...ContentBlock) error
}

func (m *mockBlockSender) SendBlocks(ctx context.Context, blocks ...ContentBlock) error {
	if m.sendBlocksFn != nil {
		return m.sendBlocksFn(ctx, blocks...)
	}
	return nil
}

var _ BlockSender = (*mockBlockSender)(nil)

type sequentialMockBlockSender struct {
	*mockBlockSender
}

func (s *sequentialMockBlockSender) SequentialSend() {}

var _ SequentialSender = (*sequentialMockBlockSender)(nil)
var _ BlockSender = (*sequentialMockBlockSender)(nil)

func TestRunTurnBlocks_Concurrent_BlockSender(t *testing.T) {
	mp := &mockBlockSender{
		mockProcess: newMockProcess(),
	}
	blocks := []ContentBlock{TextBlock("hello"), TextBlock("world")}
	gotBlocks := make(chan []ContentBlock, 1)
	mp.sendBlocksFn = func(_ context.Context, blks ...ContentBlock) error {
		gotBlocks <- blks
		return nil
	}

	mp.output <- Message{Type: MessageText, Content: "response"}
	mp.output <- Message{Type: MessageResult, Content: "done"}

	var msgs []Message
	err := RunTurnBlocks(context.Background(), mp, blocks, func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurnBlocks error: %v", err)
	}

	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}

	select {
	case blks := <-gotBlocks:
		if len(blks) != 2 || blks[0].Text != "hello" || blks[1].Text != "world" {
			t.Errorf("SendBlocks received unexpected blocks: %v", blks)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SendBlocks to be called")
	}
}

func TestRunTurnBlocks_Concurrent_Fallback(t *testing.T) {
	mp := newMockProcess()
	blocks := []ContentBlock{TextBlock("hello"), TextBlock("world")}
	gotText := make(chan string, 1)
	mp.sendFn = func(_ context.Context, text string) error {
		gotText <- text
		return nil
	}

	mp.output <- Message{Type: MessageText, Content: "response"}
	mp.output <- Message{Type: MessageResult, Content: "done"}

	var msgs []Message
	err := RunTurnBlocks(context.Background(), mp, blocks, func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurnBlocks error: %v", err)
	}

	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}

	select {
	case txt := <-gotText:
		if txt != "hello\nworld" {
			t.Errorf("Send received unexpected text: %q, want 'hello\\nworld'", txt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Send to be called")
	}
}

func TestRunTurnBlocks_Sequential_BlockSender(t *testing.T) {
	smp := &sequentialMockBlockSender{
		mockBlockSender: &mockBlockSender{
			mockProcess: newMockProcess(),
		},
	}
	blocks := []ContentBlock{TextBlock("hello")}
	smp.sendBlocksFn = func(_ context.Context, _ ...ContentBlock) error {
		smp.output = make(chan Message, 16)
		smp.output <- Message{Type: MessageText, Content: "sequential block response"}
		smp.output <- Message{Type: MessageResult, Content: "done"}
		return nil
	}

	var msgs []Message
	err := RunTurnBlocks(context.Background(), smp, blocks, func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurnBlocks error: %v", err)
	}

	if len(msgs) != 2 || msgs[0].Content != "sequential block response" {
		t.Errorf("msgs = %v, want 2 messages", msgs)
	}
}

func TestRunTurnBlocks_Sequential_Fallback(t *testing.T) {
	smp := newSequentialMockProcess()
	blocks := []ContentBlock{TextBlock("hello")}
	smp.sendFn = func(_ context.Context, _ string) error {
		smp.output = make(chan Message, 16)
		smp.output <- Message{Type: MessageText, Content: "sequential text response"}
		smp.output <- Message{Type: MessageResult, Content: "done"}
		return nil
	}

	var msgs []Message
	err := RunTurnBlocks(context.Background(), smp, blocks, func(msg Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTurnBlocks error: %v", err)
	}

	if len(msgs) != 2 || msgs[0].Content != "sequential text response" {
		t.Errorf("msgs = %v, want 2 messages", msgs)
	}
}
