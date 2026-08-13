package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/eventlog"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

// configTestEnv installs a clean, isolated env for the load tests so the
// developer's real $HOME / $XDG_* / $CMDMAN_* cannot leak in. $CMDMAN_CONF
// points at a file that does not exist, so a test that wants a config file
// writes one and re-points it.
func configTestEnv(t *testing.T) string {
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

// writeConf writes a config file at path and points $CMDMAN_CONF at it.
func writeConf(t *testing.T, path, content string) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NilError(t, os.WriteFile(path, []byte(content), 0o600))
	t.Setenv(ENV_CMDMAN_CONF, path)
}

func TestDefaultConfig(t *testing.T) {
	home := configTestEnv(t)

	cfg, err := DefaultConfig()
	assert.NilError(t, err)

	cwd, err := os.Getwd()
	assert.NilError(t, err)

	assert.Equal(t, cfg.DataDir, filepath.Join(home, ".local", "share", "cmdman"))
	assert.Equal(t, cfg.RuntimeDir, filepath.Join(home, "run", "cmdman"))
	assert.Equal(t, cfg.DefaultWorkingDir, cwd)
	assert.Assert(t, len(cfg.DefaultEnvironment) > 0)
	assert.Equal(t, cfg.DefaultScrollbackBytes, store.DefaultScrollbackBytes)
	assert.Equal(t, cfg.DefaultLogDriver, model.DefaultLogDriver)
	assert.Equal(t, cfg.EventWatcherKind, defaultEventWatcherKind())
	assert.Assert(t, cfg.DefaultHooks == nil)
	assert.Equal(t, cfg.DefaultFrame, "")
	assert.NilError(t, cfg.Validate())
}

func TestPartialConfig_Apply(t *testing.T) {
	base := Config{
		DataDir:                "/base/data",
		RuntimeDir:             "/base/runtime",
		DefaultWorkingDir:      "/base/wd",
		DefaultEnvironment:     []string{"A=1"},
		DefaultScrollbackBytes: 1024,
		DefaultLogDriver:       logdriver.LogDriver("k8s-file"),
		EventWatcherKind:       eventlog.WatcherKindPoll,
		DefaultHooks: model.HookSet{
			model.HookEventBell:  {Action: model.HookActionBlock},
			model.HookEventTitle: {Action: model.HookActionPassthrough},
		},
	}

	t.Run("absent fields leave the base", func(t *testing.T) {
		got := PartialConfig{}.Apply(base)
		assert.DeepEqual(t, got, base)
	})

	t.Run("scalars overwrite", func(t *testing.T) {
		dataDir := "/from/partial"
		frame := "dev"
		got := PartialConfig{DataDir: &dataDir, DefaultFrame: &frame}.Apply(base)
		assert.Equal(t, got.DataDir, dataDir)
		assert.Equal(t, got.DefaultFrame, frame)
		assert.Equal(t, got.RuntimeDir, base.RuntimeDir)
	})

	t.Run("explicit zero overwrites", func(t *testing.T) {
		zero := 0
		got := PartialConfig{DefaultScrollbackBytes: &zero}.Apply(base)
		assert.Equal(t, got.DefaultScrollbackBytes, 0)
	})

	t.Run("slice overwrites wholesale", func(t *testing.T) {
		got := PartialConfig{DefaultEnvironment: []string{"B=2"}}.Apply(base)
		assert.DeepEqual(t, got.DefaultEnvironment, []string{"B=2"})
	})

	t.Run("map deep-merges by key", func(t *testing.T) {
		got := PartialConfig{
			DefaultHooks: model.HookSet{
				model.HookEventBell: {
					Action: model.HookActionPassthrough,
					Exec:   []string{"notify-send"},
				},
				model.HookEventNotification: {Action: model.HookActionBlock},
			},
		}.Apply(base)

		assert.DeepEqual(t, got.DefaultHooks, model.HookSet{
			// overwritten by the incoming layer
			model.HookEventBell: {
				Action: model.HookActionPassthrough,
				Exec:   []string{"notify-send"},
			},
			// kept from the base
			model.HookEventTitle: {Action: model.HookActionPassthrough},
			// added by the incoming layer
			model.HookEventNotification: {Action: model.HookActionBlock},
		})
		// The base's own map must not be touched.
		assert.DeepEqual(t, base.DefaultHooks, model.HookSet{
			model.HookEventBell:  {Action: model.HookActionBlock},
			model.HookEventTitle: {Action: model.HookActionPassthrough},
		})
	})
}

