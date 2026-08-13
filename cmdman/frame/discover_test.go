package frame_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/frame"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

const defContent = "frame:\n  - edge: left\n    size: 20%\n    component: switcher\n"

// writeFile writes content to path, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
}

// frameDir points CMDMAN_CONF at a temp config file so the frame dir is its
// sibling, and returns that dir.
func frameDir(t *testing.T) string {
	t.Helper()
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	return filepath.Join(conf, "frame")
}

func TestDiscoverFile_NamedDef(t *testing.T) {
	dir := frameDir(t)
	writeFile(t, filepath.Join(dir, "work.yaml"), defContent)
	writeFile(t, filepath.Join(dir, "alt.yml"), defContent)

	cwd, err := os.Getwd()
	assert.NilError(t, err)

	t.Run("bare name", func(t *testing.T) {
		path, raw, err := frame.DiscoverFile(cwd, "work")
		assert.NilError(t, err)
		assert.Equal(t, path, filepath.Join(dir, "work.yaml"))
		assert.Equal(t, len(raw.Frame), 1)
	})

	t.Run("yml extension", func(t *testing.T) {
		path, _, err := frame.DiscoverFile(cwd, "alt")
		assert.NilError(t, err)
		assert.Equal(t, path, filepath.Join(dir, "alt.yml"))
	})

	t.Run("name carrying its extension", func(t *testing.T) {
		path, _, err := frame.DiscoverFile(cwd, "work.yaml")
		assert.NilError(t, err)
		assert.Equal(t, path, filepath.Join(dir, "work.yaml"))
	})

	t.Run("explicit path", func(t *testing.T) {
		tmp := t.TempDir()
		p := filepath.Join(tmp, "custom.yaml")
		writeFile(t, p, defContent)
		path, raw, err := frame.DiscoverFile(cwd, p)
		assert.NilError(t, err)
		assert.Equal(t, path, p)
		assert.Equal(t, len(raw.Frame), 1)
	})

	t.Run("unknown name errors at the path the caller typed", func(t *testing.T) {
		_, _, err := frame.DiscoverFile(cwd, "nope")
		assert.Assert(t, err != nil)
		assert.Assert(t, cmp.Contains(err.Error(), filepath.Join(cwd, "nope")))
		// The open failure names its own path, once: a wrapper repeating it
		// read as "open <path>: open <path>: …".
		assert.Equal(t, strings.Count(err.Error(), "open "), 1)
		// And callers branch on the identity, not on the text.
		assert.Assert(t, errors.Is(err, fs.ErrNotExist))
	})

	t.Run("empty name errors", func(t *testing.T) {
		_, _, err := frame.DiscoverFile(cwd, "")
		assert.Assert(t, err != nil)
	})
}

// With no frame dir on disk there is simply nothing named to find, so a
// reference falls through to the path form rather than failing early.
func TestDiscoverFile_FrameDirAbsent(t *testing.T) {
	t.Setenv("CMDMAN_CONF", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "local.yaml"), defContent)

	path, _, err := frame.DiscoverFile(cwd, "local.yaml")
	assert.NilError(t, err)
	assert.Equal(t, path, filepath.Join(cwd, "local.yaml"))
}

// TestIsBareName pins the split callers phrase failures by: a bare name is the
// form the frame dir answers for, everything else is a path.
func TestIsBareName(t *testing.T) {
	for _, name := range []string{"work", "work.yaml", "work-2"} {
		assert.Assert(t, frame.IsBareName(name), "%q is a bare def name", name)
	}
	for _, name := range []string{"", ".", "..", "./work", "dir/work", `dir\work`, "/abs/work"} {
		assert.Assert(t, !frame.IsBareName(name), "%q is not a bare def name", name)
	}
}

func TestListNamedDefs(t *testing.T) {
	dir := frameDir(t)

	t.Run("missing dir", func(t *testing.T) {
		names, err := frame.ListNamedDefs()
		assert.NilError(t, err)
		assert.Assert(t, names == nil)
	})

	writeFile(t, filepath.Join(dir, "work.yaml"), defContent)
	writeFile(t, filepath.Join(dir, "alt.yml"), defContent)
	// Same name in both spellings collapses to one entry.
	writeFile(t, filepath.Join(dir, "alt.yaml"), defContent)
	writeFile(t, filepath.Join(dir, "notes.txt"), "ignored")
	assert.NilError(t, os.MkdirAll(filepath.Join(dir, "adir"), 0o755))

	names, err := frame.ListNamedDefs()
	assert.NilError(t, err)
	assert.DeepEqual(t, names, []string{"alt", "work"})
}

func TestLoadAndNormalize(t *testing.T) {
	dir := frameDir(t)
	writeFile(t, filepath.Join(dir, "work.yaml"), defContent)

	spec, err := frame.LoadAndNormalize(context.Background(), "work")
	assert.NilError(t, err)
	assert.Equal(t, spec.Path, filepath.Join(dir, "work.yaml"))
	assert.Equal(t, len(spec.Entries), 1)
	assert.Equal(t, spec.Entries[0].Component, frame.ComponentSwitcher)

	t.Run("validation error names the file", func(t *testing.T) {
		bad := filepath.Join(dir, "bad.yaml")
		writeFile(t, bad, "frame:\n  - edge: left\n    size: 10\n    component: clock\n")
		_, err := frame.LoadAndNormalize(context.Background(), "bad")
		assert.Assert(t, err != nil)
		assert.Assert(t, cmp.Contains(err.Error(), bad))
	})
}
