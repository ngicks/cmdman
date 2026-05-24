//go:build linux

package eventlog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"gotest.tools/v3/assert"
)

func runWatcher(t *testing.T, w Watcher) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return cancel
}

func TestInotifyWatcherFiresOnAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	// Pre-create the file so InotifyAddWatch on dir has somewhere to point.
	f, err := os.Create(path)
	assert.NilError(t, err)
	_ = f.Close()

	w, err := NewWatcher(WatcherKindInotify, path, 0)
	assert.NilError(t, err)
	runWatcher(t, w)

	// Drain the initial wake-up that watchers emit on start.
	select {
	case <-w.Events():
	case <-time.After(time.Second):
		t.Fatal("expected initial wake-up token")
	}

	// Appending should produce another token.
	go func() {
		f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
		_, _ = f.Write([]byte("hello\n"))
		_ = f.Close()
	}()

	select {
	case <-w.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("expected inotify wake-up after append")
	}
}

func TestInotifyWatcherFiresOnRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	wr, err := NewWriter(path)
	assert.NilError(t, err)
	wr.maxSize = 128

	// Seed the file so inotify on the dir has something to track.
	assert.NilError(t, wr.Append(model.Event{Time: time.Now().UTC(), Type: model.EventTypeCreate, ID: "x"}))

	w, err := NewWatcher(WatcherKindInotify, path, 0)
	assert.NilError(t, err)
	runWatcher(t, w)

	// Drain initial wake-up.
	select {
	case <-w.Events():
	case <-time.After(time.Second):
	}

	// Drive enough events to force a rotation.
	for range 20 {
		assert.NilError(
			t,
			wr.Append(model.Event{Time: time.Now().UTC(), Type: model.EventTypeRunning, ID: "x"}),
		)
	}

	// At least one further wake-up should arrive (rename + create on the
	// active basename both trigger inotify on the parent dir).
	select {
	case <-w.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("expected inotify wake-up after rotation")
	}
}
