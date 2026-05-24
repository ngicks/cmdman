package k8sfile

import (
	"context"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"gotest.tools/v3/assert"
)

func TestDriverNewWriterUsesDefaultRotationOptions(t *testing.T) {
	dir := t.TempDir()

	lw, err := Driver{}.NewWriter(context.Background(), dir, nil)
	assert.NilError(t, err)
	w := lw.(*Writer)
	assert.Equal(t, w.maxSize, int64(DefaultLogMaxSize))
	assert.Equal(t, w.maxFile, DefaultLogMaxFile)
	assert.NilError(t, w.Close())
}

func TestDriverNewWriterExplicitEmptyOptionsDisableRotation(t *testing.T) {
	dir := t.TempDir()

	lw, err := Driver{}.NewWriter(context.Background(), dir, map[string]string{
		logdriver.LogOptMaxSize: "",
		logdriver.LogOptMaxFile: "",
	})
	assert.NilError(t, err)
	w := lw.(*Writer)
	assert.Equal(t, w.maxSize, int64(0))
	assert.Equal(t, w.maxFile, 0)
	assert.NilError(t, w.Close())
}
