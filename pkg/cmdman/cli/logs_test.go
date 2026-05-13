package cli

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"gotest.tools/v3/assert"
)

func TestRenderLogsRoutesStdoutAndStderr(t *testing.T) {
	records := make(chan logdriver.Record, 3)
	records <- logdriver.Record{
		Line: logdriver.LogLine{Stream: logdriver.StreamStdout, Line: []byte("out\n")},
	}
	records <- logdriver.Record{
		Line: logdriver.LogLine{Stream: logdriver.StreamStderr, Line: []byte("err\n")},
	}
	records <- logdriver.Record{Line: logdriver.LogLine{Line: []byte("default-out\n")}}
	close(records)

	var stdout, stderr bytes.Buffer
	err := RenderLogs(&stdout, &stderr, records)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "out\ndefault-out\n")
	assert.Equal(t, stderr.String(), "err\n")
}

func TestRenderLogsRejectsUnknownStream(t *testing.T) {
	records := make(chan logdriver.Record, 1)
	records <- logdriver.Record{Line: logdriver.LogLine{Stream: "unknown", Line: []byte("nope\n")}}
	close(records)

	err := RenderLogs(io.Discard, io.Discard, records)
	assert.ErrorContains(t, err, "unknown log stream")
}

func TestRenderLogsReturnsRecordError(t *testing.T) {
	records := make(chan logdriver.Record, 1)
	records <- logdriver.Record{Err: fmt.Errorf("boom")}
	close(records)

	err := RenderLogs(io.Discard, io.Discard, records)
	assert.ErrorContains(t, err, "boom")
}
