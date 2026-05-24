package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"gotest.tools/v3/assert"
)

func TestPrintComposeLogsPrefixesTimeAndCommand(t *testing.T) {
	ts := time.Date(2026, 5, 24, 1, 2, 3, 456789000, time.UTC)
	msgs := make(chan compose.LogMessage, 1)
	msgs <- compose.LogMessage{
		Command: "alpha",
		Record: logdriver.Record{
			Line: logdriver.LogLine{
				Time:   ts,
				Stream: logdriver.StreamStdout,
				Line:   []byte("line-from-alpha\n"),
			},
		},
	}
	close(msgs)

	var stdout, stderr bytes.Buffer
	err := PrintComposeLogs(&stdout, &stderr, msgs)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "2026-05-24T01:02:03.456789Z alpha|line-from-alpha\n")
	assert.Equal(t, stderr.String(), "")
}
