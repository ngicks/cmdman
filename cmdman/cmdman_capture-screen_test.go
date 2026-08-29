package cmdman

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
	"github.com/ngicks/cmdman/cmdman/store"
	"gotest.tools/v3/assert"
)

func TestParseCaptureLine(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    captureLineSpec
		wantErr bool
	}{
		{
			name: "empty leaves the end unset",
			in:   "",
			want: captureLineSpec{},
		},
		{
			name: "dash is the extreme end",
			in:   "-",
			want: captureLineSpec{set: true, extreme: true},
		},
		{
			name: "zero is the top visible row",
			in:   "0",
			want: captureLineSpec{set: true},
		},
		{
			name: "positive visible row",
			in:   "12",
			want: captureLineSpec{set: true, line: 12},
		},
		{
			name: "negative reaches into history",
			in:   "-5",
			want: captureLineSpec{set: true, line: -5},
		},
		{
			name: "explicit plus sign",
			in:   "+3",
			want: captureLineSpec{set: true, line: 3},
		},
		{
			name:    "not a number",
			in:      "top",
			wantErr: true,
		},
		{
			name:    "fractional",
			in:      "1.5",
			wantErr: true,
		},
		{
			name:    "trailing garbage",
			in:      "10lines",
			wantErr: true,
		},
		{
			name:    "double dash",
			in:      "--",
			wantErr: true,
		},
		{
			name:    "out of int32 range",
			in:      "9999999999",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCaptureLine("-S/--start-line", tt.in)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

func TestServiceCaptureScreen(t *testing.T) {
	dir := t.TempDir()
	appCfg := testConfig(t, dir)

	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)
	st, err := store.OpenStore(t.Context(), dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-capture-screen"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &model.CommandConfig{
		Argv:            []string{"/bin/sh", "-c", `printf "hello screen\r\n"; sleep 30`},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   model.RestartPolicyNo,
		Tty:             true,
		ScrollbackBytes: 4096,
		LogDriver:       model.DefaultLogDriver,
		CommandDir:      commandDir,
	}
	assert.NilError(t, st.InsertCommandConfig(id, "capture-screen", cfg))
	assert.NilError(t, store.WriteCommandConfig(cfg.CommandDir, cfg))
	assert.NilError(t, st.InsertCommandState(id, model.EventTypeCreated, &model.CommandState{}))

	svc := NewService(appCfg)
	defer svc.Close()

	// A command that has never run has no monitor holding its screen.
	_, err = svc.CaptureScreen(t.Context(), id, CaptureScreenRequest{})
	assert.ErrorContains(t, err, "is not running")

	pipeID := "test-capture-screen-pipe"
	pipeDir, err := appCfg.CommandDir(pipeID)
	assert.NilError(t, err)
	pipeCfg := *cfg
	pipeCfg.Tty = false
	pipeCfg.CommandDir = pipeDir
	assert.NilError(t, st.InsertCommandConfig(pipeID, "capture-screen-pipe", &pipeCfg))
	assert.NilError(t, store.WriteCommandConfig(pipeDir, &pipeCfg))
	assert.NilError(
		t,
		st.InsertCommandState(pipeID, model.EventTypeCreated, &model.CommandState{}),
	)

	_, err = svc.CaptureScreen(t.Context(), pipeID, CaptureScreenRequest{})
	assert.ErrorContains(t, err, "has no terminal screen")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- monitor.RunMonitor(ctx, id, appCfg, logger)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _, stateJSON, err := st.GetCommandState(id)
		assert.NilError(t, err)
		if state == model.EventTypeRunning && stateJSON.SocketPath != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The child's write races the state flip, so poll until the emulator has
	// consumed it rather than capturing once.
	var content []byte
	for time.Now().Before(deadline) {
		content, err = svc.CaptureScreen(ctx, id, CaptureScreenRequest{})
		assert.NilError(t, err)
		if strings.Contains(string(content), "hello screen") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	assert.Assert(t, strings.Contains(string(content), "hello screen"), "content = %q", content)

	// -E 0 keeps only the top visible row, which is the line just captured.
	first, err := svc.CaptureScreen(ctx, id, CaptureScreenRequest{EndLine: "0"})
	assert.NilError(t, err)
	assert.Equal(t, string(first), "hello screen\n")

	// The alternate screen was never entered; -q turns that into empty output.
	_, err = svc.CaptureScreen(ctx, id, CaptureScreenRequest{AltScreen: true})
	assert.ErrorContains(t, err, "no alternate screen")

	quiet, err := svc.CaptureScreen(ctx, id, CaptureScreenRequest{AltScreen: true, Quiet: true})
	assert.NilError(t, err)
	assert.Equal(t, len(quiet), 0)

	cancel()
	assert.NilError(t, <-runErrCh)
}
