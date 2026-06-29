package compose_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"github.com/ngicks/go-common/contextkey"
)

// testdataPath returns the absolute path to a file in the testdata directory.
func testdataPath(name string) string {
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return p
}

// normalizeFromFile is a helper: discovers + normalizes from an explicit file path.
func normalizeFromFile(
	t *testing.T,
	filePath string,
	opts compose.NormalizeOpts,
) (compose.ComposeSpec, error) {
	t.Helper()
	opts.File = filePath
	raw, err := compose.DecodeFile(filePath)
	if err != nil {
		return compose.ComposeSpec{}, err
	}
	return compose.Normalize(context.Background(), filePath, raw, opts)
}

// ---- YAML parsing -----------------------------------------------------------

func TestYAMLParsing_StringArgs(t *testing.T) {
	raw, err := compose.DecodeFile(testdataPath("basic.yaml"))
	assert.NilError(t, err)
	apiCmd := raw.Commands["api"]
	assert.DeepEqual(t, apiCmd.Args, []string{"go", "run", "./cmd/api"})
}

func TestYAMLParsing_WorkerAfter(t *testing.T) {
	raw, err := compose.DecodeFile(testdataPath("basic.yaml"))
	assert.NilError(t, err)
	workerCmd := raw.Commands["worker"]
	assert.Assert(t, workerCmd.After != nil)
	assert.Equal(t, len(workerCmd.After), 1)
}

// ---- auto_remove ignored ----------------------------------------------------

func TestAutoRemoveIgnored(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("autoremove.yaml"), compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Equal(t, len(spec.Commands), 1)
}

// ---- unknown field capture --------------------------------------------------

func TestUnknownFieldsCaptured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	content := `
name: unknownfields-test
some_top_level: 1
commands:
  myapp:
    args:
      - echo
      - hello
    auto_remove: true
`
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))

	raw, err := compose.DecodeFile(path)
	assert.NilError(t, err)

	_, topOK := raw.Unknown["some_top_level"]
	assert.Assert(t, topOK, "expected top-level unknown key to be captured")

	_, cmdOK := raw.Commands["myapp"].Unknown["auto_remove"]
	assert.Assert(t, cmdOK, "expected command-level unknown key to be captured")
}

func TestUnknownFieldWarningUsesContextLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd-compose.yaml")
	content := `
name: ctxlogger-test
commands:
  myapp:
    args: [echo, hi]
    auto_remove: true
`
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o644))

	raw, err := compose.DecodeFile(path)
	assert.NilError(t, err)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	ctx := contextkey.WithSlogLogger(context.Background(), logger)

	_, err = compose.Normalize(ctx, path, raw, compose.NormalizeOpts{})
	assert.NilError(t, err)

	out := buf.String()
	assert.Assert(t, cmp.Contains(out, "ignoring unrecognized command field"))
	assert.Assert(t, cmp.Contains(out, "auto_remove"))
}

// ---- project name -----------------------------------------------------------

func TestProjectNameFromYAML(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("basic.yaml"), compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Equal(t, spec.Project, "testproject")
}

func TestProjectNameCLIOverridesYAML(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("basic.yaml"), compose.NormalizeOpts{
		ProjectName: "override",
	})
	assert.NilError(t, err)
	assert.Equal(t, spec.Project, "override")
}

func TestProjectNameMandatory(t *testing.T) {
	_, err := normalizeFromFile(t, testdataPath("noname.yaml"), compose.NormalizeOpts{})
	assert.Assert(t, err != nil, "expected error when no project name")
	assert.Assert(t, cmp.Contains(err.Error(), "project name is required"))
}

func TestProjectNameFromCLI_NoYAMLName(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("noname.yaml"), compose.NormalizeOpts{
		ProjectName: "fromcli",
	})
	assert.NilError(t, err)
	assert.Equal(t, spec.Project, "fromcli")
}

// ---- WorkDir default --------------------------------------------------------

