package compose_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
)

// muxComposeYAML builds a compose file declaring a mux: section. When workDir is
// non-empty it sets work_dir:, which decides whether the project is associated
// with the invocation cwd.
func muxComposeYAML(name, workDir string) string {
	wd := ""
	if workDir != "" {
		wd = "work_dir: " + workDir + "\n"
	}
	return "name: " + name + "\n" + wd +
		"commands:\n  a:\n    args: [echo, a]\n" +
		"mux:\n  driver: tmux\n  layouts:\n    - name: solo\n      root: a\n"
}

// plainComposeYAML builds a compose file with no mux: section.
func plainComposeYAML(name, workDir string) string {
	wd := ""
	if workDir != "" {
		wd = "work_dir: " + workDir + "\n"
	}
	return "name: " + name + "\n" + wd + "commands:\n  a:\n    args: [echo, a]\n"
}

// muxTestEnv points CMDMAN_CONF at a fresh config dir, chdirs into a fresh
// project dir, and returns the compose dir and the (cleaned) cwd.
func muxTestEnv(t *testing.T) (composeDir, cwd string) {
	t.Helper()
	conf := t.TempDir()
	t.Setenv("CMDMAN_CONF", filepath.Join(conf, "config.json"))
	composeDir = filepath.Join(conf, "compose")

	proj := t.TempDir()
	t.Chdir(proj)
	got, err := os.Getwd()
	assert.NilError(t, err)
	return composeDir, filepath.Clean(got)
}

// The cwd compose file is selected when it is the only associated mux compose.
func TestSelectMuxProject_CwdFile(t *testing.T) {
	_, cwd := muxTestEnv(t)
	writeFile(t, filepath.Join(cwd, "cmd-compose.yaml"), muxComposeYAML("cwdproj", ""))

	sel, err := compose.SelectMuxProject(compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Assert(t, sel.Spec != nil && sel.Spec.Mux != nil)
	assert.Equal(t, sel.Project, "cwdproj")
}

// A named mux project whose work_dir is the cwd is auto-selected even when
// another named mux project targets a different directory: the second one is
// not associated with the cwd, so the selection is unambiguous (no -f needed).
func TestSelectMuxProject_NamedScopedToCwd(t *testing.T) {
	composeDir, cwd := muxTestEnv(t)
	other := t.TempDir()
	// "here" omits work_dir, so it defaults to the cwd at load time.
	writeFile(t, filepath.Join(composeDir, "here.yaml"), muxComposeYAML("here", ""))
	// "there" targets a different directory and must be ignored.
	writeFile(t, filepath.Join(composeDir, "there.yaml"), muxComposeYAML("there", other))

	sel, err := compose.SelectMuxProject(compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Assert(t, sel.Spec != nil && sel.Spec.Mux != nil)
	assert.Equal(t, sel.Project, "here")
	assert.Equal(t, filepath.Clean(sel.WorkDir), cwd)
}

// A cwd compose file without a mux: section does not block selecting the sole
// associated named project that does declare one.
func TestSelectMuxProject_CwdFileWithoutMuxPicksNamed(t *testing.T) {
	composeDir, cwd := muxTestEnv(t)
	writeFile(t, filepath.Join(cwd, "cmd-compose.yaml"), plainComposeYAML("cwdplain", ""))
	writeFile(t, filepath.Join(composeDir, "named.yaml"), muxComposeYAML("named", cwd))

	sel, err := compose.SelectMuxProject(compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Assert(t, sel.Spec != nil && sel.Spec.Mux != nil)
	assert.Equal(t, sel.Project, "named")
}

// Two named mux projects both associated with the cwd are ambiguous.
func TestSelectMuxProject_AmbiguousNamed(t *testing.T) {
	composeDir, cwd := muxTestEnv(t)
	writeFile(t, filepath.Join(composeDir, "alpha.yaml"), muxComposeYAML("alpha", cwd))
	writeFile(t, filepath.Join(composeDir, "beta.yaml"), muxComposeYAML("beta", cwd))

	_, err := compose.SelectMuxProject(compose.NormalizeOpts{})
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "multiple composes associated"))
	assert.Assert(t, strings.Contains(err.Error(), "alpha"))
	assert.Assert(t, strings.Contains(err.Error(), "beta"))
}

// A cwd mux file and an associated named mux project together are ambiguous.
func TestSelectMuxProject_AmbiguousCwdFileAndNamed(t *testing.T) {
	composeDir, cwd := muxTestEnv(t)
	writeFile(t, filepath.Join(cwd, "cmd-compose.yaml"), muxComposeYAML("cwdproj", ""))
	writeFile(t, filepath.Join(composeDir, "named.yaml"), muxComposeYAML("named", cwd))

	_, err := compose.SelectMuxProject(compose.NormalizeOpts{})
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "multiple composes associated"))
}

// No mux compose associated with the cwd is an error asking for -f, even when a
// mux project exists for some other directory.
func TestSelectMuxProject_NoneAssociated(t *testing.T) {
	composeDir, _ := muxTestEnv(t)
	other := t.TempDir()
	writeFile(t, filepath.Join(composeDir, "there.yaml"), muxComposeYAML("there", other))

	_, err := compose.SelectMuxProject(compose.NormalizeOpts{})
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "associated with this directory"))
	assert.Assert(t, strings.Contains(err.Error(), "-f"))
}
