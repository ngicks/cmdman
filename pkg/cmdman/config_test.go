package cmdman

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// configFileTestEnv installs a clean, isolated env for the config-file
// resolution tests so the developer's real $HOME/$XDG_CONFIG_HOME cannot
// leak in.
func configFileTestEnv(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(tmpHome, "run"))
	t.Setenv(ENV_CMDMAN_DATA_DIR, "")
	t.Setenv(ENV_CMDMAN_RUNTIME_DIR, "")
	t.Setenv(ENV_CMDMAN_CONF, filepath.Join(tmpHome, "no-such-config.json"))
	return tmpHome
}

func TestWithDefaults_ConfigFileLoaded(t *testing.T) {
	home := configFileTestEnv(t)
	confPath := filepath.Join(home, "cmdman-config.json")
	t.Setenv(ENV_CMDMAN_CONF, confPath)

	const dataFromConf = "/tmp/data-from-conf"
	const runtimeFromConf = "/tmp/runtime-from-conf"
	const scrollbackFromConf = 4096
	conf := []byte(`{
  "dataDir": "` + dataFromConf + `",
  "runtimeDir": "` + runtimeFromConf + `",
  "defaultScrollbackBytes": ` + itoa(scrollbackFromConf) + `
}`)
	assert.NilError(t, os.WriteFile(confPath, conf, 0o600))

	cfg, err := CmdmanConfig{}.WithDefaults()
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, dataFromConf)
	assert.Equal(t, cfg.RuntimeDir, runtimeFromConf)
	assert.Equal(t, cfg.DefaultScrollbackBytes, scrollbackFromConf)
}

func TestWithDefaults_ExplicitFieldsBeatConfigFile(t *testing.T) {
	home := configFileTestEnv(t)
	confPath := filepath.Join(home, "cmdman-config.json")
	t.Setenv(ENV_CMDMAN_CONF, confPath)

	conf := []byte(`{"dataDir": "/from-conf", "runtimeDir": "/from-conf"}`)
	assert.NilError(t, os.WriteFile(confPath, conf, 0o600))

	dataExplicit := filepath.Join(home, "explicit-data")
	runtimeExplicit := filepath.Join(home, "explicit-runtime")
	cfg, err := CmdmanConfig{
		DataDir:    dataExplicit,
		RuntimeDir: runtimeExplicit,
	}.WithDefaults()
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, dataExplicit)
	assert.Equal(t, cfg.RuntimeDir, runtimeExplicit)
}

func TestWithDefaults_EnvBeatsConfigFile(t *testing.T) {
	home := configFileTestEnv(t)
	confPath := filepath.Join(home, "cmdman-config.json")
	t.Setenv(ENV_CMDMAN_CONF, confPath)

	conf := []byte(`{"dataDir": "/from-conf", "runtimeDir": "/from-conf"}`)
	assert.NilError(t, os.WriteFile(confPath, conf, 0o600))

	dataEnv := filepath.Join(home, "env-data")
	runtimeEnv := filepath.Join(home, "env-runtime")
	t.Setenv(ENV_CMDMAN_DATA_DIR, dataEnv)
	t.Setenv(ENV_CMDMAN_RUNTIME_DIR, runtimeEnv)

	cfg, err := CmdmanConfig{}.WithDefaults()
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, dataEnv)
	assert.Equal(t, cfg.RuntimeDir, runtimeEnv)
}

func TestWithDefaults_MissingConfigFileIsOK(t *testing.T) {
	home := configFileTestEnv(t)
	// CMDMAN_CONF is set to a non-existent path; WithDefaults must not
	// fail.
	dataExplicit := filepath.Join(home, "data")
	runtimeExplicit := filepath.Join(home, "runtime")
	cfg, err := CmdmanConfig{
		DataDir:    dataExplicit,
		RuntimeDir: runtimeExplicit,
	}.WithDefaults()
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, dataExplicit)
}

func TestWithDefaults_MalformedConfigFileFails(t *testing.T) {
	home := configFileTestEnv(t)
	confPath := filepath.Join(home, "cmdman-config.json")
	t.Setenv(ENV_CMDMAN_CONF, confPath)

	assert.NilError(t, os.WriteFile(confPath, []byte("not json"), 0o600))

	_, err := CmdmanConfig{}.WithDefaults()
	assert.ErrorContains(t, err, "parse config file")
}

// itoa is a minimal int-to-decimal helper so the test file stays free of
// the "strconv" import (keeping its surface narrow and easy to read).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