func TestLoad_ConfigFileBeatsDefaults(t *testing.T) {
	home := configTestEnv(t)

	const dataFromConf = "/tmp/data-from-conf"
	const runtimeFromConf = "/tmp/runtime-from-conf"
	const scrollbackFromConf = 4096
	writeConf(t, filepath.Join(home, "cmdman-config.json"), `{
  "data_dir": "`+dataFromConf+`",
  "runtime_dir": "`+runtimeFromConf+`",
  "default_working_dir": "`+home+`",
  "default_scrollback_bytes": `+strconv.Itoa(scrollbackFromConf)+`,
  "default_log_driver": "none",
  "event_watcher_kind": "poll",
  "default_frame": "dev"
}`)

	cfg, err := Load("")
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, dataFromConf)
	assert.Equal(t, cfg.RuntimeDir, runtimeFromConf)
	assert.Equal(t, cfg.DefaultWorkingDir, home)
	assert.Equal(t, cfg.DefaultScrollbackBytes, scrollbackFromConf)
	assert.Equal(t, cfg.DefaultLogDriver, logdriver.LogDriver("none"))
	assert.Equal(t, cfg.EventWatcherKind, eventlog.WatcherKindPoll)
	assert.Equal(t, cfg.DefaultFrame, "dev")
}

func TestLoad_EnvBeatsConfigFile(t *testing.T) {
	home := configTestEnv(t)
	writeConf(t, filepath.Join(home, "cmdman-config.json"),
		`{"data_dir": "/from-conf", "runtime_dir": "/from-conf"}`)

	dataEnv := filepath.Join(home, "env-data")
	runtimeEnv := filepath.Join(home, "env-runtime")
	t.Setenv(ENV_CMDMAN_DATA_DIR, dataEnv)
	t.Setenv(ENV_CMDMAN_RUNTIME_DIR, runtimeEnv)

	cfg, err := Load("")
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, dataEnv)
	assert.Equal(t, cfg.RuntimeDir, runtimeEnv)
}

// An env var that exists but is empty means "unset", the behavior every caller
// that clears a variable with t.Setenv(k, "") depends on.
func TestLoad_EmptyEnvVarIsAbsent(t *testing.T) {
	home := configTestEnv(t)
	writeConf(t, filepath.Join(home, "cmdman-config.json"),
		`{"data_dir": "/from-conf", "runtime_dir": "/from-conf"}`)

	t.Setenv(ENV_CMDMAN_DATA_DIR, "")

	cfg, err := Load("")
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, "/from-conf")
}

func TestLoad_FlagPathBeatsEnv(t *testing.T) {
	home := configTestEnv(t)
	writeConf(t, filepath.Join(home, "env-config.json"),
		`{"data_dir": "/from-env-conf", "runtime_dir": "/from-env-conf"}`)

	flagPath := filepath.Join(home, "flag-config.json")
	assert.NilError(t, os.WriteFile(
		flagPath,
		[]byte(`{"data_dir": "/from-flag-conf", "runtime_dir": "/from-flag-conf"}`),
		0o600,
	))

	cfg, err := Load(flagPath)
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, "/from-flag-conf")
}

// A process invoked with a minimal environment - no $HOME, no config file, only
// the CMDMAN_* dirs - must still load: the home-derived default is skipped, not
// fatal. The events e2e drives the binary exactly that way.
func TestLoad_WithoutHomeDir(t *testing.T) {
	t.Run("dirs from the environment", func(t *testing.T) {
		home := configTestEnv(t)
		t.Setenv("HOME", "")
		t.Setenv(ENV_CMDMAN_CONF, "")
		t.Setenv(ENV_CMDMAN_DATA_DIR, filepath.Join(home, "data"))
		t.Setenv(ENV_CMDMAN_RUNTIME_DIR, filepath.Join(home, "run"))

		cfg, err := Load("")
		assert.NilError(t, err)
		assert.Equal(t, cfg.DataDir, filepath.Join(home, "data"))
	})

	t.Run("nothing supplies the data dir", func(t *testing.T) {
		configTestEnv(t)
		t.Setenv("HOME", "")
		t.Setenv(ENV_CMDMAN_CONF, "")

		_, err := Load("")
		assert.ErrorContains(t, err, "data dir is empty")
	})
}

