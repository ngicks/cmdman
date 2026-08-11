package compose_test

import (
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/compose"
)

func TestProjectSelection_MuxWindowName(t *testing.T) {
	t.Run("named project", func(t *testing.T) {
		sel := compose.ProjectSelection{Project: "web"}
		assert.Equal(t, sel.MuxWindowName(), "cmdman-web")
	})
	t.Run("unnamed project", func(t *testing.T) {
		sel := compose.ProjectSelection{}
		assert.Equal(t, sel.MuxWindowName(), "cmdman")
	})
}

func TestResolveMuxSelection(t *testing.T) {
	t.Run("explicit file with mux section", func(t *testing.T) {
		_, cwd := muxTestEnv(t)
		path := filepath.Join(cwd, "custom.yaml")
		writeFile(t, path, muxComposeYAML("named", ""))

		sel, err := compose.ResolveMuxSelection(compose.NormalizeOpts{File: path})
		assert.NilError(t, err)
		assert.Assert(t, sel.Spec != nil && sel.Spec.Mux != nil)
		assert.Equal(t, sel.Project, "named")
	})

	t.Run("explicit file without mux section errors", func(t *testing.T) {
		_, cwd := muxTestEnv(t)
		path := filepath.Join(cwd, "plain.yaml")
		writeFile(t, path, plainComposeYAML("named", ""))

		_, err := compose.ResolveMuxSelection(compose.NormalizeOpts{File: path})
		assert.Error(t, err, `compose mux: missing "mux:" section in compose file`)
	})

	t.Run("no file auto-selects cwd mux compose", func(t *testing.T) {
		_, cwd := muxTestEnv(t)
		writeFile(t, filepath.Join(cwd, "cmd-compose.yaml"), muxComposeYAML("cwdproj", ""))

		sel, err := compose.ResolveMuxSelection(compose.NormalizeOpts{})
		assert.NilError(t, err)
		assert.Equal(t, sel.Project, "cwdproj")
	})
}

func TestResolveMuxSelectionByName(t *testing.T) {
	t.Run("explicit compose file with mux section", func(t *testing.T) {
		_, cwd := muxTestEnv(t)
		path := filepath.Join(cwd, "custom.yaml")
		writeFile(t, path, muxComposeYAML("named", ""))

		sel, err := compose.ResolveMuxSelectionByName("named", path)
		assert.NilError(t, err)
		assert.Assert(t, sel.Spec != nil && sel.Spec.Mux != nil)
	})

	t.Run("compose file without mux section errors", func(t *testing.T) {
		_, cwd := muxTestEnv(t)
		path := filepath.Join(cwd, "plain.yaml")
		writeFile(t, path, plainComposeYAML("named", ""))

		_, err := compose.ResolveMuxSelectionByName("named", path)
		assert.Error(t, err, `mux: project "named" has no mux section`)
	})

	t.Run("no file and no name in a fileless dir has no compose file", func(t *testing.T) {
		// With no composeFile and no projectName, LoadOrProject falls back to a
		// workdir selection with a nil Spec (no default compose file in this cwd),
		// so the "no compose file found" branch is reached.
		muxTestEnv(t)

		_, err := compose.ResolveMuxSelectionByName("", "")
		assert.Error(t, err, `mux: no compose file found for project ""`)
	})
}
