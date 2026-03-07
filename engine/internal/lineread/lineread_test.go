package lineread

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadLine_MultiLine(t *testing.T) {
	input := "line1\nline2\nline3\n"
	lr := NewReader(strings.NewReader(input), 4096, 0)

	want := []string{"line1", "line2", "line3"}
	for _, w := range want {
		got, err := lr.ReadLineString()
		if err != nil {
			t.Fatalf("ReadLineString: %v", err)
		}
		if got != w {
			t.Errorf("got %q, want %q", got, w)
		}
	}
	_, err := lr.ReadLineString()
	if !errors.Is(err, io.EOF) {
		t.Errorf("final read: got %v, want io.EOF", err)
	}
}

func TestReadLine_LargerThanBuffer(t *testing.T) {
	// Line is 4096 bytes, buffer is 64 bytes — requires multi-fragment assembly.
	line := strings.Repeat("A", 4096)
	input := line + "\n"
	lr := NewReader(strings.NewReader(input), 64, 0)

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v", err)
	}
	if got != line {
		t.Errorf("len(got) = %d, want %d", len(got), len(line))
	}
}

func TestReadLine_LastLineWithoutNewline(t *testing.T) {
	input := "first\nsecond"
	lr := NewReader(strings.NewReader(input), 4096, 0)

	got1, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("line 1: %v", err)
	}
	if got1 != "first" {
		t.Errorf("line 1 = %q, want %q", got1, "first")
	}

	got2, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("line 2: %v", err)
	}
	if got2 != "second" {
		t.Errorf("line 2 = %q, want %q", got2, "second")
	}

	_, err = lr.ReadLineString()
	if !errors.Is(err, io.EOF) {
		t.Errorf("final read: got %v, want io.EOF", err)
	}
}

func TestReadLine_EmptyLines(t *testing.T) {
	input := "\n\n\n"
	lr := NewReader(strings.NewReader(input), 4096, 0)

	for range 3 {
		got, err := lr.ReadLineString()
		if err != nil {
			t.Fatalf("ReadLineString: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	}
}

func TestReadLine_MaxLineSizeExceeded(t *testing.T) {
	// First line exceeds max, second line is within bounds.
	input := strings.Repeat("X", 200) + "\nok\n"
	lr := NewReader(strings.NewReader(input), 64, 100)

	_, err := lr.ReadLineString()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("got %v, want ErrLineTooLong", err)
	}

	// Reader should stay aligned — next line reads fine.
	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString after overflow: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

func TestReadLine_MaxLineSizeZero_Unlimited(t *testing.T) {
	line := strings.Repeat("B", 10000)
	lr := NewReader(strings.NewReader(line+"\n"), 64, 0)

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v", err)
	}
	if got != line {
		t.Errorf("len(got) = %d, want %d", len(got), len(line))
	}
}

func TestReadLine_MaxLineSizeNegative_Unlimited(t *testing.T) {
	line := strings.Repeat("C", 10000)
	lr := NewReader(strings.NewReader(line+"\n"), 64, -1)

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v", err)
	}
	if got != line {
		t.Errorf("len(got) = %d, want %d", len(got), len(line))
	}
}

func TestReadLine_CRLFStripping(t *testing.T) {
	input := "hello\r\nworld\r\n"
	lr := NewReader(strings.NewReader(input), 4096, 0)

	tests := []string{"hello", "world"}
	for _, want := range tests {
		got, err := lr.ReadLineString()
		if err != nil {
			t.Fatalf("ReadLineString: %v", err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestReadLine_EmptyReader(t *testing.T) {
	lr := NewReader(strings.NewReader(""), 4096, 0)

	_, err := lr.ReadLineString()
	if !errors.Is(err, io.EOF) {
		t.Errorf("got %v, want io.EOF", err)
	}
}

func TestReadLine_MultipleOversizedLines(t *testing.T) {
	// Three oversized lines followed by one valid line.
	var b bytes.Buffer
	for range 3 {
		b.WriteString(strings.Repeat("Z", 200))
		b.WriteByte('\n')
	}
	b.WriteString("valid\n")

	lr := NewReader(&b, 64, 100)

	for range 3 {
		_, err := lr.ReadLineString()
		if !errors.Is(err, ErrLineTooLong) {
			t.Fatalf("got %v, want ErrLineTooLong", err)
		}
	}

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v", err)
	}
	if got != "valid" {
		t.Errorf("got %q, want %q", got, "valid")
	}
}

func TestReadLine_ExactlyAtMaxLineSize(t *testing.T) {
	// maxLineSize applies to content (post-strip), not raw bytes.
	// 100-byte content + \n delimiter → content is exactly 100, should succeed.
	line := strings.Repeat("E", 100)
	lr := NewReader(strings.NewReader(line+"\n"), 4096, 100)

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v (100-byte content should fit maxLineSize=100)", err)
	}
	if got != line {
		t.Errorf("got %q, want %q", got, line)
	}
}

func TestReadLine_ExactlyAtMaxLineSize_CRLF(t *testing.T) {
	// 100-byte content + \r\n delimiter → content is exactly 100, should succeed.
	line := strings.Repeat("F", 100)
	lr := NewReader(strings.NewReader(line+"\r\n"), 4096, 100)

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v (100-byte content with CRLF should fit maxLineSize=100)", err)
	}
	if got != line {
		t.Errorf("got %q, want %q", got, line)
	}
}

func TestReadLine_OneOverMaxLineSize(t *testing.T) {
	// 101-byte content exceeds maxLineSize=100.
	line := strings.Repeat("G", 101)
	lr := NewReader(strings.NewReader(line+"\n"), 4096, 100)

	_, err := lr.ReadLineString()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("got %v, want ErrLineTooLong", err)
	}
}

func TestReadLine_JustUnderMaxLineSize(t *testing.T) {
	// Line content is 99 bytes + newline — well within limit.
	line := strings.Repeat("U", 99)
	lr := NewReader(strings.NewReader(line+"\n"), 4096, 100)

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v", err)
	}
	if got != line {
		t.Errorf("got %q, want %q", got, line)
	}
}

