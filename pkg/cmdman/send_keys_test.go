package cmdman

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"gotest.tools/v3/assert"
)

func TestEncodeSendKeys(t *testing.T) {
	tests := []struct {
		name    string
		req     SendKeysRequest
		want    []byte
		wantErr bool
	}{
		{
			name: "literal text",
			req:  SendKeysRequest{Keys: []string{"hello"}},
			want: []byte("hello"),
		},
		{
			name: "named keys",
			req:  SendKeysRequest{Keys: []string{"Space", "Tab", "Enter"}},
			want: []byte{' ', '\t', '\r'},
		},
		{
			name: "tmux ctrl key",
			req:  SendKeysRequest{Keys: []string{"C-c"}},
			want: []byte{0x03},
		},
		{
			name: "caret ctrl shorthand",
			req:  SendKeysRequest{Keys: []string{"^c"}},
			want: []byte{0x03},
		},
		{
			name: "meta key",
			req:  SendKeysRequest{Keys: []string{"M-x"}},
			want: []byte{0x1b, 'x'},
		},
		{
			name: "special key sequence",
			req:  SendKeysRequest{Keys: []string{"Up"}},
			want: []byte("\x1b[A"),
		},
		{
			name: "shift tab alias",
			req:  SendKeysRequest{Keys: []string{"S-Tab"}},
			want: []byte("\x1b[Z"),
		},
		{
			name: "literal mode",
			req:  SendKeysRequest{Keys: []string{"Enter"}, Literal: true},
			want: []byte("Enter"),
		},
		{
			name: "hex mode",
			req:  SendKeysRequest{Keys: []string{"41", "0d"}, Hex: true},
			want: []byte{'A', '\r'},
		},
		{
			name: "repeat count",
			req:  SendKeysRequest{Keys: []string{"A"}, RepeatCount: 3},
			want: []byte("AAA"),
		},
		{
			name:    "invalid hex byte",
			req:     SendKeysRequest{Keys: []string{"gg"}, Hex: true},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EncodeSendKeys(tt.req)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestServiceSendKeys(t *testing.T) {
	dir := t.TempDir()
	appCfg := CmdmanConfig{
		DataDir:            dir,
		RuntimeDir:         dir,
		DefaultWorkingDir:  dir,
		DefaultEnvironment: testEnv(),
	}
	appCfg, err := appCfg.WithDefaults()
	assert.NilError(t, err)

	dbPath, err := appCfg.DBPath()
	assert.NilError(t, err)
	st, err := store.OpenStore(dbPath, true)
	assert.NilError(t, err)
	defer st.Close()

	id := "test-send-keys"
	commandDir, err := appCfg.CommandDir(id)
	assert.NilError(t, err)
	cfg := &store.CommandConfigJSON{
		Argv: []string{
			"/bin/sh",
			"-c",
			`read line; printf "<%s>" "$line"; sleep 1`,
		},
		Dir:             dir,
		Env:             testEnv(),
		RestartPolicy:   store.RestartPolicyNo,
		ScrollbackBytes: 4096,
		LogDriver:       store.DefaultLogDriver,
		CommandDir:      commandDir,
	}

	assert.NilError(t, st.InsertCommandConfig(id, "send-keys", cfg))
	assert.NilError(t, cfg.Write())
	assert.NilError(t, st.InsertCommandState(id, store.StateCreated, &store.CommandStateJSON{}))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- RunMonitor(ctx, id, appCfg, logger)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, _, stateJSON, err := st.GetCommandState(id)
		assert.NilError(t, err)
		if state == store.StateRunning && stateJSON.SocketPath != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	sessionSvc := NewService(appCfg)
	session, err := sessionSvc.OpenAttachSession(ctx, id)
	assert.NilError(t, err)
	defer session.Close()

	sendSvc := NewService(appCfg)
	assert.NilError(t, sendSvc.SendKeys(ctx, id, SendKeysRequest{
		Keys: []string{"hello world", "Enter"},
	}))

	received := make(chan string, 1)
	go func() {
		var b strings.Builder
		for {
			data, err := session.Recv()
			if err != nil {
				received <- b.String()
				return
			}
			b.Write(data)
			if strings.Contains(b.String(), "<hello world>") {
				received <- b.String()
				return
			}
		}
	}()

	select {
	case out := <-received:
		assert.Assert(t, strings.Contains(out, "<hello world>"), "output = %q", out)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for command output")
	}

	cancel()
	assert.NilError(t, <-runErrCh)
}
