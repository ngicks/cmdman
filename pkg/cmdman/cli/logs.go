package cli

import (
	"fmt"
	"io"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/stdcopy"
)

// RenderLogs copies structured log lines to stdout and stderr.
func RenderLogs(stdout, stderr io.Writer, r logdriver.Reader) error {
	if r == nil {
		return fmt.Errorf("render logs: reader is nil")
	}
	return stdcopy.Copy(stdout, stderr, r)
}
