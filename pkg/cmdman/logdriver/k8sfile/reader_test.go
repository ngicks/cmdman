package k8sfile

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"gotest.tools/v3/assert"
)

func readK8sLines(t *testing.T, r logdriver.Reader) string {
	t.Helper()
	var out bytes.Buffer
	for rec := range r.Records() {
		assert.NilError(t, rec.Err)
		_, err := out.Write(rec.Line.Line)
		assert.NilError(t, err)
	}
	return out.String()
}

func TestReaderTailCrossesRotatedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeK8sFixture(t, path,
		"2023-08-07T19:56:38.000000000Z stdout F active\n",
	)
	writeK8sFixture(t, path+".1",
		"2023-08-07T19:56:36.000000000Z stdout F one\n"+
			"2023-08-07T19:56:37.000000000Z stdout F two\n"+
			"2023-08-07T19:56:37.500000000Z stdout R -\n",
	)
	writeK8sFixture(t, path+".2",
		"2023-08-07T19:56:34.000000000Z stdout F old\n"+
			"2023-08-07T19:56:35.000000000Z stdout F older\n"+
			"2023-08-07T19:56:35.500000000Z stdout R -\n",
	)

	r, err := newReader(context.Background(), path, 3, logdriver.ReaderOption{Tail: 3})
	assert.NilError(t, err)
	defer r.Close()

	assert.Equal(t, readK8sLines(t, r), "one\ntwo\nactive\n")
}

func TestReaderSinceSelectsArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeK8sFixture(t, path,
		"2023-08-07T19:56:38.000000000Z stdout F active\n",
	)
	writeK8sFixture(t, path+".1",
		"2023-08-07T19:56:36.000000000Z stdout F before\n"+
			"2023-08-07T19:56:37.000000000Z stdout F selected\n"+
			"2023-08-07T19:56:37.500000000Z stdout R -\n",
	)

	since, err := time.Parse(time.RFC3339Nano, "2023-08-07T19:56:37Z")
	assert.NilError(t, err)
	r, err := newReader(context.Background(), path, 2, logdriver.ReaderOption{Since: since})
	assert.NilError(t, err)
	defer r.Close()

	assert.Equal(t, readK8sLines(t, r), "selected\nactive\n")
}

func TestReaderSkipsRotationMarkerAndContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeK8sFixture(t, path,
		"2023-08-07T19:56:36.000000000Z stdout F active\n",
	)
	writeK8sFixture(t, path+".1",
		"2023-08-07T19:56:35.000000000Z stdout F archived\n"+
			"2023-08-07T19:56:35.500000000Z stdout R -\n",
	)

	r, err := newReader(context.Background(), path, 2, logdriver.ReaderOption{})
	assert.NilError(t, err)
	defer r.Close()

	assert.Equal(t, readK8sLines(t, r), "archived\nactive\n")
}

func TestReader_ClosesOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeK8sFixture(t, path,
		"2023-08-07T19:56:36.000000000Z stdout F active\n",
	)

	ctx, cancel := context.WithCancel(context.Background())
	r, err := newReader(ctx, path, 0, logdriver.ReaderOption{Follow: true})
	assert.NilError(t, err)
	defer r.Close()

	select {
	case rec, ok := <-r.Records():
		assert.Assert(t, ok)
		assert.NilError(t, rec.Err)
		assert.Equal(t, string(rec.Line.Line), "active\n")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for initial record")
	}

	cancel()

	select {
	case _, ok := <-r.Records():
		assert.Assert(t, !ok, "records channel should close after context cancellation")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for records channel to close")
	}
}

func TestWriterAddsMarkerBeforeArchiveRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newWriter(path, 50, 2)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("line1\n"))
	assert.NilError(t, err)
	_, err = w.Write([]byte("line2\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	archive, err := os.ReadFile(path + ".1")
	assert.NilError(t, err)
	assert.Assert(t, containsLine(string(archive), " stdout R -"))
}

func TestWriterAddsMarkerBeforeRemoveRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newWriter(path, 50, 1)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("line1\n"))
	assert.NilError(t, err)
	old, err := os.Open(path)
	assert.NilError(t, err)
	defer old.Close()
	_, err = w.Write([]byte("line2\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	data, err := ioReadAll(old)
	assert.NilError(t, err)
	assert.Assert(t, containsLine(string(data), " stdout R -"))
}

func containsLine(s, substr string) bool {
	return strings.Contains(s, substr)
}

func ioReadAll(f *os.File) ([]byte, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
