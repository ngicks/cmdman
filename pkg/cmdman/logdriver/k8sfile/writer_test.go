package k8sfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
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
	w, err := newWriter(path, 0, 0)
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
	w, err := newWriter(path, 0, 0)
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
	w, err := newWriter(path, 0, 0)
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
	w, err := newWriter(path, 0, 0)
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

	w1, err := newWriter(path, 0, 0)
	assert.NilError(t, err)
	w1.now = fixedTime(t)
	_, err = w1.Write([]byte("first\n"))
	assert.NilError(t, err)
	assert.NilError(t, w1.Close())

	w2, err := newWriter(path, 0, 0)
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

func TestK8sFileWriter_LogLineStderr(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newWriter(path, 0, 0)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	err = w.WriteLogLine(logdriver.LogLine{
		Stream: logdriver.StreamStderr,
		Line:   []byte("problem\n"),
	})
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stderr F problem\n"
	assert.Equal(t, string(got), want)
}

func TestK8sFileWriter_EmptyWriteIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newWriter(path, 0, 0)
	assert.NilError(t, err)

	n, err := w.Write(nil)
	assert.NilError(t, err)
	assert.Equal(t, n, 0)
	assert.NilError(t, w.Close())

	info, err := os.Stat(path)
	assert.NilError(t, err)
	assert.Equal(t, info.Size(), int64(0))
}

func TestK8sFileWriter_TruncatesAtMaxSizeWithoutArchives(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := newWriter(path, 100, 1)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	for _, line := range []string{"line1\n", "line2\n", "line3\n"} {
		_, err := w.Write([]byte(line))
		assert.NilError(t, err)
	}
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stdout F line3\n"
	assert.Equal(t, string(got), want)

	_, err = os.Stat(path + ".1")
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected no .1 archive with maxFile=1")
}

func TestK8sFileWriter_HonorsExistingFileSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	pre := strings.Repeat("x", 80)
	assert.NilError(t, os.WriteFile(path, []byte(pre), 0o640))

	w, err := newWriter(path, 100, 1)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("fresh\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stdout F fresh\n"
	assert.Equal(t, string(got), want)
}

func TestK8sFileWriter_OversizedEntryStillWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := newWriter(path, 10, 1)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("a-big-line\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	want := "2023-08-07T19:56:34.223758260Z stdout F a-big-line\n"
	assert.Equal(t, string(got), want)
}

func TestK8sFileWriter_RotatesArchiveChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := newWriter(path, 50, 3)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	for _, line := range []string{"line1\n", "line2\n", "line3\n", "line4\n"} {
		_, err := w.Write([]byte(line))
		assert.NilError(t, err)
	}
	assert.NilError(t, w.Close())

	for name, want := range map[string]string{
		path:        "line4",
		path + ".1": "line3",
		path + ".2": "line2",
	} {
		data, err := os.ReadFile(name)
		assert.NilError(t, err, "reading %s", name)
		assert.Assert(t, strings.Contains(string(data), want),
			"expected %q in %s, got %q", want, name, string(data))
	}
	_, err = os.Stat(path + ".3")
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected no .3 archive with maxFile=3")
}

func TestK8sFileWriter_RotateWithEmptyArchiveSlots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := newWriter(path, 50, 3)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("line1\n"))
	assert.NilError(t, err)
	_, err = w.Write([]byte("line2\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	active, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(active), "line2"))
	one, err := os.ReadFile(path + ".1")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(one), "line1"))
	_, err = os.Stat(path + ".2")
	assert.Assert(t, errors.Is(err, fs.ErrNotExist))
}

func TestK8sFileWriter_ZeroMaxSizeMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := newWriter(path, 0, 0)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	for _, line := range []string{"line1\n", "line2\n", "line3\n"} {
		_, err := w.Write([]byte(line))
		assert.NilError(t, err)
	}
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	for _, want := range []string{"line1", "line2", "line3"} {
		assert.Assert(t, strings.Contains(string(got), want), "expected %q in %q", want, got)
	}
}
