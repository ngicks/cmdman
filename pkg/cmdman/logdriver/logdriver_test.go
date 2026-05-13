package logdriver_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	_ "github.com/ngicks/cmdman/pkg/cmdman/logdriver/k8sfile"
	"gotest.tools/v3/assert"
)

func TestNew_Dispatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := logdriver.NewWriter(t.Context(), "none", dir, nil)
	assert.NilError(t, err)
	sw := logdriver.NewStreamWriter(w, logdriver.StreamStdout)
	_, err = sw.Write([]byte("ignored\n"))
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
	_, statErr := os.Stat(path)
	assert.Assert(t, os.IsNotExist(statErr), "none driver must not create a file")

	w2, err := logdriver.NewWriter(t.Context(), "k8s-file", dir, map[string]string{"path": path})
	assert.NilError(t, err)
	sw2 := logdriver.NewStreamWriter(w2, logdriver.StreamStdout)
	_, err = sw2.Write([]byte("captured\n"))
	assert.NilError(t, err)
	assert.NilError(t, w2.Close())
	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(got), " stdout F captured"))

	_, err = logdriver.NewWriter(t.Context(), "bogus", dir, map[string]string{"path": path})
	assert.ErrorContains(t, err, "unknown driver")
}

func TestNew_K8sFileParsesMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	w, err := logdriver.NewWriter(t.Context(), "k8s-file", dir, map[string]string{
		"path":     path,
		"max-size": "10mb",
	})
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
}

func TestNew_K8sFileRejectsBadMaxSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")

	_, err := logdriver.NewWriter(t.Context(), "k8s-file", dir, map[string]string{
		"path":     path,
		"max-size": "not-a-size",
	})
	assert.ErrorContains(t, err, "max-size")
}

func TestNew_NoneIgnoresOpts(t *testing.T) {
	w, err := logdriver.NewWriter(t.Context(), "none", "", map[string]string{
		"max-size": "not-a-size",
		"max-file": "abc",
	})
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
}

func TestSplitLogLines(t *testing.T) {
	lines := logdriver.SplitLogLines(time.Time{}, "", []byte("one\ntwo\nthr"))
	assert.Equal(t, len(lines), 3)
	assert.Equal(t, string(lines[0].Line), "one\n")
	assert.Equal(t, lines[0].Partial, false)
	assert.Equal(t, string(lines[1].Line), "two\n")
	assert.Equal(t, lines[1].Partial, false)
	assert.Equal(t, string(lines[2].Line), "thr")
	assert.Equal(t, lines[2].Partial, true)
}
