package cli

import (
	"io"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/stdcopy"
)

// RenderLogs copies structured log records to stdout and stderr.
func RenderLogs(stdout, stderr io.Writer, records <-chan logdriver.Record) error {
	return stdcopy.Copy(stdout, stderr, records)
}
