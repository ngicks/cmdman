// Package stdcopy renders structured log lines back to stdout and stderr
// byte streams.
package stdcopy

import (
	"fmt"
	"io"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// Copy reads log lines from r and writes their bytes to stdout or stderr
// based on each line's Stream. It stops cleanly at io.EOF.
func Copy(stdout, stderr io.Writer, r logdriver.Reader) error {
	if stdout == nil {
		return fmt.Errorf("stdcopy: stdout writer is nil")
	}
	if stderr == nil {
		return fmt.Errorf("stdcopy: stderr writer is nil")
	}
	for {
		line, err := r.ReadLogLine()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		w := stdout
		switch line.Stream {
		case logdriver.StreamStdout, "":
		case logdriver.StreamStderr:
			w = stderr
		default:
			return fmt.Errorf("stdcopy: unknown log stream %q", line.Stream)
		}
		if _, err := w.Write(line.Line); err != nil {
			return err
		}
	}
}