func TestLoad_MissingConfigFileIsOK(t *testing.T) {
	home := configTestEnv(t)

	cfg, err := Load("")
	assert.NilError(t, err)
	assert.Equal(t, cfg.DataDir, filepath.Join(home, ".local", "share", "cmdman"))
}

func TestLoad_MalformedConfigFileFails(t *testing.T) {
	home := configTestEnv(t)
	writeConf(t, filepath.Join(home, "cmdman-config.json"), "not json")

	_, err := Load("")
	assert.ErrorContains(t, err, "parse config")
}

func TestLoad_DefaultHooksFromConfigFile(t *testing.T) {
	home := configTestEnv(t)
	writeConf(t, filepath.Join(home, "cmdman-config.json"), `{
  "default_hooks": {
    "bell": {"action": "block", "exec": ["notify-send", "bell"]},
    "title": {"action": "passthrough"}
  }
}`)

	cfg, err := Load("")
	assert.NilError(t, err)
	assert.DeepEqual(t, cfg.DefaultHooks, model.HookSet{
		model.HookEventBell: {
			Action: model.HookActionBlock,
			Exec:   []string{"notify-send", "bell"},
		},
		model.HookEventTitle: {Action: model.HookActionPassthrough},
	})
}

func TestLoad_RejectsUnknownHookEvent(t *testing.T) {
	home := configTestEnv(t)
	writeConf(t, filepath.Join(home, "cmdman-config.json"),
		`{"default_hooks": {"bel": {"action": "block"}}}`)

	_, err := Load("")
	assert.ErrorContains(t, err, `unknown event "bel"`)
}

func TestLoad_RejectsInvalidWatcherKind(t *testing.T) {
	home := configTestEnv(t)
	writeConf(t, filepath.Join(home, "cmdman-config.json"),
		`{"event_watcher_kind": "epoll"}`)

	_, err := Load("")
	assert.ErrorContains(t, err, "invalid event watcher kind")
}

// The config subdirectories track $CMDMAN_CONF, and fall back to the user
// config dir. They never see the --config flag: their callers sit deep inside
// compose / frame discovery.
func TestConfigSubDirs(t *testing.T) {
	t.Run("beside $CMDMAN_CONF", func(t *testing.T) {
		home := configTestEnv(t)
		conf := filepath.Join(home, "etc", "config.json")
		t.Setenv(ENV_CMDMAN_CONF, conf)

		composeDir, err := ComposeConfigDir()
		assert.NilError(t, err)
		assert.Equal(t, composeDir, filepath.Join(home, "etc", "compose"))

		frameDir, err := FrameConfigDir()
		assert.NilError(t, err)
		assert.Equal(t, frameDir, filepath.Join(home, "etc", "frame"))
	})

	t.Run("under the user config dir", func(t *testing.T) {
		home := configTestEnv(t)
		t.Setenv(ENV_CMDMAN_CONF, "")
		xdg := filepath.Join(home, "xdg-config")
		t.Setenv("XDG_CONFIG_HOME", xdg)

		composeDir, err := ComposeConfigDir()
		assert.NilError(t, err)
		assert.Equal(t, composeDir, filepath.Join(xdg, "cmdman", "compose"))
	})
}

func TestWithHookEventEnv(t *testing.T) {
	env := WithHookEventEnv(
		[]string{
			"PATH=/bin",
			ENV_CMDMAN_CMD_ID + "=cmd-1",
			// A command that exported its own hook variables must not be able
			// to lie to the hook about what happened.
			ENV_CMDMAN_HOOK_EVENT + "=bogus",
			ENV_CMDMAN_HOOK_TITLE + "=bogus",
			ENV_CMDMAN_HOOK_BODY + "=bogus",
		},
		HookEventEnv{Event: model.HookEventNotification, Title: "Agent", Body: "needs input"},
	)
	assert.DeepEqual(t, env, []string{
		"PATH=/bin",
		ENV_CMDMAN_CMD_ID + "=cmd-1",
		ENV_CMDMAN_HOOK_EVENT + "=notification",
		ENV_CMDMAN_HOOK_TITLE + "=Agent",
		ENV_CMDMAN_HOOK_BODY + "=needs input",
	})
}
