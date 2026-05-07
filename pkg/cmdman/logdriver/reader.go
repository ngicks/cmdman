package logdriver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// followPollInterval is how often we re-read the file in --follow mode.
const followPollInterval = 100 * time.Millisecond

// NewReader opens the persisted command output for the given driver. It
// returns a structured log-line reader. ReadLogLine returns io.EOF once
// the source is exhausted, unless follow is true, in which case it tails
// the source for new entries until ctx is cancelled.
//
// Drivers that don't retain output (currently LogDriverNone) return an
// error that callers can surface to the user.
func NewReader(
	ctx context.Context,
	driver store.LogDriver,
	path string,
	follow bool,
) (Reader, error) {
	switch driver {
	case store.LogDriverNone:
		return nil, fmt.Errorf("logdriver: driver %q does not retain logs", driver)
	case store.LogDriverK8sFile:
		return newK8sFileReader(ctx, path, follow)
	default:
		return nil, fmt.Errorf("logdriver: unknown driver %q", driver)
	}
}

type k8sFileReader struct {
	ctx     context.Context
	f       *os.File
	follow  bool
	rbuf    []byte
	partial []byte
}

func newK8sFileReader(ctx context.Context, path string, follow bool) (*k8sFileReader, error) {
	if path == "" {
		return nil, fmt.Errorf("logdriver: log file path is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("logdriver: open log file: %w", err)
	}
	return &k8sFileReader{
		ctx:    ctx,
		f:      f,
		follow: follow,
		rbuf:   make([]byte, 32*1024),
	}, nil
}

func (r *k8sFileReader) ReadLogLine() (LogLine, error) {
	for {
		i := bytes.IndexByte(r.partial, '\n')
		if i >= 0 {
			entry := r.partial[:i+1]
			r.partial = r.partial[i+1:]
			return parseK8sFileEntry(entry)
		}

		n, err := r.f.Read(r.rbuf)
		if n > 0 {
			r.partial = append(r.partial, r.rbuf[:n]...)
			continue
		}
		if err != nil && err != io.EOF {
			return LogLine{}, fmt.Errorf("logdriver: read log file: %w", err)
		}
		if !r.follow {
			return LogLine{}, io.EOF
		}
		select {
		case <-r.ctx.Done():
			return LogLine{}, io.EOF
		case <-time.After(followPollInterval):
		}
	}
}

func (r *k8sFileReader) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// parseK8sFileEntry splits a single k8s-file entry of the form
//
//	<RFC3339Nano> <stream> <F|P> <content>\n
//
// and returns the unframed log line. The trailing '\n' that the writer
// appends to partial (P) entries is stripped because it is framing rather
// than original output.
func parseK8sFileEntry(entry []byte) (LogLine, error) {
	sp1 := bytes.IndexByte(entry, ' ')
	if sp1 < 0 {
		return LogLine{}, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp2 := bytes.IndexByte(entry[sp1+1:], ' ')
	if sp2 < 0 {
		return LogLine{}, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp2 += sp1 + 1
	sp3 := bytes.IndexByte(entry[sp2+1:], ' ')
	if sp3 < 0 {
		return LogLine{}, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp3 += sp2 + 1
	ts, err := time.Parse(K8sLogTimeFormat, string(entry[:sp1]))
	if err != nil {
		return LogLine{}, fmt.Errorf("logdriver: malformed log entry timestamp: %w", err)
	}
	stream := Stream(entry[sp1+1 : sp2])
	tag := entry[sp2+1 : sp3]
	content := entry[sp3+1:]
	partial := bytes.Equal(tag, []byte(tagPartial))
	switch {
	case partial:
		content = bytes.TrimRight(content, "\n")
	case bytes.Equal(tag, []byte(tagFull)):
	default:
		return LogLine{}, fmt.Errorf("logdriver: malformed log entry tag %q", tag)
	}
	return LogLine{
		Time:    ts,
		Stream:  stream,
		Partial: partial,
		Line:    content,
	}, nil
}
