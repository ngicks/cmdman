package logdriver

import (
	"bytes"
	"context"
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

func TestNewReader_NoneDriverErrors(t *testing.T) {
	err := NewReader(
		context.Background(),
		store.LogDriverNone,
		"/tmp/whatever",
		&bytes.Buffer{},
		false,
	)
	assert.ErrorContains(t, err, "does not retain logs")
}

func TestNewReader_UnknownDriverErrors(t *testing.T) {
	err := NewReader(context.Background(), "bogus", "/tmp/whatever", &bytes.Buffer{}, false)
	assert.ErrorContains(t, err, "unknown driver")
}

func TestNewReader_K8sFile_FullLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.223758260Z stdout F line1\n"+
			"2023-08-07T19:56:34.223758260Z stdout F line2\n",
	)

	var buf bytes.Buffer
	assert.NilError(t, NewReader(context.Background(), store.LogDriverK8sFile, path, &buf, false))
	assert.Equal(t, buf.String(), "line1\nline2\n")
}

func TestNewReader_K8sFile_JoinsPartials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	// "lin" was a partial; "e2\n" completed the line. Expected reconstruction: "line2\n".
	writeFixture(t, path,
		"2023-08-07T19:56:34.223758260Z stdout P lin\n"+
			"2023-08-07T19:56:34.223758260Z stdout F e2\n",
	)

	var buf bytes.Buffer
	assert.NilError(t, NewReader(context.Background(), store.LogDriverK8sFile, path, &buf, false))
	assert.Equal(t, buf.String(), "line2\n")
}

func TestNewReader_K8sFile_TrailingPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path,
		"2023-08-07T19:56:34.223758260Z stdout F complete\n"+
			"2023-08-07T19:56:34.223758260Z stdout P rest\n",
	)

	var buf bytes.Buffer
	assert.NilError(t, NewReader(context.Background(), store.LogDriverK8sFile, path, &buf, false))
	assert.Equal(t, buf.String(), "complete\nrest")
}

func TestNewReader_K8sFile_MissingFileErrors(t *testing.T) {
	err := NewReader(
		context.Background(),
		store.LogDriverK8sFile,
		"/no/such/path",
		&bytes.Buffer{},
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
		done <- NewReader(ctx, store.LogDriverK8sFile, path, &buf, true)
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

	var buf bytes.Buffer
	err := NewReader(ctx, store.LogDriverK8sFile, path, &buf, true)
	assert.NilError(t, err)
	assert.Equal(t, buf.String(), "only\n")
}

func TestNewReader_K8sFile_MalformedLineErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeFixture(t, path, "not-a-valid-entry\n")

	err := NewReader(context.Background(), store.LogDriverK8sFile, path, &bytes.Buffer{}, false)
	assert.ErrorContains(t, err, "malformed")
}
