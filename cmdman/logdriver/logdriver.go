// Package logdriver defines the public log driver API.
package logdriver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// Stream names the output stream a log line came from.
type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

// LogLine is the structured log record exchanged with log drivers.
type LogLine struct {
	Time    time.Time
	Stream  Stream
	Partial bool
	Line    []byte
}

// Writer persists structured log lines.
type Writer interface {
	WriteLogLine(LogLine) error
	Close() error
}

// OffsetWriter exposes a driver-specific current write offset. The value is
// opaque outside the concrete driver.
type OffsetWriter interface {
	CurrentOffset() any
}

// Record is one item emitted by a Reader. Err carries fatal read errors in
// stream order; the reader closes its channel after emitting a terminal error.
type Record struct {
	Line   LogLine
	Err    error
	Offset any
}

// Reader returns structured log records over one channel.
type Reader interface {
	Records() <-chan Record
	Close() error
}

// Driver constructs log writers and readers for one storage format.
type Driver interface {
	NewWriter(ctx context.Context, dir string, opts map[string]string) (Writer, error)
	NewReader(
		ctx context.Context,
		dir string,
		opts map[string]string,
		ro ReaderOption,
	) (Reader, error)
}

// Options is a driver-specific bag of raw KEY=VALUE strings. Each driver
// reads only the keys it understands and parses the values itself.
type Options map[string]string

var (
	driversMu sync.RWMutex
	drivers   = map[string]Driver{}
)

func init() {
	Register(string(DriverNone), noneDriver{})
}

// Register adds a log driver implementation by name.
func Register(name string, d Driver) {
	driversMu.Lock()
	defer driversMu.Unlock()
	if name == "" {
		panic("logdriver: empty driver name")
	}
	if d == nil {
		panic("logdriver: nil driver")
	}
	if _, ok := drivers[name]; ok {
		panic(fmt.Sprintf("logdriver: driver %q already registered", name))
	}
	drivers[name] = d
}

func lookup(name string) (Driver, error) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("logdriver: unknown driver %q", name)
	}
	return d, nil
}

// NewWriter constructs a Writer through the registered driver.
func NewWriter(ctx context.Context, driver, dir string, opts map[string]string) (Writer, error) {
	d, err := lookup(driver)
	if err != nil {
		return nil, err
	}
	return d.NewWriter(ctx, dir, opts)
}

// NewReader constructs a Reader through the registered driver.
func NewReader(
	ctx context.Context,
	driver string,
	dir string,
	opts map[string]string,
	ro ReaderOption,
) (Reader, error) {
	if err := ro.Validate(); err != nil {
		return nil, err
	}
	d, err := lookup(driver)
	if err != nil {
		return nil, err
	}
	return d.NewReader(ctx, dir, opts, ro)
}

type noneDriver struct{}

func (noneDriver) NewWriter(context.Context, string, map[string]string) (Writer, error) {
	return noopWriter{}, nil
}

func (noneDriver) NewReader(
	context.Context,
	string,
	map[string]string,
	ReaderOption,
) (Reader, error) {
	// The none driver intentionally has no readable storage. Follow mode is
	// handled by monitor live output in higher layers when supported.
	return nil, fmt.Errorf("logdriver: driver %q does not retain logs", "none")
}

type noopWriter struct{}

func (noopWriter) WriteLogLine(LogLine) error { return nil }
func (noopWriter) Close() error               { return nil }

// SplitLogLines splits a byte stream write into log-line chunks for a stream.
// Chunks that end in '\n' are full lines; the final chunk without '\n' is
// partial.
func SplitLogLines(ts time.Time, stream Stream, p []byte) []LogLine {
	var lines []LogLine
	for len(p) > 0 {
		nl := bytes.IndexByte(p, '\n')
		var line []byte
		var partial bool
		if nl < 0 {
			line = p
			partial = true
		} else {
			line = p[:nl+1]
		}
		lines = append(lines, LogLine{
			Time:    ts,
			Stream:  stream,
			Partial: partial,
			Line:    line,
		})
		p = p[len(line):]
	}
	return lines
}

// SplitLogLines_LegacyShape preserves the pre-Stage-4 helper shape for tests
// and adapters that fill Time and Stream separately.
//
// Deprecated: use SplitLogLines(ts, stream, p).
func SplitLogLines_LegacyShape(p []byte) []LogLine {
	return SplitLogLines(time.Time{}, "", p)
}

// NewStreamWriter adapts byte writes for a single stream into structured
// log lines. It is useful for execution paths that already expose stdout
// and stderr as byte streams. The returned adapter does not own w; callers
// must close w separately after all stream adapters stop writing.
func NewStreamWriter(w Writer, stream Stream) io.WriteCloser {
	return &streamWriter{
		w:      w,
		stream: stream,
		now:    time.Now,
	}
}

type streamWriter struct {
	w      Writer
	stream Stream
	now    func() time.Time
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	consumed := 0
	for _, line := range SplitLogLines(w.now(), w.stream, p) {
		if err := w.w.WriteLogLine(line); err != nil {
			return consumed, err
		}
		consumed += len(line.Line)
	}
	return consumed, nil
}

func (w *streamWriter) Close() error {
	return nil
}
