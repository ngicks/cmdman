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

// New constructs a Writer for the configured driver. path is the on-disk
// log file path; it is required for file-based drivers and ignored for
// others. The returned Writer is owned by a single producer goroutine
// and is not safe for concurrent calls.
func New(driver store.LogDriver, path string) (io.WriteCloser, error) {
	switch driver {
	case store.LogDriverNone:
		return noopWriter{}, nil
	case store.LogDriverK8sFile:
		return newK8sFileWriter(path)
	default:
		return nil, fmt.Errorf("logdriver: unknown driver %q", driver)
	}
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
type k8sFileWriter struct {
	f   *os.File
	bw  *bufio.Writer
	now func() time.Time
}

func newK8sFileWriter(path string) (*k8sFileWriter, error) {
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
	return &k8sFileWriter{
		f:   f,
		bw:  bufio.NewWriter(f),
		now: time.Now,
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
		consumed += len(line)
		p = p[len(line):]
	}
	if err := w.bw.Flush(); err != nil {
		return consumed, err
	}
	return consumed, nil
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
