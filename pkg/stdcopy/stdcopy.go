// Package stdcopy renders structured log records back to stdout and stderr
// byte streams.
package stdcopy

import (
	"fmt"
	"io"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// Copy consumes records and writes each line's bytes to stdout or stderr
// based on its Stream. It returns when the records channel is closed, when
// a record carries a terminal error, or when a write fails.
func Copy(stdout, stderr io.Writer, records <-chan logdriver.Record) error {
	if stdout == nil {
		return fmt.Errorf("stdcopy: stdout writer is nil")
	}
	if stderr == nil {
		return fmt.Errorf("stdcopy: stderr writer is nil")
	}
	if records == nil {
		return fmt.Errorf("stdcopy: records channel is nil")
	}
	for rec := range records {
		if rec.Err != nil {
			return rec.Err
		}
		w := stdout
		switch rec.Line.Stream {
		case logdriver.StreamStdout, "":
		case logdriver.StreamStderr:
			w = stderr
		default:
			return fmt.Errorf("stdcopy: unknown log stream %q", rec.Line.Stream)
		}
		if _, err := w.Write(rec.Line.Line); err != nil {
			return err
		}
	}
	return nil
}
