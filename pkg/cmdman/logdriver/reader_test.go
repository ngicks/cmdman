package logdriver_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver/k8sfile"
	"gotest.tools/v3/assert"
)

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(body), 0o644))
}

// syncBuffer is a bytes.Buffer guarded by a mutex. The follow tests have a
// background reader goroutine append here while the test goroutine polls and
// then inspects the contents; without the lock those concurrent accesses are a
// data race (the -race detector fails them, and the read can observe torn state
// even without it).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) contains(sub string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Contains(b.buf.Bytes(), []byte(sub))
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, value)
	assert.NilError(t, err)
	return ts
}

func readAll(t *testing.T, r logdriver.Reader) []logdriver.LogLine {
	t.Helper()
	var lines []logdriver.LogLine
	for rec := range r.Records() {
		assert.NilError(t, rec.Err)
		lines = append(lines, rec.Line)
	}
	return lines
}

func readAllBytes(t *testing.T, r logdriver.Reader) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, line := range readAll(t, r) {
		_, err := buf.Write(line.Line)
		assert.NilError(t, err)
	}
	return buf.Bytes()
}

func TestNewReader_NoneDriverErrors(t *testing.T) {
	_, err := logdriver.NewReader(
		context.Background(),
		"none",
		"/tmp",
		map[string]string{"path": "/tmp/whatever"},
		logdriver.ReaderOption{},
	)
	assert.ErrorContains(t, err, "does not retain logs")
}

func TestNewReader_UnknownDriverErrors(t *testing.T) {
	_, err := logdriver.NewReader(
		context.Background(),
		"bogus",
		"/tmp",
		map[string]string{"path": "/tmp/whatever"},
		logdriver.ReaderOption{},
	)
	assert.ErrorContains(t, err, "unknown driver")
}

func TestNewReader_K8sFile_FullLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.223758260Z stdout F line1\n"+
			"2023-08-07T19:56:34.223758260Z stdout F line2\n",
	)

	r, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{},
	)
	assert.NilError(t, err)
	defer r.Close()

	lines := readAll(t, r)
	assert.Equal(t, len(lines), 2)
	assert.Equal(t, lines[0].Stream, logdriver.StreamStdout)
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

	r, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{},
	)
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

	r, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{},
	)
	assert.NilError(t, err)
	defer r.Close()
	assert.Equal(t, string(readAllBytes(t, r)), "complete\nrest")
}

func TestNewReader_K8sFile_MissingFileErrors(t *testing.T) {
	_, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		"",
		map[string]string{"path": "/no/such/path"},
		logdriver.ReaderOption{},
	)
	assert.ErrorContains(t, err, "open log file")
}

func TestNewReader_K8sFile_FollowReadsAppendedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "2023-08-07T19:56:34.223758260Z stdout F first\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var buf syncBuffer
	done := make(chan error, 1)
	go func() {
		r, err := logdriver.NewReader(
			ctx,
			"k8s-file",
			dir,
			map[string]string{"path": path},
			logdriver.ReaderOption{Follow: true},
		)
		if err != nil {
			done <- err
			return
		}
		defer r.Close()
		for rec := range r.Records() {
			if rec.Err != nil {
				done <- rec.Err
				return
			}
			_, err = buf.Write(rec.Line.Line)
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	// Append a second entry while the reader is following.
	time.Sleep(k8sfile.FollowPollInterval + 50*time.Millisecond)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	assert.NilError(t, err)
	_, err = f.WriteString("2023-08-07T19:56:34.223758260Z stdout F second\n")
	assert.NilError(t, err)
	assert.NilError(t, f.Close())

	// Wait for the appended content to surface, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if buf.contains("second") {
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

	r, err := logdriver.NewReader(
		ctx,
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{Follow: true},
	)
	assert.NilError(t, err)
	defer r.Close()
	assert.Equal(t, string(readAllBytes(t, r)), "only\n")
}

func TestNewReader_K8sFile_MalformedLineErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "not-a-valid-entry\n")

	_, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{},
	)
	assert.ErrorContains(t, err, "malformed")
}

func TestNewReader_K8sFile_StderrLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "2023-08-07T19:56:34.223758260Z stderr F problem\n")

	r, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{},
	)
	assert.NilError(t, err)
	defer r.Close()

	rec := <-r.Records()
	assert.NilError(t, rec.Err)
	line := rec.Line
	assert.Equal(t, line.Stream, logdriver.StreamStderr)
	assert.Equal(t, string(line.Line), "problem\n")
}

func TestNewReader_K8sFile_SinceUntilFilters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.000000000Z stdout F before\n"+
			"2023-08-07T19:56:35.000000000Z stdout F inside-1\n"+
			"2023-08-07T19:56:36.000000000Z stdout F inside-2\n"+
			"2023-08-07T19:56:37.000000000Z stdout F after\n",
	)

	r, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{
			Since: mustTime(t, "2023-08-07T19:56:35Z"),
			Until: mustTime(t, "2023-08-07T19:56:36Z"),
		},
	)
	assert.NilError(t, err)
	defer r.Close()

	assert.Equal(t, string(readAllBytes(t, r)), "inside-1\ninside-2\n")
}

func TestNewReader_K8sFile_HeadFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.000000000Z stdout F one\n"+
			"2023-08-07T19:56:35.000000000Z stdout F two\n"+
			"2023-08-07T19:56:36.000000000Z stdout F three\n",
	)

	r, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{Head: 2},
	)
	assert.NilError(t, err)
	defer r.Close()

	assert.Equal(t, string(readAllBytes(t, r)), "one\ntwo\n")
}

func TestNewReader_K8sFile_TailFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.000000000Z stdout F one\n"+
			"2023-08-07T19:56:35.000000000Z stdout F two\n"+
			"2023-08-07T19:56:36.000000000Z stdout F three\n",
	)

	r, err := logdriver.NewReader(
		context.Background(),
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{Tail: 2},
	)
	assert.NilError(t, err)
	defer r.Close()

	assert.Equal(t, string(readAllBytes(t, r)), "two\nthree\n")
}

func TestNewReader_K8sFile_TailFollowReadsAppendedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.000000000Z stdout F one\n"+
			"2023-08-07T19:56:35.000000000Z stdout F two\n",
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := logdriver.NewReader(
		ctx,
		"k8s-file",
		dir,
		map[string]string{"path": path},
		logdriver.ReaderOption{Tail: 1, Follow: true},
	)
	assert.NilError(t, err)
	defer r.Close()

	var buf syncBuffer
	done := make(chan error, 1)
	go func() {
		for rec := range r.Records() {
			if rec.Err != nil {
				done <- rec.Err
				return
			}
			_, err := buf.Write(rec.Line.Line)
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	time.Sleep(k8sfile.FollowPollInterval + 50*time.Millisecond)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	assert.NilError(t, err)
	_, err = f.WriteString("2023-08-07T19:56:36.000000000Z stdout F three\n")
	assert.NilError(t, err)
	assert.NilError(t, f.Close())

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if buf.contains("three") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	assert.NilError(t, <-done)
	assert.Equal(t, buf.String(), "two\nthree\n")
}
