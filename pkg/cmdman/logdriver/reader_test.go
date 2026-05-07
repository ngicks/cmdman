package logdriver

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"gotest.tools/v3/assert"
)

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(body), 0o644))
}

func readAll(t *testing.T, r Reader) []LogLine {
	t.Helper()
	var lines []LogLine
	for {
		line, err := r.ReadLogLine()
		if err == io.EOF {
			return lines
		}
		assert.NilError(t, err)
		lines = append(lines, line)
	}
}

func readAllBytes(t *testing.T, r Reader) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, line := range readAll(t, r) {
		_, err := buf.Write(line.Line)
		assert.NilError(t, err)
	}
	return buf.Bytes()
}

func TestNewReader_NoneDriverErrors(t *testing.T) {
	_, err := NewReader(
		context.Background(),
		store.LogDriverNone,
		"/tmp/whatever",
		false,
	)
	assert.ErrorContains(t, err, "does not retain logs")
}

func TestNewReader_UnknownDriverErrors(t *testing.T) {
	_, err := NewReader(context.Background(), "bogus", "/tmp/whatever", false)
	assert.ErrorContains(t, err, "unknown driver")
}

func TestNewReader_K8sFile_FullLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.223758260Z stdout F line1\n"+
			"2023-08-07T19:56:34.223758260Z stdout F line2\n",
	)

	r, err := NewReader(context.Background(), store.LogDriverK8sFile, path, false)
	assert.NilError(t, err)
	defer r.Close()

	lines := readAll(t, r)
	assert.Equal(t, len(lines), 2)
	assert.Equal(t, lines[0].Stream, StreamStdout)
	assert.Equal(t, string(lines[0].Line), "line1\n")
	assert.Equal(t, string(lines[1].Line), "line2\n")
}

func TestNewReader_K8sFile_JoinsPartials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	// "lin" was a partial; "e2\n" completed the line. Expected reconstruction: "line2\n".
	writeFixture(t, path,
		"2023-08-07T19:56:34.223758260Z stdout P lin\n"+
			"2023-08-07T19:56:34.223758260Z stdout F e2\n",
	)

	r, err := NewReader(context.Background(), store.LogDriverK8sFile, path, false)
	assert.NilError(t, err)
	defer r.Close()
	assert.Equal(t, string(readAllBytes(t, r)), "line2\n")
}

func TestNewReader_K8sFile_TrailingPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.223758260Z stdout F complete\n"+
			"2023-08-07T19:56:34.223758260Z stdout P rest\n",
	)

	r, err := NewReader(context.Background(), store.LogDriverK8sFile, path, false)
	assert.NilError(t, err)
	defer r.Close()
	assert.Equal(t, string(readAllBytes(t, r)), "complete\nrest")
}

func TestNewReader_K8sFile_MissingFileErrors(t *testing.T) {
	_, err := NewReader(
		context.Background(),
		store.LogDriverK8sFile,
		"/no/such/path",
		false,
	)
	assert.ErrorContains(t, err, "open log file")
}

func TestNewReader_K8sFile_FollowReadsAppendedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "2023-08-07T19:56:34.223758260Z stdout F first\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() {
		r, err := NewReader(ctx, store.LogDriverK8sFile, path, true)
		if err != nil {
			done <- err
			return
		}
		defer r.Close()
		for {
			line, err := r.ReadLogLine()
			if err == io.EOF {
				done <- nil
				return
			}
			if err != nil {
				done <- err
				return
			}
			_, err = buf.Write(line.Line)
			if err != nil {
				done <- err
				return
			}
		}
	}()

	// Append a second entry while the reader is following.
	time.Sleep(followPollInterval + 50*time.Millisecond)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	assert.NilError(t, err)
	_, err = f.WriteString("2023-08-07T19:56:34.223758260Z stdout F second\n")
	assert.NilError(t, err)
	assert.NilError(t, f.Close())

	// Wait for the appended content to surface, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if bytes.Contains(buf.Bytes(), []byte("second")) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	assert.NilError(t, <-done)
	assert.Equal(t, buf.String(), "first\nsecond\n")
}

func TestNewReader_K8sFile_FollowExitsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "2023-08-07T19:56:34.223758260Z stdout F only\n")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	r, err := NewReader(ctx, store.LogDriverK8sFile, path, true)
	assert.NilError(t, err)
	defer r.Close()
	assert.Equal(t, string(readAllBytes(t, r)), "only\n")
}

func TestNewReader_K8sFile_MalformedLineErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "not-a-valid-entry\n")

	r, err := NewReader(context.Background(), store.LogDriverK8sFile, path, false)
	assert.NilError(t, err)
	defer r.Close()
	_, err = r.ReadLogLine()
	assert.ErrorContains(t, err, "malformed")
}

func TestNewReader_K8sFile_StderrLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "2023-08-07T19:56:34.223758260Z stderr F problem\n")

	r, err := NewReader(context.Background(), store.LogDriverK8sFile, path, false)
	assert.NilError(t, err)
	defer r.Close()

	line, err := r.ReadLogLine()
	assert.NilError(t, err)
	assert.Equal(t, line.Stream, StreamStderr)
	assert.Equal(t, string(line.Line), "problem\n")
}
