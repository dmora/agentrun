package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ContentBlock is a single content element in a prompt.
//
// Modern LLMs support multi-modal inputs. Backends that support multi-modal
// input accept ContentBlock arrays.
type ContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Source   json.RawMessage `json:"source,omitempty"` // image source (base64/URL) — format follows Anthropic API
	MimeType string          `json:"mime_type,omitempty"`
}

// TextBlock creates a text-only ContentBlock.
func TextBlock(s string) ContentBlock {
	return ContentBlock{
		Type: "text",
		Text: s,
	}
}

// ImageBase64Block creates an image ContentBlock from base64 data.
// mediaType should be one of "image/jpeg", "image/png", "image/gif", "image/webp".
// data is the raw base64 data string.
func ImageBase64Block(mediaType, data string) ContentBlock {
	src := map[string]string{
		"type":       "base64",
		"media_type": mediaType,
		"data":       data,
	}
	srcJSON, _ := json.Marshal(src) // marshalling simple map cannot fail
	return ContentBlock{
		Type:   "image",
		Source: json.RawMessage(srcJSON),
	}
}

// TextFromBlocks extracts concatenated text from blocks.
// Backends that only support text use this for graceful degradation.
func TextFromBlocks(blocks []ContentBlock) string {
	var sb strings.Builder
	first := true
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			if !first {
				sb.WriteByte('\n')
			}
			sb.WriteString(b.Text)
			first = false
		}
	}
	return sb.String()
}

// MaxBase64Size is the maximum allowed size for base64 image payload (15 MiB).
const MaxBase64Size = 15 * 1024 * 1024

// ValidateBlocks checks the validity of the content blocks.
// Rejects blocks with empty Type and enforces a 15 MiB limit on base64 data.
func ValidateBlocks(blocks []ContentBlock) error {
	if len(blocks) == 0 {
		return errors.New("no content blocks provided")
	}
	for i, b := range blocks {
		if strings.TrimSpace(b.Type) == "" {
			return fmt.Errorf("block %d: empty type", i)
		}
		if b.Type == "image" {
			if err := validateImageBlock(b, i); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateImageBlock(b ContentBlock, index int) error {
	if len(b.Source) == 0 {
		return fmt.Errorf("block %d: image block missing source", index)
	}
	var src struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}
	if err := json.Unmarshal(b.Source, &src); err != nil {
		return fmt.Errorf("block %d: invalid image source JSON: %w", index, err)
	}
	if src.Type != "base64" {
		return fmt.Errorf("block %d: unsupported image source type %q", index, src.Type)
	}
	switch src.MediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		// valid media type
	default:
		return fmt.Errorf("block %d: unsupported media type %q", index, src.MediaType)
	}
	if len(src.Data) > MaxBase64Size {
		return fmt.Errorf("block %d: base64 image data exceeds 15 MiB limit", index)
	}
	return nil
}

// BlockSender is an optional interface for processes that support structured content.
// Discovered via type assertion on Process:
//
//	if bs, ok := proc.(BlockSender); ok {
//	    bs.SendBlocks(ctx, TextBlock("describe this"), ImageBase64Block("image/png", data))
//	}
type BlockSender interface {
	Process
	SendBlocks(ctx context.Context, blocks ...ContentBlock) error
}

// RunTurnBlocks sends structured content blocks and drains Output() until MessageResult
// or channel close. handler is called for each message (including MessageResult).
// Safe for all engine types.
//
// If the process satisfies [BlockSender], it uses SendBlocks to transmit the blocks.
// If the process does not satisfy [BlockSender], it degrades gracefully by converting
// the blocks to text using [TextFromBlocks] and calling the process's Send method.
//
// Like [RunTurn], it respects [SequentialSender] to select the send/drain strategy.
func RunTurnBlocks(ctx context.Context, proc Process, blocks []ContentBlock, handler func(Message) error) error {
	if _, ok := proc.(SequentialSender); ok {
		// Send must complete before draining Output (spawn-per-turn backends).
		if err := sendBlocksOrDegrade(ctx, proc, blocks); err != nil {
			return err
		}
		return drainOutput(ctx, proc, nil, handler)
	}
	// Default: concurrent Send + drain (ACP, streaming CLI).
	sendCh := make(chan error, 1)
	go func() {
		sendCh <- sendBlocksOrDegrade(ctx, proc, blocks)
	}()

	return drainOutput(ctx, proc, sendCh, handler)
}

func sendBlocksOrDegrade(ctx context.Context, proc Process, blocks []ContentBlock) error {
	if bs, ok := proc.(BlockSender); ok {
		return bs.SendBlocks(ctx, blocks...)
	}
	// Fallback to text Send
	return proc.Send(ctx, TextFromBlocks(blocks))
}