func TestWorkDirDefaultIsCWD(t *testing.T) {
	cwd, err := os.Getwd()
	assert.NilError(t, err)

	spec, err := normalizeFromFile(t, testdataPath("basic.yaml"), compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Equal(t, spec.WorkDir, filepath.Clean(cwd))
}

func TestWorkDirOverride(t *testing.T) {
	spec, err := normalizeFromFile(t, testdataPath("basic.yaml"), compose.NormalizeOpts{
		WorkDir: "/tmp",
	})
	assert.NilError(t, err)
	assert.Equal(t, spec.WorkDir, "/tmp")
}

// ---- workdir hash determinism -----------------------------------------------

func TestWorkdirHashDeterminism(t *testing.T) {
	cwd, _ := os.Getwd()
	// ./a and a relative to same CWD should produce identical absolute paths.
	p1 := filepath.Clean(filepath.Join(cwd, "a"))
	p2 := filepath.Clean(filepath.Join(cwd, "./a"))
	assert.Equal(t, p1, p2, "canonical paths should be equal")

	// Use the exported helper via two separate normalizations with explicit WorkDir.
	spec1, err := normalizeFromFile(
		t,
		testdataPath("basic.yaml"),
		compose.NormalizeOpts{WorkDir: p1},
	)
	assert.NilError(t, err)
	spec2, err := normalizeFromFile(
		t,
		testdataPath("basic.yaml"),
		compose.NormalizeOpts{WorkDir: p2},
	)
	assert.NilError(t, err)

	// Both should have the same WorkDir and the same generated names.
	assert.Equal(t, spec1.WorkDir, spec2.WorkDir)
	assert.Equal(t, spec1.Commands[0].GeneratedName, spec2.Commands[0].GeneratedName)
}

func TestWorkdirHashDifferentPaths(t *testing.T) {
	spec1, err := normalizeFromFile(
		t,
		testdataPath("basic.yaml"),
		compose.NormalizeOpts{WorkDir: "/tmp/proj1"},
	)
	assert.NilError(t, err)
	spec2, err := normalizeFromFile(t, testdataPath("basic.yaml"),
		compose.NormalizeOpts{WorkDir: "/tmp/proj2"})
	assert.NilError(t, err)

	// Different WorkDirs must produce different generated names.
	assert.Assert(t, spec1.Commands[0].GeneratedName != spec2.Commands[0].GeneratedName)
}

// ---- generated name escaping ------------------------------------------------

func TestGeneratedNameEscaping(t *testing.T) {
	// Project "dev-session", command "claude-cli" → ...-dev--session-claude--cli
	// Project "a-b", command "c" → ...-a--b-c
	// Project "a", command "b-c" → ...-a-b--c
	// These must be distinct.

	nameAB_C := compose.GenerateName("000000000000", "a-b", "c")
	nameA_BC := compose.GenerateName("000000000000", "a", "b-c")
	assert.Assert(t, nameAB_C != nameA_BC,
		"distinct (project,command) pairs must yield distinct names: %q vs %q", nameAB_C, nameA_BC)

	// Verify the escaping pattern.
	assert.Assert(t, cmp.Contains(nameAB_C, "a--b"))
	assert.Assert(t, cmp.Contains(nameA_BC, "b--c"))
}

func TestGeneratedNameStructure(t *testing.T) {
	// project "devsession", command "claude", hash "a3f9b2c1e8d4"
	// expected: "a3f9b2c1e8d4-devsession-claude"
	got := compose.GenerateName("a3f9b2c1e8d4", "devsession", "claude")
	assert.Equal(t, got, "a3f9b2c1e8d4-devsession-claude")
}

// ---- hash output format -----------------------------------------------------

func TestHashFormat(t *testing.T) {
	cmd := compose.Command{
		Name:          "api",
		Args:          []string{"go", "run", "./cmd/api"},
		Dir:           "/work",
		RestartPolicy: model.RestartPolicy("on-failure"),
	}
	h, err := compose.Hash(cmd)
	assert.NilError(t, err)
	assert.Assert(
		t,
		strings.HasPrefix(h, "sha256:"),
		"hash must start with sha256: prefix, got: %s",
		h,
	)
	// Full digest: "sha256:" + 64 hex chars
	assert.Equal(t, len(h), len("sha256:")+64)
}

func TestHashStability(t *testing.T) {
	cmd := compose.Command{
		Name:          "api",
		Args:          []string{"go", "run", "./cmd/api"},
		Dir:           "/work",
		RestartPolicy: model.RestartPolicy("on-failure"),
	}
	h1, err := compose.Hash(cmd)
	assert.NilError(t, err)
	h2, err := compose.Hash(cmd)
	assert.NilError(t, err)
	assert.Equal(t, h1, h2)
}

func TestHashChangesOnFieldChange(t *testing.T) {
	cmd := compose.Command{
		Name: "api",
		Args: []string{"go", "run", "./cmd/api"},
		Dir:  "/work",
	}
	h1, err := compose.Hash(cmd)
	assert.NilError(t, err)

	cmd.Args = append(cmd.Args, "--verbose")
	h2, err := compose.Hash(cmd)
	assert.NilError(t, err)

	assert.Assert(t, h1 != h2, "hash must change when args change")
}

func TestHashChangesOnImportHostEnv(t *testing.T) {
	cmd := compose.Command{
		Name:          "api",
		Args:          []string{"go", "run", "./cmd/api"},
		Dir:           "/work",
		ImportHostEnv: true,
	}
	h1, err := compose.Hash(cmd)
	assert.NilError(t, err)

	cmd.ImportHostEnv = false
	h2, err := compose.Hash(cmd)
	assert.NilError(t, err)

	assert.Assert(t, h1 != h2, "hash must change when import_host_env changes")
}

func TestImportHostEnvDefaultsTrue(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: import-host-env-test
commands:
  default-on:
    args: [echo, a]
  explicit-off:
    args: [echo, b]
    import_host_env: false
  explicit-on:
    args: [echo, c]
    import_host_env: true
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(context.Background(), yamlPath, raw, compose.NormalizeOpts{})
	assert.NilError(t, err)

	byName := make(map[string]compose.Command, len(spec.Commands))
	for _, c := range spec.Commands {
		byName[c.Name] = c
	}
	assert.Equal(t, byName["default-on"].ImportHostEnv, true)
	assert.Equal(t, byName["explicit-off"].ImportHostEnv, false)
	assert.Equal(t, byName["explicit-on"].ImportHostEnv, true)
}

// ---- reserved label rejection -----------------------------------------------

func TestReservedLabelRejection(t *testing.T) {
	// Write a temporary YAML with a reserved label.
	dir := t.TempDir()
	yamlContent := `
name: reserved-test
commands:
  app:
    args: [echo, hello]
    labels:
      cmdman.compose.project: user-override
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	_, err = compose.Normalize(context.Background(), yamlPath, raw, compose.NormalizeOpts{})
	assert.Assert(t, err != nil)
	assert.Assert(t, cmp.Contains(err.Error(), "reserved prefix"))
}

// ---- env_file loading -------------------------------------------------------

func TestEnvFileRequired_Missing(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: envfile-test
commands:
  app:
    args: [echo, hello]
    env_file:
      - path: missing.env
        required: true
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	_, err = compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.Assert(t, err != nil, "required missing env_file should error")
}

func TestEnvFileOptional_Missing(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: envfile-test
commands:
  app:
    args: [echo, hello]
    env_file:
      - path: missing.env
        required: false
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err, "optional missing env_file should not error")
	assert.Equal(t, len(spec.Commands[0].Env), 0)
}

func TestEnvFileDefaultRequired(t *testing.T) {
	dir := t.TempDir()
	// No `required:` field → defaults to true → error on missing.
	yamlContent := `
name: envfile-test
commands:
  app:
    args: [echo, hello]
    env_file:
      - path: missing.env
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	_, err = compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.Assert(
		t,
		err != nil,
		"absent required field should default to true and error on missing file",
	)
}

func TestEnvFileLoaded(t *testing.T) {
	dir := t.TempDir()
	envContent := "APP_KEY=secret\nDB_HOST=localhost\n"
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "app.env"), []byte(envContent), 0644))

	yamlContent := `
name: envfile-test
commands:
  app:
    args: [echo, hello]
    env_file:
      - path: app.env
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)

	envMap := envSliceToMap(spec.Commands[0].Env)
	assert.Equal(t, envMap["APP_KEY"], "secret")
	assert.Equal(t, envMap["DB_HOST"], "localhost")
}

// ---- interpolation ----------------------------------------------------------

func TestInterpolation_SimpleVar(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: interp-test
commands:
  app:
    args:
      - echo
      - ${MSG}
    env:
      - MSG=hello-world
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)
	assert.Equal(t, spec.Commands[0].Args[1], "hello-world")
}

func TestInterpolation_DefaultSyntax(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: interp-test
commands:
  app:
    args:
      - echo
      - ${GREETING:-hello}
    env: []
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	// Ensure GREETING is not set.
	os.Unsetenv("GREETING")
	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)
	assert.Equal(t, spec.Commands[0].Args[1], "hello")
}

func TestInterpolation_RequiredError(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: interp-test
commands:
  app:
    args:
      - echo
      - ${MUST_EXIST:?MUST_EXIST is required}
    env: []
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	os.Unsetenv("MUST_EXIST")
	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	_, err = compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.Assert(t, err != nil, "expected error for missing required variable")
	assert.Assert(t, cmp.Contains(err.Error(), "MUST_EXIST"))
}

// ---- env layering order -----------------------------------------------------

func TestEnvLayeringOrder(t *testing.T) {
	dir := t.TempDir()
	// env_file sets KEY=from-file; env: overrides with KEY=from-env
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "app.env"), []byte("KEY=from-file\n"), 0644))

	yamlContent := `
