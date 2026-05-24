// Package k8sfile implements podman's k8s-file log format.
package k8sfile

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// DefaultLogFileName is the default per-command log filename for k8s-file.
const DefaultLogFileName = "console.log"

// K8sLogTimeFormat matches podman's libpod/logs.LogTimeFormat for
// byte-level compatibility.
const K8sLogTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

const (
	logOptPath    = "path"
	logOptMaxSize = "max-size"
	logOptMaxFile = "max-file"
	tagFull       = "F"
	tagPartial    = "P"
	tagRotation   = "R"
)

func init() {
	logdriver.Register("k8s-file", Driver{})
}

// Driver constructs k8s-file log writers and readers.
type Driver struct{}

// Offset identifies a position in the active k8s-file log.
type Offset struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

func (o Offset) MarshalBinary() ([]byte, error) {
	return json.Marshal(o)
}

func (o *Offset) UnmarshalBinary(b []byte) error {
	return json.Unmarshal(b, o)
}

// NewWriter opens the active k8s-file log.
func (Driver) NewWriter(
	_ context.Context,
	dir string,
	opts map[string]string,
) (logdriver.Writer, error) {
	path, err := resolvePath(dir, opts)
	if err != nil {
		return nil, err
	}
	maxSize, err := parseLogMaxSizeOption(opts)
	if err != nil {
		return nil, fmt.Errorf("logdriver: k8s-file: %s: %w", logOptMaxSize, err)
	}
	maxFile, err := parseLogMaxFileOption(opts)
	if err != nil {
		return nil, fmt.Errorf("logdriver: k8s-file: %s: %w", logOptMaxFile, err)
	}
	return newWriter(path, maxSize, maxFile)
}

func resolvePath(dir string, opts map[string]string) (string, error) {
	if p := opts[logOptPath]; p != "" {
		return p, nil
	}
	if dir != "" {
		return filepath.Join(dir, DefaultLogFileName), nil
	}
	return "", fmt.Errorf("logdriver: k8s-file path is empty")
}

// Writer writes podman's k8s-file format, where each entry is:
//
//	<timestamp> <stream> <F|P> <content>\n
type Writer struct {
	f       *os.File
	bw      *bufio.Writer
	now     func() time.Time
	path    string
	maxSize int64
	maxFile int
	written int64
}

func newWriter(path string, maxSize int64, maxFile int) (*Writer, error) {
	if path == "" {
		return nil, fmt.Errorf("logdriver: k8s-file path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("logdriver: create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		return nil, fmt.Errorf("logdriver: open log file: %w", err)
	}
	stat, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("logdriver: stat log file: %w", err)
	}
	return &Writer{
		f:       f,
		bw:      bufio.NewWriter(f),
		now:     time.Now,
		path:    path,
		maxSize: maxSize,
		maxFile: maxFile,
		written: stat.Size(),
	}, nil
}

func (w *Writer) WriteLogLine(line logdriver.LogLine) error {
	if len(line.Line) == 0 {
		return nil
	}
	ts := line.Time
	if ts.IsZero() {
		ts = w.now()
	}
	stream := line.Stream
	if stream == "" {
		stream = logdriver.StreamStdout
	}
	tag := tagFull
	if line.Partial {
		tag = tagPartial
	}

	tsText := ts.Format(K8sLogTimeFormat)
	entrySize := int64(len(tsText) + 1 + len(stream) + 1 + len(tag) + 1 + len(line.Line))
	if line.Partial {
		entrySize++
	}

	if w.maxSize > 0 && w.written+entrySize >= w.maxSize {
		if err := w.rotate(); err != nil {
			return err
		}
	}

	if _, err := w.bw.WriteString(tsText); err != nil {
		return err
	}
	if err := w.bw.WriteByte(' '); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(string(stream)); err != nil {
		return err
	}
	if err := w.bw.WriteByte(' '); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(tag); err != nil {
		return err
	}
	if err := w.bw.WriteByte(' '); err != nil {
		return err
	}
	if _, err := w.bw.Write(line.Line); err != nil {
		return err
	}
	if line.Partial {
		if err := w.bw.WriteByte('\n'); err != nil {
			return err
		}
	}
	w.written += entrySize
	return w.bw.Flush()
}

// Write preserves byte-stream compatibility for package-local tests and
// adapters. New callers should use WriteLogLine or logdriver.NewStreamWriter.
func (w *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	consumed := 0
	for _, line := range logdriver.SplitLogLines(w.now(), logdriver.StreamStdout, p) {
		if err := w.WriteLogLine(line); err != nil {
			return consumed, err
		}
		consumed += len(line.Line)
	}
	return consumed, nil
}

func (w *Writer) CurrentOffset() any {
	if w == nil {
		return nil
	}
	if w.bw != nil {
		_ = w.bw.Flush()
	}
	return Offset{
		Path:  w.path,
		Bytes: w.written,
	}
}

func (w *Writer) rotate() error {
	if err := w.writeRotationMarker(); err != nil {
		return err
	}
	if w.maxFile <= 1 {
		if err := w.f.Close(); err != nil {
			return fmt.Errorf("logdriver: close before rotate: %w", err)
		}
		w.f = nil
		if err := os.Remove(w.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("logdriver: remove log file before rotate: %w", err)
		}
		f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
		if err != nil {
			return fmt.Errorf("logdriver: reopen log file after rotate: %w", err)
		}
		w.f = f
		w.bw.Reset(f)
		w.written = 0
		return nil
	}
	if err := w.f.Close(); err != nil {
		return fmt.Errorf("logdriver: close before rotate: %w", err)
	}
	w.f = nil
	for i := w.maxFile - 2; i >= 0; i-- {
		src := w.path
		if i > 0 {
			src = fmt.Sprintf("%s.%d", w.path, i)
		}
		dst := fmt.Sprintf("%s.%d", w.path, i+1)
		if err := os.Rename(src, dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("logdriver: rotate %s -> %s: %w", src, dst, err)
		}
	}
	f, err := os.OpenFile(w.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o640)
	if err != nil {
		return fmt.Errorf("logdriver: reopen log file after rotate: %w", err)
	}
	w.f = f
	w.bw.Reset(f)
	w.written = 0
	return nil
}

func (w *Writer) writeRotationMarker() error {
	tsText := w.now().Format(K8sLogTimeFormat)
	if _, err := w.bw.WriteString(tsText); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(" stdout "); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(tagRotation); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(" -\n"); err != nil {
		return err
	}
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("logdriver: flush rotation marker: %w", err)
	}
	w.written += int64(len(tsText) + len(" stdout ") + len(tagRotation) + len(" -\n"))
	return nil
}

func (w *Writer) Close() error {
	if w.f == nil {
		return nil
	}
	flushErr := w.bw.Flush()
	closeErr := w.f.Close()
	w.f = nil
	w.bw = nil
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}
