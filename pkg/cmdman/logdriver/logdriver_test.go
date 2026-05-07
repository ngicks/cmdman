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
	w, err := newK8sFileWriter(path, 0, 0)
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
	w, err := newK8sFileWriter(path, 0, 0)
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
	w, err := newK8sFileWriter(path, 0, 0)
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
	w, err := newK8sFileWriter(path, 0, 0)
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

	w1, err := newK8sFileWriter(path, 0, 0)
	assert.NilError(t, err)
	w1.now = fixedTime(t)
	_, err = w1.Write([]byte("first\n"))
	assert.NilError(t, err)
	assert.NilError(t, w1.Close())

	w2, err := newK8sFileWriter(path, 0, 0)
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

	w, err := New(store.LogDriverNone, Options{})
	assert.NilError(t, err)
	_, err = w.Write([]byte("ignored\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
	_, statErr := os.Stat(path)
	assert.Assert(t, os.IsNotExist(statErr), "none driver must not create a file")

	w2, err := New(store.LogDriverK8sFile, Options{store.LogOptPath: path})
	assert.NilError(t, err)
	_, err = w2.Write([]byte("captured\n"))
	assert.NilError(t, err)
	assert.NilError(t, w2.Close())
	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(got), " stdout F captured"))

	_, err = New("bogus", Options{store.LogOptPath: path})
	assert.ErrorContains(t, err, "unknown driver")
}

func TestNew_K8sFileParsesMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := New(store.LogDriverK8sFile, Options{
		store.LogOptPath:    path,
		store.LogOptMaxSize: "10mb",
	})
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
}

func TestNew_K8sFileRejectsBadMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	_, err := New(store.LogDriverK8sFile, Options{
		store.LogOptPath:    path,
		store.LogOptMaxSize: "not-a-size",
	})
	assert.ErrorContains(t, err, "max-size")
}

func TestNew_NoneIgnoresOpts(t *testing.T) {
	// The none driver must not parse opts: it returns a noop writer
	// regardless of what's in the bag, including invalid values that
	// would fail for the k8s-file driver.
	w, err := New(store.LogDriverNone, Options{
		store.LogOptMaxSize: "not-a-size",
		store.LogOptMaxFile: "abc",
	})
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
}

func TestK8sFileWriter_EmptyWriteIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	w, err := newK8sFileWriter(path, 0, 0)
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

	// Each "lineN\n" line produces a 47-byte entry (41 overhead + 6 chars).
	// Cap at 100 bytes with maxFile = 1 so the third entry triggers an
	// in-place truncation (no archive kept).
	w, err := newK8sFileWriter(path, 100, 1)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	for _, line := range []string{"line1\n", "line2\n", "line3\n"} {
		_, err := w.Write([]byte(line))
		assert.NilError(t, err)
	}
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	// After the truncation triggered by the third write, only "line3" should
	// remain on disk — the older entries were dropped.
	want := "2023-08-07T19:56:34.223758260Z stdout F line3\n"
	assert.Equal(t, string(got), want)

	// No .1 archive should exist with maxFile = 1.
	_, err = os.Stat(path + ".1")
	assert.Assert(t, os.IsNotExist(err), "expected no .1 archive with maxFile=1")
}

func TestK8sFileWriter_HonorsExistingFileSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	// Pre-populate the file so the writer has to account for existing bytes.
	pre := strings.Repeat("x", 80)
	assert.NilError(t, os.WriteFile(path, []byte(pre), 0o640))

	// Cap of 100 leaves only 20 bytes of headroom, which is less than a
	// single 47-byte entry — the very first write must trigger rotation.
	w, err := newK8sFileWriter(path, 100, 1)
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

	// Cap is smaller than the resulting entry. The writer rotates first
	// (a no-op on an empty file) and then writes the entry in full, leaving
	// the file larger than the cap. Matches podman's behavior.
	w, err := newK8sFileWriter(path, 10, 1)
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

	// maxFile = 3 → keep active + .1 + .2. Each "lineN\n" entry is 47 bytes;
	// cap at 50 forces every write to rotate.
	w, err := newK8sFileWriter(path, 50, 3)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	for _, line := range []string{"line1\n", "line2\n", "line3\n", "line4\n"} {
		_, err := w.Write([]byte(line))
		assert.NilError(t, err)
	}
	assert.NilError(t, w.Close())

	// After four writes with per-entry rotation:
	//   active  -> line4
	//   .1      -> line3
	//   .2      -> line2
	// line1 was rotated past .2 and dropped.
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
	// .3 must not exist — the chain is capped at maxFile.
	_, err = os.Stat(path + ".3")
	assert.Assert(t, os.IsNotExist(err), "expected no .3 archive with maxFile=3")
}

func TestK8sFileWriter_RotateWithEmptyArchiveSlots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	// maxFile = 3 but only one rotation happens. .2 should never be created.
	w, err := newK8sFileWriter(path, 50, 3)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	_, err = w.Write([]byte("line1\n"))
	assert.NilError(t, err)
	_, err = w.Write([]byte("line2\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())

	// active = line2, .1 = line1, .2 should not exist.
	active, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(active), "line2"))
	one, err := os.ReadFile(path + ".1")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(one), "line1"))
	_, err = os.Stat(path + ".2")
	assert.Assert(t, os.IsNotExist(err))
}

func TestK8sFileWriter_ZeroMaxSizeMeansUnlimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := newK8sFileWriter(path, 0, 0)
	assert.NilError(t, err)
	w.now = fixedTime(t)

	for _, line := range []string{"line1\n", "line2\n", "line3\n"} {
		_, err := w.Write([]byte(line))
		assert.NilError(t, err)
	}
	assert.NilError(t, w.Close())

	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	// All three entries should still be present.
	for _, want := range []string{"line1", "line2", "line3"} {
		assert.Assert(t, strings.Contains(string(got), want), "expected %q in %q", want, got)
	}
}
