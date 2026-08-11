package k8sfile

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

const tailChunkMultiple = 4

// SkipLines moves the offset by lines log lines from startOffset.
// lines > 0 moves forward, lines < 0 moves backward, and lines == 0 snaps to
// the start of the current line. It returns the byte offset of the target line.
func SkipLines(r io.ReaderAt, size, startOffset int64, lines int) (int64, error) {
	if size < 0 {
		return 0, fmt.Errorf("logdriver: negative file size %d", size)
	}
	if startOffset < 0 {
		startOffset = 0
	}
	if startOffset > size {
		startOffset = size
	}
	offset, err := snapLineStart(r, startOffset)
	if err != nil {
		return 0, err
	}
	switch {
	case lines == 0:
		return offset, nil
	case lines > 0:
		return skipLinesForward(r, size, offset, lines)
	default:
		return skipLinesBackward(r, offset, -lines)
	}
}

// FindLastLine returns the offset of the start of the last complete log line
// before size. A trailing partial entry without '\n' is ignored. Empty files
// and files with no complete lines return 0.
func FindLastLine(r io.ReaderAt, size int64) (int64, error) {
	if size < 0 {
		return 0, fmt.Errorf("logdriver: negative file size %d", size)
	}
	if size == 0 {
		return 0, nil
	}
	chunkSize := tailChunkSize()
	buf := make([]byte, chunkSize)
	pos := size
	sawTerminator := false
	for pos > 0 {
		start := max(pos-int64(chunkSize), 0)
		n := int(pos - start)
		if _, err := r.ReadAt(buf[:n], start); err != nil && err != io.EOF {
			return 0, fmt.Errorf("logdriver: read log tail: %w", err)
		}
		for i := n - 1; i >= 0; i-- {
			if buf[i] != '\n' {
				continue
			}
			if !sawTerminator {
				sawTerminator = true
				continue
			}
			return start + int64(i) + 1, nil
		}
		pos = start
	}
	if sawTerminator {
		return 0, nil
	}
	return 0, nil
}

func tailChunkSize() int {
	size := os.Getpagesize() * tailChunkMultiple
	if size <= 0 {
		return 16 * 1024
	}
	return size
}

func snapLineStart(r io.ReaderAt, offset int64) (int64, error) {
	if offset == 0 {
		return 0, nil
	}
	chunkSize := tailChunkSize()
	buf := make([]byte, chunkSize)
	pos := offset
	for pos > 0 {
		start := max(pos-int64(chunkSize), 0)
		n := int(pos - start)
		if _, err := r.ReadAt(buf[:n], start); err != nil && err != io.EOF {
			return 0, fmt.Errorf("logdriver: read log tail: %w", err)
		}
		if idx := bytes.LastIndexByte(buf[:n], '\n'); idx >= 0 {
			return start + int64(idx) + 1, nil
		}
		pos = start
	}
	return 0, nil
}

func skipLinesForward(r io.ReaderAt, size, offset int64, lines int) (int64, error) {
	chunkSize := tailChunkSize()
	buf := make([]byte, chunkSize)
	pos := offset
	for pos < size {
		n := chunkSize
		if remaining := size - pos; remaining < int64(n) {
			n = int(remaining)
		}
		if _, err := r.ReadAt(buf[:n], pos); err != nil && err != io.EOF {
			return 0, fmt.Errorf("logdriver: read log tail: %w", err)
		}
		for i, b := range buf[:n] {
			if b != '\n' {
				continue
			}
			lines--
			if lines == 0 {
				return pos + int64(i) + 1, nil
			}
		}
		pos += int64(n)
	}
	return size, nil
}

func skipLinesBackward(r io.ReaderAt, offset int64, lines int) (int64, error) {
	if offset == 0 {
		return 0, nil
	}
	chunkSize := tailChunkSize()
	buf := make([]byte, chunkSize)
	pos := offset - 1
	for pos > 0 {
		start := max(pos-int64(chunkSize), 0)
		n := int(pos - start)
		if _, err := r.ReadAt(buf[:n], start); err != nil && err != io.EOF {
			return 0, fmt.Errorf("logdriver: read log tail: %w", err)
		}
		for i := n - 1; i >= 0; i-- {
			if buf[i] != '\n' {
				continue
			}
			lines--
			if lines == 0 {
				return start + int64(i) + 1, nil
			}
		}
		pos = start
	}
	return 0, nil
}
