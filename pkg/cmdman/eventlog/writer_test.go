package eventlog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestWriterAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)

	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	assert.NilError(t, w.Append(Event{Time: now, Type: EventTypeCreate, ID: "abc"}))
	assert.NilError(t, w.Append(Event{Time: now.Add(time.Second), Type: EventTypeStart, ID: "abc"}))

	data, err := os.ReadFile(path)
	assert.NilError(t, err)

	lines := splitLines(data)
	assert.Equal(t, len(lines), 2)
	var ev1 Event
	assert.NilError(t, json.Unmarshal(lines[0], &ev1))
	assert.Equal(t, ev1.Type, EventTypeCreate)
	assert.Equal(t, ev1.ID, "abc")
}

func TestWriterRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)
	w.SetMaxSize(256) // force quick rotation

	// Append enough events to trigger rotation at least once.
	for range 20 {
		assert.NilError(t, w.Append(Event{
			Time: time.Now().UTC(),
			Type: EventTypeRunning,
			ID:   "cmd",
		}))
	}

	// .1 archive must exist with a trailing rotation marker.
	archiveData, err := os.ReadFile(path + ArchiveSuffix)
	assert.NilError(t, err)
	archive := splitLines(archiveData)
	assert.Assert(t, len(archive) >= 1, "archive should have at least the marker")
	var lastArchive Event
	assert.NilError(t, json.Unmarshal(archive[len(archive)-1], &lastArchive))
	assert.Equal(t, lastArchive.Type, eventTypeRotation, "archive must end with rotation marker")

	// Active path must contain only post-rotation entries.
	activeData, err := os.ReadFile(path)
	assert.NilError(t, err)
	for _, ln := range splitLines(activeData) {
		var ev Event
		assert.NilError(t, json.Unmarshal(ln, &ev))
		assert.Assert(
			t,
			ev.Type != eventTypeRotation,
			"active file must not contain rotation marker mid-stream",
		)
	}
}

func TestWriterRotationRemovesOldArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)
	w.SetMaxSize(128)

	// First rotation produces .1 with content A.
	for range 10 {
		assert.NilError(t, w.Append(Event{Time: time.Now().UTC(), Type: EventTypeRunning, ID: "A"}))
	}
	firstArchive, err := os.ReadFile(path + ArchiveSuffix)
	assert.NilError(t, err)

	// Second rotation produces .1 with content B; old A must be gone.
	for range 10 {
		assert.NilError(t, w.Append(Event{Time: time.Now().UTC(), Type: EventTypeRunning, ID: "B"}))
	}
	secondArchive, err := os.ReadFile(path + ArchiveSuffix)
	assert.NilError(t, err)
	assert.Assert(
		t,
		string(firstArchive) != string(secondArchive),
		"archive content must change across rotations",
	)

	// No .2 archive: rotation only keeps a single archive.
	_, err = os.Stat(path + ".2")
	assert.Assert(t, os.IsNotExist(err), "rotation must not produce a .2 archive")
}

func TestWriterConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)

	const writers = 4
	const perWriter = 25
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			for range perWriter {
				_ = w.Append(Event{
					Time: time.Now().UTC(),
					Type: EventTypeRunning,
					ID:   string(rune('a' + i)),
				})
			}
		})
	}
	wg.Wait()

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	lines := splitLines(data)
	assert.Equal(t, len(lines), writers*perWriter)
	for _, ln := range lines {
		var ev Event
		assert.NilError(t, json.Unmarshal(ln, &ev), "line %q must be valid JSON", string(ln))
		assert.Equal(t, ev.Type, EventTypeRunning)
	}
}

func TestReaderFollowWithRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)
	w.SetMaxSize(256)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, err := NewReader(ctx, path, ReaderOption{
		Follow:       true,
		WatcherKind:  WatcherKindPoll,
		PollInterval: 20 * time.Millisecond,
	})
	assert.NilError(t, err)
	defer r.Close()

	// Drive enough events to trigger at least one rotation while the
	// reader is following.
	const n = 30
	go func() {
		for range n {
			_ = w.Append(Event{
				Time: time.Now().UTC(),
				Type: EventTypeRunning,
				ID:   "x",
			})
			time.Sleep(5 * time.Millisecond)
		}
	}()

	got := 0
	for got < n {
		select {
		case rec, ok := <-r.Events():
			if !ok {
				t.Fatalf("reader closed after %d events", got)
			}
			if rec.Err != nil {
				t.Fatalf("reader error: %v", rec.Err)
			}
			assert.Equal(t, rec.Event.Type, EventTypeRunning)
			got++
		case <-ctx.Done():
			t.Fatalf("timed out at %d events", got)
		}
	}
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
