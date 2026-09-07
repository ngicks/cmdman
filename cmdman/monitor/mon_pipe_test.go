package monitor

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ngicks/cmdman/cmdman/logdriver/k8sfile"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"
)

// A non-TTY run reads two pipes of its own, so which stream a chunk belongs to
// is the run's own bookkeeping rather than something os/exec keeps apart for
// it. Both streams still have to reach the log, each under its own tag.
func TestMonitorPipeRunTagsBothStreams(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-monitor-pipe-stream-tags"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", "echo pipe-out; echo pipe-err >&2"},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m, err := newMonitor(t.Context(), id, appCfg, logger)
	assert.NilError(t, err)
	defer m.Close()

	code, err := m.runOnce(t.Context())
	assert.NilError(t, err)
	assert.Equal(t, code, 0)

	// runOnce closes the log writer as it returns, so everything the run wrote
	// is on disk by now.
	consoleLog, err := os.ReadFile(filepath.Join(commandDir, k8sfile.DefaultLogFileName))
	assert.NilError(t, err)
	assert.Assert(
		t,
		logHasTaggedLine(string(consoleLog), "stdout", "pipe-out"),
		"log: %q",
		string(consoleLog),
	)
	assert.Assert(
		t,
		logHasTaggedLine(string(consoleLog), "stderr", "pipe-err"),
		"log: %q",
		string(consoleLog),
	)
}

// logHasTaggedLine reports whether the k8s-file log holds content under the
// given stream tag. An entry reads "<timestamp> <stream> <F|P> <content>".
func logHasTaggedLine(log, stream, content string) bool {
	for line := range strings.SplitSeq(strings.TrimRight(log, "\n"), "\n") {
		fields := strings.SplitN(line, " ", 4)
		if len(fields) == 4 && fields[1] == stream && strings.Contains(fields[3], content) {
			return true
		}
	}
	return false
}
