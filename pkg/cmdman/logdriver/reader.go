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

// NewReader writes the persisted command output for the given driver to
// w. It returns once the source is exhausted, unless follow is true, in
// which case it tails the source for new entries until ctx is cancelled
// or w returns an error.
//
// Drivers that don't retain output (currently LogDriverNone) return an
// error that callers can surface to the user.
func NewReader(
	ctx context.Context,
	driver store.LogDriver,
	path string,
	w io.Writer,
	follow bool,
) error {
	switch driver {
	case store.LogDriverNone:
		return fmt.Errorf("logdriver: driver %q does not retain logs", driver)
	case store.LogDriverK8sFile:
		return readK8sFile(ctx, path, w, follow)
	default:
		return fmt.Errorf("logdriver: unknown driver %q", driver)
	}
}

func readK8sFile(ctx context.Context, path string, w io.Writer, follow bool) error {
	if path == "" {
		return fmt.Errorf("logdriver: log file path is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("logdriver: open log file: %w", err)
	}
	defer f.Close()

	rbuf := make([]byte, 32*1024)
	var partial []byte
	for {
		// Drain every complete entry currently buffered.
		for {
			i := bytes.IndexByte(partial, '\n')
			if i < 0 {
				break
			}
			entry := partial[:i+1]
			partial = partial[i+1:]
			content, perr := parseK8sFileEntry(entry)
			if perr != nil {
				return perr
			}
			if _, werr := w.Write(content); werr != nil {
				return werr
			}
		}

		n, err := f.Read(rbuf)
		if n > 0 {
			partial = append(partial, rbuf[:n]...)
			continue
		}
		if err != nil && err != io.EOF {
			return fmt.Errorf("logdriver: read log file: %w", err)
		}
		if !follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(followPollInterval):
		}
	}
}

// parseK8sFileEntry splits a single k8s-file entry of the form
//
//	<RFC3339Nano> <stream> <F|P> <content>\n
//
// and returns the unframed content. The trailing '\n' that the writer
// appends to partial (P) entries is stripped because it is framing
// rather than original output.
func parseK8sFileEntry(entry []byte) ([]byte, error) {
	sp1 := bytes.IndexByte(entry, ' ')
	if sp1 < 0 {
		return nil, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp2 := bytes.IndexByte(entry[sp1+1:], ' ')
	if sp2 < 0 {
		return nil, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp2 += sp1 + 1
	sp3 := bytes.IndexByte(entry[sp2+1:], ' ')
	if sp3 < 0 {
		return nil, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp3 += sp2 + 1
	tag := entry[sp2+1 : sp3]
	content := entry[sp3+1:]
	if bytes.Equal(tag, []byte(tagPartial)) {
		content = bytes.TrimRight(content, "\n")
	}
	return content, nil
}
