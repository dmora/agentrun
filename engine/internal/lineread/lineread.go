// Package lineread provides a line reader that supports lines larger than
// the internal buffer while enforcing an optional maximum assembled line size.
//
// Unlike bufio.Scanner, which enforces a hard max token size that cannot be
// bypassed, lineread.Reader uses bufio.Reader.ReadSlice in a loop. This allows
// it to track accumulated size before allocating the final line, failing fast
// on oversized lines without assembling the full byte slice first.
package lineread

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// ErrLineTooLong is returned when a line exceeds the configured maximum size.
var ErrLineTooLong = errors.New("lineread: line too long")

// Reader reads newline-delimited lines from an io.Reader. Lines larger than
// the internal buffer are assembled from multiple ReadSlice calls. An optional
// maxLineSize enforces a cap on the assembled line size.
type Reader struct {
	r           *bufio.Reader
	maxLineSize int // <= 0 means unlimited
}

// NewReader creates a Reader with the given internal buffer size and maximum
// line size. bufSize sets the internal bufio.Reader chunk size. maxLineSize
// sets the maximum assembled line size in bytes (content, excluding the
// newline delimiter); <= 0 means unlimited.
func NewReader(r io.Reader, bufSize, maxLineSize int) *Reader {
	if bufSize <= 0 {
		bufSize = 4096
	}
	return &Reader{
		r:           bufio.NewReaderSize(r, bufSize),
		maxLineSize: maxLineSize,
	}
}

// ReadLine reads and returns the next line, stripping the trailing newline
// (\n or \r\n). Returns (nil, io.EOF) at end of stream. Returns (nil,
// ErrLineTooLong) if the line content exceeds maxLineSize; the remainder of
// the oversized line is discarded so the reader stays aligned for the next
// line.
//
// For single-fragment lines (the common case), the returned slice is a view
// into the internal buffer and is only valid until the next call. Callers
// that need to retain the data must copy it. ReadLineString always returns
// an owned string.
//
// For multi-fragment lines, a bytes.Buffer accumulates fragments directly,
// avoiding separate per-fragment allocations.
func (lr *Reader) ReadLine() ([]byte, error) {
	var buf *bytes.Buffer
	var totalSize int

	for {
		fragment, err := lr.r.ReadSlice('\n')
		totalSize += len(fragment)

		if err == nil {
			// Found newline. The limit applies to content (post-strip),
			// so subtract the newline overhead (\n or \r\n).
			if lr.exceedsMax(totalSize - newlineOverhead(buf, fragment)) {
				return nil, ErrLineTooLong
			}
			return lr.finishLine(buf, fragment), nil
		}
		if lr.exceedsMax(contentEstimate(totalSize, fragment)) {
			lr.drainIfBufferFull(err)
			return nil, ErrLineTooLong
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			if buf == nil {
				buf = &bytes.Buffer{}
			}
			buf.Write(fragment)
			continue
		}
		return lr.handleEOFOrError(buf, fragment, err)
	}
}

// ReadLineString is a convenience wrapper that returns the line as a string.
func (lr *Reader) ReadLineString() (string, error) {
	line, err := lr.ReadLine()
	if err != nil {
		return "", err
	}
	return string(line), nil
}

// exceedsMax reports whether contentSize exceeds the configured maximum.
// Always returns false when maxLineSize <= 0 (unlimited).
func (lr *Reader) exceedsMax(contentSize int) bool {
	return lr.maxLineSize > 0 && contentSize > lr.maxLineSize
}

// contentEstimate returns the estimated content size for a mid-stream
// fragment. A trailing \r is discounted because it may be part of a \r\n
// pair split across the buffer boundary.
func contentEstimate(totalSize int, fragment []byte) int {
	if len(fragment) > 0 && fragment[len(fragment)-1] == '\r' {
		return totalSize - 1
	}
	return totalSize
}

// drainIfBufferFull discards the remainder of an oversized line when
// ReadSlice returned bufio.ErrBufferFull, keeping the reader aligned.
func (lr *Reader) drainIfBufferFull(err error) {
	if errors.Is(err, bufio.ErrBufferFull) {
		lr.discardLine()
	}
}

// finishLine assembles the final line result. For single-fragment lines
// (buf == nil), returns stripNewline applied to the fragment directly.
// For multi-fragment lines, appends the final fragment to the buffer
// and strips in place.
func (lr *Reader) finishLine(buf *bytes.Buffer, fragment []byte) []byte {
	if buf == nil {
		return stripNewline(fragment)
	}
	buf.Write(fragment)
	return stripNewline(buf.Bytes())
}

// handleEOFOrError returns the final assembled line on io.EOF (if any data
// was read) or propagates the error.
func (lr *Reader) handleEOFOrError(buf *bytes.Buffer, fragment []byte, err error) ([]byte, error) {
	if errors.Is(err, io.EOF) {
		if buf == nil && len(fragment) == 0 {
			return nil, io.EOF
		}
		return lr.finishLine(buf, fragment), nil
	}
	return nil, err
}

// discardLine reads and discards bytes until a newline or EOF.
func (lr *Reader) discardLine() {
	for {
		_, err := lr.r.ReadSlice('\n')
		if err == nil || !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}

// newlineOverhead returns the number of trailing newline bytes (\n or \r\n)
// considering both the accumulated buffer and the final fragment. This
// handles CRLF pairs split across buffer boundaries where \r is in buf
// and \n is the sole byte in fragment.
func newlineOverhead(buf *bytes.Buffer, fragment []byte) int {
	n := len(fragment)
	if n >= 2 && fragment[n-2] == '\r' && fragment[n-1] == '\n' {
		return 2
	}
	if n >= 1 && fragment[n-1] == '\n' {
		if buf != nil && buf.Len() > 0 && buf.Bytes()[buf.Len()-1] == '\r' {
			return 2
		}
		return 1
	}
	return 0
}

// stripNewline removes a trailing \n or \r\n.
func stripNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return b
}
