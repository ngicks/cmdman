package logdriver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"gotest.tools/v3/assert"
)

func fixedTime(t *testing.T) func() time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, "2023-08-07T19:56:34.223758260Z")
	assert.NilError(t, err)
	return func() time.Time { return ts }
}

func TestK8sFileWriter_FullLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newK8sFileWriter(path)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	n, err := w.Write([]byte("hello\n"))
	assert.NilError(t, err)
	assert.Equal(t, n, len("hello\n"))
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stdout F hello\n"
	assert.Equal(t, string(got), want)
}

func TestK8sFileWriter_PartialThenFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newK8sFileWriter(path)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("lin"))
	assert.NilError(t, err)
	_, err = w.Write([]byte("e2\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stdout P lin\n" +
		"2023-08-07T19:56:34.223758260Z stdout F e2\n"
	assert.Equal(t, string(got), want)
}

func TestK8sFileWriter_MultipleLinesOneBatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newK8sFileWriter(path)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("line1\nline2\nline3\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	wantPrefix := "2023-08-07T19:56:34.223758260Z stdout F "
	lines := strings.Split(strings.TrimRight(string(got), "\n"), "\n")
	assert.Equal(t, len(lines), 3)
	for i, l := range lines {
		assert.Equal(t, l, wantPrefix+[]string{"line1", "line2", "line3"}[i])
	}
}

func TestK8sFileWriter_TrailingPartial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newK8sFileWriter(path)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("complete\nrest"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stdout F complete\n" +
		"2023-08-07T19:56:34.223758260Z stdout P rest\n"
	assert.Equal(t, string(got), want)
}

func TestK8sFileWriter_AppendsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w1, err := newK8sFileWriter(path)
	assert.NilError(t, err)
	w1.now = fixedTime(t)
	_, err = w1.Write([]byte("first\n"))
	assert.NilError(t, err)
	assert.NilError(t, w1.Close())

	w2, err := newK8sFileWriter(path)
	assert.NilError(t, err)
	w2.now = fixedTime(t)
	_, err = w2.Write([]byte("second\n"))
	assert.NilError(t, err)
	assert.NilError(t, w2.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stdout F first\n" +
		"2023-08-07T19:56:34.223758260Z stdout F second\n"
	assert.Equal(t, string(got), want)
}

func TestNew_Dispatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := New(store.LogDriverNone, "")
	assert.NilError(t, err)
	_, err = w.Write([]byte("ignored\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
	_, statErr := os.Stat(path)
	assert.Assert(t, os.IsNotExist(statErr), "none driver must not create a file")

	w2, err := New(store.LogDriverK8sFile, path)
	assert.NilError(t, err)
	_, err = w2.Write([]byte("captured\n"))
	assert.NilError(t, err)
	assert.NilError(t, w2.Close())
	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(got), " stdout F captured"))

	_, err = New("bogus", path)
	assert.ErrorContains(t, err, "unknown driver")
}

func TestK8sFileWriter_EmptyWriteIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newK8sFileWriter(path)
	assert.NilError(t, err)

	n, err := w.Write(nil)
	assert.NilError(t, err)
	assert.Equal(t, n, 0)
	assert.NilError(t, w.Close())

	info, err := os.Stat(path)
	assert.NilError(t, err)
	assert.Equal(t, info.Size(), int64(0))
}
