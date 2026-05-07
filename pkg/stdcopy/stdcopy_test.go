package stdcopy

import (
	"bytes"
	"io"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"gotest.tools/v3/assert"
)

type sliceReader struct {
	lines []logdriver.LogLine
}

func (r *sliceReader) ReadLogLine() (logdriver.LogLine, error) {
	if len(r.lines) == 0 {
		return logdriver.LogLine{}, io.EOF
	}
	line := r.lines[0]
	r.lines = r.lines[1:]
	return line, nil
}

func (r *sliceReader) Close() error { return nil }

func TestCopyRoutesStdoutAndStderr(t *testing.T) {
	r := &sliceReader{
		lines: []logdriver.LogLine{
			{Stream: logdriver.StreamStdout, Line: []byte("out\n")},
			{Stream: logdriver.StreamStderr, Line: []byte("err\n")},
			{Line: []byte("default-out\n")},
		},
	}
	var stdout, stderr bytes.Buffer

	err := Copy(&stdout, &stderr, r)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "out\ndefault-out\n")
	assert.Equal(t, stderr.String(), "err\n")
}

func TestCopyRejectsUnknownStream(t *testing.T) {
	r := &sliceReader{
		lines: []logdriver.LogLine{
			{Stream: "unknown", Line: []byte("nope\n")},
		},
	}

	err := Copy(io.Discard, io.Discard, r)
	assert.ErrorContains(t, err, "unknown log stream")
}
