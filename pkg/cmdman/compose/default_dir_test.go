package compose_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

// writeFile writes content to path, creating parent dirs.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))
}

// ---- ${CMDMAN_COMPOSE_FILE} interpolation -----------------------------------

func TestComposeFileInterpolation(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "proj")
	writeFile(t, filepath.Join(projDir, "app.env"), "APP_KEY=fromfile\n")

	yamlContent := `
name: interp-cf
commands:
  app:
    args:
      - echo
      - ${CMDMAN_COMPOSE_FILE}
    env_file:
      - path: ${CMDMAN_COMPOSE_FILE}/../app.env
    env:
      - SELF=${CMDMAN_COMPOSE_FILE}
`
	yamlPath := filepath.Join(projDir, "compose.yaml")
	writeFile(t, yamlPath, yamlContent)

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(), yamlPath, raw,
		compose.NormalizeOpts{WorkDir: projDir},
	)
	assert.NilError(t, err)

	// args see the compose file's absolute path.
	assert.Equal(t, spec.Commands[0].Args[1], yamlPath)
	// the env_file referenced relative to the compose file was loaded.
	envMap := envSliceToMap(spec.Commands[0].Env)
	assert.Equal(t, envMap["APP_KEY"], "fromfile")
	// env: entries can reference the variable too.
	assert.Equal(t, envMap["SELF"], yamlPath)
}

// work_dir may be expressed relative to the compose file, which makes a named
// project's identity independent of the invocation CWD.
func TestComposeFileInterpolation_WorkDir(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "proj")
	yamlPath := filepath.Join(projDir, "compose.yaml")
	writeFile(t, yamlPath, `
name: wd
work_dir: ${CMDMAN_COMPOSE_FILE}/..
commands:
  app:
    args: [echo, hi]
`)

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(), yamlPath, raw, compose.NormalizeOpts{},
	)
	assert.NilError(t, err)
	assert.Equal(t, spec.WorkDir, projDir)
}

// ---- default compose dir name resolution ------------------------------------

func TestDiscoverFile_NamedProject(t *testing.T) {
	conf := t.TempDir()
	// CMDMAN_CONF points at the config file; the compose dir is its sibling.
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	composeDir := filepath.Join(conf, "compose")

	writeFile(t, filepath.Join(composeDir, "filey.yaml"),
		"name: filey\ncommands:\n  a:\n    args: [echo, a]\n")
	dirProj := filepath.Join(composeDir, "diry")
	writeFile(t, filepath.Join(dirProj, "compose.yaml"),
		"name: diry\ncommands:\n  a:\n    args: [echo, a]\n")

	cwd, err := os.Getwd()
	assert.NilError(t, err)

	t.Run("file style", func(t *testing.T) {
		path, raw, err := compose.DiscoverFile(cwd, compose.NormalizeOpts{File: "filey"})
		assert.NilError(t, err)
		assert.Equal(t, path, filepath.Join(composeDir, "filey.yaml"))
		assert.Equal(t, raw.Name, "filey")
	})

	t.Run("dir style", func(t *testing.T) {
		path, raw, err := compose.DiscoverFile(cwd, compose.NormalizeOpts{File: "diry"})
		assert.NilError(t, err)
		assert.Equal(t, path, filepath.Join(dirProj, "compose.yaml"))
		assert.Equal(t, raw.Name, "diry")
	})

	t.Run("explicit existing path wins over name", func(t *testing.T) {
		tmp := t.TempDir()
		p := filepath.Join(tmp, "x.yaml")
		writeFile(t, p, "name: x\ncommands:\n  a:\n    args: [echo, a]\n")
		path, raw, err := compose.DiscoverFile(cwd, compose.NormalizeOpts{File: p})
		assert.NilError(t, err)
		assert.Equal(t, path, p)
		assert.Equal(t, raw.Name, "x")
	})

	t.Run("unknown name errors", func(t *testing.T) {
		_, _, err := compose.DiscoverFile(cwd, compose.NormalizeOpts{File: "nope"})
		assert.Assert(t, err != nil)
	})

	t.Run("dir without compose.yaml errors", func(t *testing.T) {
		assert.NilError(t, os.MkdirAll(filepath.Join(composeDir, "empty"), 0o755))
		_, _, err := compose.DiscoverFile(cwd, compose.NormalizeOpts{File: "empty"})
		assert.Assert(t, err != nil)
	})
}