func TestReadLine_MaxLineSizeExceeded_MultiFragment(t *testing.T) {
	// Content exceeds limit across fragments (buffer=32, content=200, limit=100).
	line := strings.Repeat("M", 200)
	lr := NewReader(strings.NewReader(line+"\n"), 32, 100)

	_, err := lr.ReadLineString()
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("got %v, want ErrLineTooLong", err)
	}

	// Verify alignment — next line reads fine.
	lr2 := NewReader(strings.NewReader(strings.Repeat("M", 200)+"\nok\n"), 32, 100)
	_, _ = lr2.ReadLineString() // discard oversized
	got, err := lr2.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

func TestReadLine_ExactlyAtMaxLineSize_MultiFragment(t *testing.T) {
	// 100-byte content with buffer=32 (multi-fragment), maxLineSize=100.
	line := strings.Repeat("N", 100)
	lr := NewReader(strings.NewReader(line+"\n"), 32, 100)

	got, err := lr.ReadLineString()
	if err != nil {
		t.Fatalf("ReadLineString: %v", err)
	}
	if got != line {
		t.Errorf("len(got) = %d, want %d", len(got), len(line))
	}
}

func FuzzReadLine(f *testing.F) {
	f.Add([]byte("hello\nworld\n"), 64, 0)
	f.Add([]byte(""), 64, 100)
	f.Add([]byte("\n\n"), 16, 10)
	f.Add([]byte(strings.Repeat("x", 500)+"\n"), 32, 100)

	f.Fuzz(func(_ *testing.T, data []byte, bufSize, maxLine int) {
		if bufSize <= 0 {
			bufSize = 16
		}
		lr := NewReader(bytes.NewReader(data), bufSize, maxLine)
		for {
			_, err := lr.ReadLine()
			if err != nil {
				break
			}
		}
	})
}
