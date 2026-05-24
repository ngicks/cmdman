package cmdman

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver/k8sfile"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"gotest.tools/v3/assert"
)

func TestServiceLogsFollowNoDuplicatesAcrossStorageAndLive(t *testing.T) {
	dir, err := os.MkdirTemp("", "cm-logs-*")
	assert.NilError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	appCfg := CmdmanConfig{
		DataDir:            dir,
		RuntimeDir:         dir,
		DefaultWorkingDir:  dir,
		DefaultEnvironment: testEnv(),
	}
	appCfg, err = appCfg.WithDefaults()
	assert.NilError(t, err)
	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)

	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-logs-follow-bridge"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfigJSON{
		Argv: []string{"/bin/sh", "-c", strings.Join([]string{
			"i=1",
			"while [ $i -le 20 ]; do",
			"  printf 'bridge-line-%02d\\n' \"$i\"",
			"  i=$((i+1))",
			"  sleep 0.05",
			"done",
			"sleep 30",
		}, "\n")},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "logs-follow-bridge", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.StateCreated, &model.CommandStateJSON{}))
	assert.NilError(t, st.Close())

	monitorCtx, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	monitorErr := make(chan error, 1)
	go func() {
		logger := slog.New(slog.NewJSONHandler(
			os.Stderr,
			&slog.HandlerOptions{Level: slog.LevelWarn},
		))
		monitorErr <- RunMonitor(monitorCtx, id, appCfg, logger)
	}()
	t.Cleanup(func() {
		stopMonitor()
		select {
		case err := <-monitorErr:
			assert.NilError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for monitor shutdown")
		}
	})

	waitForCommandRunning(t, appCfg, id)
	waitForLogContent(t, filepath.Join(commandDir, k8sfile.DefaultLogFileName), "bridge-line-01")

	svc := NewService(appCfg)
	defer svc.Close()
	logCtx, cancelLogs := context.WithCancel(t.Context())
	defer cancelLogs()
	r, err := svc.Logs(logCtx, LogsRequest{IDOrName: id, Follow: true})
	assert.NilError(t, err)
	defer r.Close()

	got := make([]string, 0, 20)
	counts := map[string]int{}
	deadline := time.After(10 * time.Second)
	for len(got) < 20 {
		select {
		case rec, ok := <-r.Records():
			assert.Assert(t, ok, "logs reader closed before all records arrived")
			assert.NilError(t, rec.Err)
			line := strings.TrimSpace(string(rec.Line.Line))
			if line == "" {
				continue
			}
			got = append(got, line)
			counts[line]++
		case <-deadline:
			t.Fatalf("timed out waiting for 20 log records; got %v", got)
		}
	}

	for i := 1; i <= 20; i++ {
		want := fmt.Sprintf("bridge-line-%02d", i)
		assert.Equal(t, counts[want], 1, "unexpected count for %s in %v", want, got)
		assert.Equal(t, got[i-1], want)
	}
}

func waitForCommandRunning(t *testing.T, cfg CmdmanConfig, id string) {
	t.Helper()
	st, err := store.OpenStore(t.Context(), mustDBPath(t, cfg), true)
	assert.NilError(t, err)
	defer st.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _, stateJSON, err := st.GetCommandState(id)
		if err == nil && state == model.StateRunning && stateJSON.SocketPath != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for command %s to run", id)
}

func waitForLogContent(t *testing.T, path string, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in %s", want, path)
}

func mustDBPath(t *testing.T, cfg CmdmanConfig) string {
	t.Helper()
	dbPath, err := cfg.DBPath()
	assert.NilError(t, err)
	return dbPath
}
