// Package logdriver writes command output to disk in the formats used
// by upstream container runtimes. Currently it supports podman's
// "k8s-file" format and a "none" driver that drops everything.
package logdriver

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// K8sLogTimeFormat matches podman's libpod/logs.LogTimeFormat for
// byte-level compatibility.
const K8sLogTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

const (
	streamStdout = "stdout"
	tagFull      = "F"
	tagPartial   = "P"
)

// Options is a driver-specific bag of raw KEY=VALUE strings, mirroring
// the on-disk LogOpts vocabulary defined in the store package. Each
// driver reads only the keys it understands and parses the values
// itself; unrecognized keys are ignored, missing keys mean "use the
// default."
//
// Keys recognized by built-in drivers:
//
//   - store.LogOptPath    — log file path; required by file-based
//     drivers. Callers are expected to pre-resolve any default (the
//     store package's CommandConfigJSON.LogPath does this).
//   - store.LogOptMaxSize — k8s-file: cap in bytes for the active file;
//     parsed via store.ParseLogMaxSize. Rotation triggers when a write
//     would push the active file to or past this value.
//   - store.LogOptMaxFile — k8s-file: total files kept (active +
//     archives); parsed via store.ParseLogMaxFile. With <= 1 the active
//     file is truncated in place instead of being rotated.
//
// New drivers can introduce their own keys without changing this type.
type Options map[string]string

// New constructs a Writer for the configured driver. The returned
// Writer is owned by a single producer goroutine and is not safe for
// concurrent calls. Drivers that do not retain output (currently
// LogDriverNone) ignore opts.
func New(driver store.LogDriver, opts Options) (io.WriteCloser, error) {
	switch driver {
	case store.LogDriverNone:
		return noopWriter{}, nil
	case store.LogDriverK8sFile:
		return newK8sFileWriterFromOpts(opts)
	default:
		return nil, fmt.Errorf("logdriver: unknown driver %q", driver)
	}
}

func newK8sFileWriterFromOpts(opts Options) (*k8sFileWriter, error) {
	maxSize, err := store.ParseLogMaxSize(opts[store.LogOptMaxSize])
	if err != nil {
		return nil, fmt.Errorf("logdriver: k8s-file: %s: %w", store.LogOptMaxSize, err)
	}
	maxFile, err := store.ParseLogMaxFile(opts[store.LogOptMaxFile])
	if err != nil {
		return nil, fmt.Errorf("logdriver: k8s-file: %s: %w", store.LogOptMaxFile, err)
	}
	return newK8sFileWriter(opts[store.LogOptPath], maxSize, maxFile)
}

type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (noopWriter) Close() error                { return nil }

// k8sFileWriter writes podman's k8s-file format, where each entry is:
//
//	<timestamp> <stream> <F|P> <content>\n
//
// A line that ends with '\n' in the source is tagged "F" (full) and the
// source newline serves as the entry terminator. A trailing chunk
// without a newline is tagged "P" (partial) and we append our own '\n'
// to terminate the entry.
//
// When maxSize is positive, the writer rotates before any entry whose
// serialized form would push the active file's byte count to or past
// the cap:
//
//   - With maxFile <= 1 the active file is truncated in place and old
//     entries are dropped.
//   - With maxFile >= 2 the rename chain "<path> -> <path>.1, .1 -> .2,
//     ... .(N-2) -> .(N-1)" is shifted; .(N-1) is overwritten by the
//     rename and effectively removed. A fresh active file is opened.
//
// Single entries larger than the cap are written in full after rotation,
// matching podman's behavior.
type k8sFileWriter struct {
	f       *os.File
	bw      *bufio.Writer
	now     func() time.Time
	path    string
	maxSize int64
	maxFile int
	written int64
}

func newK8sFileWriter(path string, maxSize int64, maxFile int) (*k8sFileWriter, error) {
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
	return &k8sFileWriter{
		f:       f,
		bw:      bufio.NewWriter(f),
		now:     time.Now,
		path:    path,
		maxSize: maxSize,
		maxFile: maxFile,
		written: stat.Size(),
	}, nil
}

func (w *k8sFileWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ts := w.now().Format(K8sLogTimeFormat)
	consumed := 0
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

		tag := tagFull
		if partial {
			tag = tagPartial
		}
		// <ts> <stream> <tag> <line>[\n]
		entrySize := int64(len(ts) + 1 + len(streamStdout) + 1 + len(tag) + 1 + len(line))
		if partial {
			entrySize++
		}

		if w.maxSize > 0 && w.written+entrySize >= w.maxSize {
			if err := w.rotate(); err != nil {
				return consumed, err
			}
		}

		if _, err := w.bw.WriteString(ts); err != nil {
			return consumed, err
		}
		if err := w.bw.WriteByte(' '); err != nil {
			return consumed, err
		}
		if _, err := w.bw.WriteString(streamStdout); err != nil {
			return consumed, err
		}
		if err := w.bw.WriteByte(' '); err != nil {
			return consumed, err
		}
		if _, err := w.bw.WriteString(tag); err != nil {
			return consumed, err
		}
		if err := w.bw.WriteByte(' '); err != nil {
			return consumed, err
		}
		if _, err := w.bw.Write(line); err != nil {
			return consumed, err
		}
		if partial {
			if err := w.bw.WriteByte('\n'); err != nil {
				return consumed, err
			}
		}
		w.written += entrySize
		consumed += len(line)
		p = p[len(line):]
	}
	if err := w.bw.Flush(); err != nil {
		return consumed, err
	}
	return consumed, nil
}

// rotate flushes any buffered output, then either truncates the active
// file in place (maxFile <= 1) or shifts the archive chain by one slot
// and opens a fresh active file (maxFile >= 2). The shift overwrites
// the would-be-(maxFile)th archive, effectively dropping the oldest
// retained entries.
func (w *k8sFileWriter) rotate() error {
	if err := w.bw.Flush(); err != nil {
		return fmt.Errorf("logdriver: flush before rotate: %w", err)
	}
	if w.maxFile <= 1 {
		if err := w.f.Truncate(0); err != nil {
			return fmt.Errorf("logdriver: truncate log file: %w", err)
		}
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
		if err := os.Rename(src, dst); err != nil && !os.IsNotExist(err) {
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

func (w *k8sFileWriter) Close() error {
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
