package stdcopy

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"gotest.tools/v3/assert"
)

func feed(lines []logdriver.Record) <-chan logdriver.Record {
	ch := make(chan logdriver.Record, len(lines))
	for _, rec := range lines {
		ch <- rec
	}
	close(ch)
	return ch
}

func TestCopyRoutesStdoutAndStderr(t *testing.T) {
	records := feed([]logdriver.Record{
		{Line: logdriver.LogLine{Stream: logdriver.StreamStdout, Line: []byte("out\n")}},
		{Line: logdriver.LogLine{Stream: logdriver.StreamStderr, Line: []byte("err\n")}},
		{Line: logdriver.LogLine{Line: []byte("default-out\n")}},
	})

	var stdout, stderr bytes.Buffer
	err := Copy(&stdout, &stderr, records)
	assert.NilError(t, err)
	assert.Equal(t, stdout.String(), "out\ndefault-out\n")
	assert.Equal(t, stderr.String(), "err\n")
}

func TestCopyRejectsUnknownStream(t *testing.T) {
	records := feed([]logdriver.Record{
		{Line: logdriver.LogLine{Stream: "unknown", Line: []byte("nope\n")}},
	})

	err := Copy(io.Discard, io.Discard, records)
	assert.ErrorContains(t, err, "unknown log stream")
}

func TestCopyReturnsRecordError(t *testing.T) {
	wantErr := fmt.Errorf("boom")
	records := feed([]logdriver.Record{
		{Line: logdriver.LogLine{Stream: logdriver.StreamStdout, Line: []byte("ok\n")}},
		{Err: wantErr},
		{Line: logdriver.LogLine{Stream: logdriver.StreamStdout, Line: []byte("unreached\n")}},
	})

	var stdout, stderr bytes.Buffer
	err := Copy(&stdout, &stderr, records)
	assert.ErrorIs(t, err, wantErr)
	assert.Equal(t, stdout.String(), "ok\n")
	assert.Equal(t, stderr.String(), "")
}

func TestCopyNilArgsError(t *testing.T) {
	records := feed(nil)
	assert.ErrorContains(t, Copy(nil, io.Discard, records), "stdout")
	records = feed(nil)
	assert.ErrorContains(t, Copy(io.Discard, nil, records), "stderr")
	assert.ErrorContains(t, Copy(io.Discard, io.Discard, nil), "records")
}