name: layer-test
commands:
  app:
    args: [echo, "${KEY}"]
    env_file:
      - path: app.env
    env:
      - KEY=from-env
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)

	// env: overrides env_file → KEY=from-env
	envMap := envSliceToMap(spec.Commands[0].Env)
	assert.Equal(t, envMap["KEY"], "from-env")
	// args interpolation sees the final env
	assert.Equal(t, spec.Commands[0].Args[1], "from-env")
}

// ---- within-WorkDir collision -----------------------------------------------

func TestWithinWorkdirCollision(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: myproject
commands:
  app:
    args: [echo, hello]
`
	file1 := filepath.Join(dir, "cmd-compose.yaml")
	file2 := filepath.Join(dir, "other-compose.yaml")
	assert.NilError(t, os.WriteFile(file1, []byte(yamlContent), 0644))
	assert.NilError(t, os.WriteFile(file2, []byte(yamlContent), 0644))

	// Build a spec from file1.
	raw1, err := compose.DecodeFile(file1)
	assert.NilError(t, err)
	spec1, err := compose.Normalize(
		context.Background(),
		file1,
		raw1,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)

	// Simulate an existing command entry owned by file2 for the same project.
	existing := buildExistingEntry("app-id", spec1.Commands[0].GeneratedName,
		map[string]string{
			compose.LabelWorkdir:    dir,
			compose.LabelProject:    "myproject",
			compose.LabelCommand:    "app",
			compose.LabelFile:       file2, // different file!
			compose.LabelVersion:    "1",
			compose.LabelConfigHash: "sha256:abc",
		})

	_, err = compose.ComputePlan(spec1, []store.CommandEntry{existing})
	assert.Assert(t, err != nil, "expected collision error")
	assert.Assert(t, cmp.Contains(err.Error(), "collision"))
}

// ---- orphan detection -------------------------------------------------------

func TestOrphanDetection(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: myproject
commands:
  app:
    args: [echo, hello]
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)

	// An orphan: same workdir+project, but command "old-cmd" not in desired spec.
	orphan := buildExistingEntry("orphan-id", "orphan-generated-name",
		map[string]string{
			compose.LabelWorkdir:    dir,
			compose.LabelProject:    "myproject",
			compose.LabelCommand:    "old-cmd",
			compose.LabelFile:       yamlPath,
			compose.LabelVersion:    "1",
			compose.LabelConfigHash: "sha256:old",
		})

	plan, err := compose.ComputePlan(spec, []store.CommandEntry{orphan})
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Orphans), 1)
	assert.Equal(t, plan.Orphans[0].Name, "orphan-generated-name")
}

// ---- reconciliation plan generation ----------------------------------------

func TestReconciliationPlan_Create(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: myproject
commands:
  app:
    args: [echo, hello]
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)

	// No existing commands → all should be ActionCreate.
	plan, err := compose.ComputePlan(spec, nil)
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Actions), 1)
	assert.Equal(t, plan.Actions[0].Kind, compose.ActionCreate)
	assert.Equal(t, plan.Actions[0].Desired.Name, "app")
}

func TestReconciliationPlan_Unchanged(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: myproject
commands:
  app:
    args: [echo, hello]
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)

	// Compute the real hash.
	h, err := compose.Hash(spec.Commands[0])
	assert.NilError(t, err)

	existing := buildExistingEntry("app-id", spec.Commands[0].GeneratedName,
		map[string]string{
			compose.LabelWorkdir:    dir,
			compose.LabelProject:    "myproject",
			compose.LabelCommand:    "app",
			compose.LabelFile:       yamlPath,
			compose.LabelVersion:    "1",
			compose.LabelScaleIndex: "1",
			compose.LabelConfigHash: h,
		})

	plan, err := compose.ComputePlan(spec, []store.CommandEntry{existing})
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Actions), 1)
	assert.Equal(t, plan.Actions[0].Kind, compose.ActionUnchanged)
}

func TestReconciliationPlan_Recreate(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: myproject
commands:
  app:
    args: [echo, hello]
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(),
		yamlPath,
		raw,
		compose.NormalizeOpts{WorkDir: dir},
	)
	assert.NilError(t, err)

	existing := buildExistingEntry("app-id", spec.Commands[0].GeneratedName,
		map[string]string{
			compose.LabelWorkdir:    dir,
			compose.LabelProject:    "myproject",
			compose.LabelCommand:    "app",
			compose.LabelFile:       yamlPath,
			compose.LabelVersion:    "1",
			compose.LabelScaleIndex: "1",
			compose.LabelConfigHash: "sha256:" + strings.Repeat("0", 64), // stale hash
		})

	plan, err := compose.ComputePlan(spec, []store.CommandEntry{existing})
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Actions), 1)
	assert.Equal(t, plan.Actions[0].Kind, compose.ActionRecreate)
}

func TestReconciliationPlan_ScaleExpandsInstances(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: myproject
commands:
  app:
    args: [echo, hello]
    scale: 3
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(), yamlPath, raw, compose.NormalizeOpts{WorkDir: dir})
	assert.NilError(t, err)
	assert.Equal(t, spec.Commands[0].Scale, 3)

	// No existing commands: three create actions, one per replica, each with a
	// distinct scale index and instance name.
	plan, err := compose.ComputePlan(spec, nil)
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Actions), 3)
	base := spec.Commands[0].GeneratedName
	for i, action := range plan.Actions {
		assert.Equal(t, action.Kind, compose.ActionCreate)
		assert.Equal(t, action.ScaleIndex, i+1)
		assert.Equal(t, action.InstanceName, compose.InstanceName(base, i+1))
	}
}

func TestReconciliationPlan_ScaleDownYieldsExcess(t *testing.T) {
	dir := t.TempDir()
	yamlContent := `
name: myproject
commands:
  app:
    args: [echo, hello]
    scale: 1
`
	yamlPath := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0644))

	raw, err := compose.DecodeFile(yamlPath)
	assert.NilError(t, err)
	spec, err := compose.Normalize(
		context.Background(), yamlPath, raw, compose.NormalizeOpts{WorkDir: dir})
	assert.NilError(t, err)

	h, err := compose.Hash(spec.Commands[0])
	assert.NilError(t, err)
	base := spec.Commands[0].GeneratedName

	mkEntry := func(idx int) store.CommandEntry {
		return buildExistingEntry(
			"id-"+compose.InstanceName(base, idx), compose.InstanceName(base, idx),
			map[string]string{
				compose.LabelWorkdir:    dir,
				compose.LabelProject:    "myproject",
				compose.LabelCommand:    "app",
				compose.LabelFile:       yamlPath,
				compose.LabelVersion:    "1",
				compose.LabelScaleIndex: strconv.Itoa(idx),
				compose.LabelConfigHash: h,
			})
	}
	// Two replicas exist but the desired scale is 1: replica 1 is unchanged,
	// replica 2 is surplus.
	plan, err := compose.ComputePlan(spec, []store.CommandEntry{mkEntry(1), mkEntry(2)})
	assert.NilError(t, err)
	assert.Equal(t, len(plan.Actions), 1)
	assert.Equal(t, plan.Actions[0].Kind, compose.ActionUnchanged)
	assert.Equal(t, len(plan.ExcessReplicas), 1)
	assert.Equal(t, plan.ExcessReplicas[0].Name, compose.InstanceName(base, 2))
}

// ---- file discovery ---------------------------------------------------------

func TestFileDiscovery_YAMLFirst(t *testing.T) {
	dir := t.TempDir()
	// Both files exist; .yaml takes priority.
	assert.NilError(
		t,
		os.WriteFile(
			filepath.Join(dir, "cmd-compose.yaml"),
			[]byte("name: yaml\ncommands: {}\n"),
			0644,
		),
	)
	assert.NilError(
		t,
		os.WriteFile(
			filepath.Join(dir, "cmd-compose.yml"),
			[]byte("name: yml\ncommands: {}\n"),
			0644,
		),
	)

	p, raw, err := compose.DiscoverFile(dir, compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Equal(t, filepath.Base(p), "cmd-compose.yaml")
	assert.Equal(t, raw.Name, "yaml")
}

func TestFileDiscovery_YMLFallback(t *testing.T) {
	dir := t.TempDir()
	assert.NilError(
		t,
		os.WriteFile(
			filepath.Join(dir, "cmd-compose.yml"),
			[]byte("name: yml\ncommands: {}\n"),
			0644,
		),
	)

	p, raw, err := compose.DiscoverFile(dir, compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Equal(t, filepath.Base(p), "cmd-compose.yml")
	assert.Equal(t, raw.Name, "yml")
}

func TestFileDiscovery_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, _, err := compose.DiscoverFile(dir, compose.NormalizeOpts{})
	assert.Assert(t, err != nil)
	assert.Assert(t, cmp.Contains(err.Error(), "no compose file found"))
}

// ---- helpers ----------------------------------------------------------------

func envSliceToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, entry := range env {
		k, v, _ := strings.Cut(entry, "=")
		m[k] = v
	}
	return m
}

func buildExistingEntry(id, name string, labels map[string]string) store.CommandEntry {
	return store.CommandEntry{
		ID:    id,
		Name:  name,
		State: "stopped",
		ConfigJSON: &model.CommandConfig{
			Argv:   []string{"echo", "hello"},
			Labels: labels,
		},
	}
}

func TestLoadOrProjectExplicitFileErrorDoesNotFallBack(t *testing.T) {
	t.Chdir(t.TempDir())

	_, err := compose.LoadOrProject(compose.NormalizeOpts{
		File:        "missing-compose.yaml",
		ProjectName: "existing-project",
	})
	if err == nil {
		t.Fatal("expected explicit compose file error")
	}
	if !strings.Contains(err.Error(), "missing-compose.yaml") {
		t.Fatalf("expected error to name explicit file, got: %v", err)
	}
}

func TestLoadOrProjectByCwdWithoutFileOrProjectName(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	assert.NilError(t, err)

	// No compose file and no --project-name: resolve by cwd (workdir) instead of
	// erroring, with an empty project that matches every command in the workdir.
	sel, err := compose.LoadOrProject(compose.NormalizeOpts{})
	assert.NilError(t, err)
	assert.Assert(t, sel.Spec == nil)
	assert.Equal(t, sel.Project, "")
	assert.Equal(t, sel.WorkDir, filepath.Clean(cwd))
}
