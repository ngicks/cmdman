package eventlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"gotest.tools/v3/assert"
)

func TestWriterAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)

	now := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	assert.NilError(t, w.Append(model.Event{Time: now, Type: model.EventTypeCreated, ID: "abc"}))
	assert.NilError(t, w.Append(model.Event{Time: now.Add(time.Second), Type: model.EventTypeStarting, ID: "abc"}))

	data, err := os.ReadFile(path)
	assert.NilError(t, err)

	lines := splitLines(data)
	assert.Equal(t, len(lines), 2)
	var ev1 model.Event
	assert.NilError(t, json.Unmarshal(lines[0], &ev1))
	assert.Equal(t, ev1.Type, model.EventTypeCreated)
	assert.Equal(t, ev1.ID, "abc")
}

func TestWriterRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)
	w.maxSize = 256 // force quick rotation

	// Append enough events to trigger rotation at least once.
	for range 20 {
		assert.NilError(t, w.Append(model.Event{
			Time: time.Now().UTC(),
			Type: model.EventTypeRunning,
			ID:   "cmd",
		}))
	}

	// .1 archive must exist with a trailing rotation marker.
	archiveData, err := os.ReadFile(path + ArchiveSuffix)
	assert.NilError(t, err)
	archive := splitLines(archiveData)
	assert.Assert(t, len(archive) >= 1, "archive should have at least the marker")
	var lastArchive model.Event
	assert.NilError(t, json.Unmarshal(archive[len(archive)-1], &lastArchive))
	assert.Equal(t, lastArchive.Type, model.EventTypeRotation, "archive must end with rotation marker")

	// Active path must contain only post-rotation entries.
	activeData, err := os.ReadFile(path)
	assert.NilError(t, err)
	for _, ln := range splitLines(activeData) {
		var ev model.Event
		assert.NilError(t, json.Unmarshal(ln, &ev))
		assert.Assert(
			t,
			ev.Type != model.EventTypeRotation,
			"active file must not contain rotation marker mid-stream",
		)
	}
}

func TestWriterRotationRemovesOldArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)
	w.maxSize = 128

	// First rotation produces .1 with content A.
	for range 10 {
		assert.NilError(t, w.Append(model.Event{Time: time.Now().UTC(), Type: model.EventTypeRunning, ID: "A"}))
	}
	firstArchive, err := os.ReadFile(path + ArchiveSuffix)
	assert.NilError(t, err)

	// Second rotation produces .1 with content B; old A must be gone.
	for range 10 {
		assert.NilError(t, w.Append(model.Event{Time: time.Now().UTC(), Type: model.EventTypeRunning, ID: "B"}))
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
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "rotation must not produce a .2 archive")
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
				_ = w.Append(model.Event{
					Time: time.Now().UTC(),
					Type: model.EventTypeRunning,
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
		var ev model.Event
		assert.NilError(t, json.Unmarshal(ln, &ev), "line %q must be valid JSON", string(ln))
		assert.Equal(t, ev.Type, model.EventTypeRunning)
	}
}

func TestReaderFollowWithRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)
	w.maxSize = 1024

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	r, err := NewReader(path, ReaderOption{
		Follow:       true,
		WatcherKind:  WatcherKindPoll,
		PollInterval: 20 * time.Millisecond,
	})
	assert.NilError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	// Drive enough events to trigger at least one rotation while the
	// reader is following.
	const n = 30
	go func() {
		for range n {
			_ = w.Append(model.Event{
				Time: time.Now().UTC(),
				Type: model.EventTypeRunning,
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
			assert.Equal(t, rec.Event.Type, model.EventTypeRunning)
			got++
		case <-ctx.Done():
			t.Fatalf("timed out at %d events", got)
		}
	}
}

// TestReaderFromEndFreshLog verifies that a Follow+FromEnd reader pointed
// at a not-yet-existent path delivers every event written after the file
// appears. Without the fresh-log fix, the reader would seek to EOF after
// the writer creates the file and silently drop any events that were
// already flushed by the time of the seek.
func TestReaderFromEndFreshLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	r, err := NewReader(path, ReaderOption{
		Follow:       true,
		FromEnd:      true,
		WatcherKind:  WatcherKindPoll,
		PollInterval: 20 * time.Millisecond,
	})
	assert.NilError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	// Let the reader observe ENOENT at least once before any writes
	// appear; this is what the fresh-log path keys off.
	time.Sleep(100 * time.Millisecond)

	w, err := NewWriter(path)
	assert.NilError(t, err)

	const n = 10
	for range n {
		assert.NilError(t, w.Append(model.Event{
			Time: time.Now().UTC(),
			Type: model.EventTypeRunning,
			ID:   "x",
		}))
	}

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
			assert.Equal(t, rec.Event.Type, model.EventTypeRunning)
			got++
		case <-ctx.Done():
			t.Fatalf("timed out at %d events", got)
		}
	}
}

// TestReaderNoFollowSkipsWatcher verifies that a Follow=false reader does
// not construct or run a Watcher, so one-shot reads cannot fail due to
// inotify/poll setup errors and exit cleanly when the file is absent.
func TestReaderNoFollowSkipsWatcher(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	r, err := NewReader(path, ReaderOption{Follow: false})
	assert.NilError(t, err)
	assert.Assert(t, r.watcher == nil, "watcher must not be created for no-follow readers")

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	assert.NilError(t, r.Run(ctx))

	rec, ok := <-r.Events()
	assert.Assert(t, !ok, "channel must be closed without delivering events, got %+v", rec)
}

// TestReaderReplaysArchive verifies that a reader started after a rotation
// still delivers the events retained in events.log.1 — i.e. that the
// archive is replayed before the active file is opened.
func TestReaderReplaysArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")
	w, err := NewWriter(path)
	assert.NilError(t, err)
	// Sized so the 5 pre-rotation events (~62 B each) fit, and the next
	// Append triggers exactly one rotation. After this:
	//   - events.log.1: pre00..pre04 + rotation marker
	//   - events.log:   post00, post01, post02
	w.maxSize = 350

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	const preRot, postRot = 5, 3
	for i := range preRot {
		assert.NilError(t, w.Append(model.Event{
			Time: now.Add(time.Duration(i) * time.Second),
			Type: model.EventTypeRunning,
			ID:   fmt.Sprintf("pre%02d", i),
		}))
	}
	for i := range postRot {
		assert.NilError(t, w.Append(model.Event{
			Time: now.Add(time.Duration(preRot+i) * time.Second),
			Type: model.EventTypeRunning,
			ID:   fmt.Sprintf("post%02d", i),
		}))
	}

	_, err = os.Stat(path + ArchiveSuffix)
	assert.NilError(t, err, "archive must exist after rotation")

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := NewReader(path, ReaderOption{Follow: false})
	assert.NilError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	got := map[string]bool{}
	for rec := range r.Events() {
		if rec.Err != nil {
			t.Fatalf("reader error: %v", rec.Err)
		}
		got[rec.Event.ID] = true
	}
	for i := range preRot {
		want := fmt.Sprintf("pre%02d", i)
		if !got[want] {
			t.Errorf(
				"missing pre-rotation event %q (archive not replayed?); got %d events",
				want,
				len(got),
			)
		}
	}
	for i := range postRot {
		want := fmt.Sprintf("post%02d", i)
		if !got[want] {
			t.Errorf("missing post-rotation event %q; got %d events", want, len(got))
		}
	}
}

// TestReaderMarkerlessArchiveReadsActive verifies that a corrupt/truncated
// archive (no trailing rotation marker — e.g. writer crashed mid-rotation)
// does not suppress the active file's records.
func TestReaderMarkerlessArchiveReadsActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	// Hand-craft an archive without a rotation marker.
	archiveLine, err := marshalEvent(model.Event{
		Time: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		Type: model.EventTypeRunning,
		ID:   "archived",
	})
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(path+ArchiveSuffix, archiveLine, 0o644))

	// Active file has one event too.
	activeLine, err := marshalEvent(model.Event{
		Time: time.Date(2026, 5, 21, 12, 1, 0, 0, time.UTC),
		Type: model.EventTypeRunning,
		ID:   "active",
	})
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(path, activeLine, 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := NewReader(path, ReaderOption{Follow: false})
	assert.NilError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	got := map[string]bool{}
	for rec := range r.Events() {
		if rec.Err != nil {
			t.Fatalf("reader error: %v", rec.Err)
		}
		got[rec.Event.ID] = true
	}
	if !got["archived"] {
		t.Errorf("missing archived event; got %v", got)
	}
	if !got["active"] {
		t.Errorf("missing active event (marker-less archive suppressed it?); got %v", got)
	}
}

// TestReaderRecoversIntermediateArchive verifies that when the reader sees
// a rotation marker and the next snapshot reveals an archive whose inode
// differs from the file the reader just finished — i.e. a second rotation
// completed in between — the intermediate file is replayed instead of
// silently dropped.
func TestReaderRecoversIntermediateArchive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	// Hand-craft a "post-second-rotation" snapshot:
	//   - events.log.1 contains the intermediate file (events the reader
	//     has never seen) ending with a rotation marker.
	//   - events.log contains the new active file.
	intermediate := []byte{}
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		b, err := marshalEvent(model.Event{
			Time: now.Add(time.Duration(i) * time.Second),
			Type: model.EventTypeRunning,
			ID:   fmt.Sprintf("intermediate%d", i),
		})
		assert.NilError(t, err)
		intermediate = append(intermediate, b...)
	}
	marker, err := rotationMarker(now.Add(10 * time.Second))
	assert.NilError(t, err)
	intermediate = append(intermediate, marker...)
	assert.NilError(t, os.WriteFile(path+ArchiveSuffix, intermediate, 0o644))

	activeContent := []byte{}
	for i := range 3 {
		b, err := marshalEvent(model.Event{
			Time: now.Add(time.Duration(20+i) * time.Second),
			Type: model.EventTypeRunning,
			ID:   fmt.Sprintf("active%d", i),
		})
		assert.NilError(t, err)
		activeContent = append(activeContent, b...)
	}
	assert.NilError(t, os.WriteFile(path, activeContent, 0o644))

	// Simulate "the reader has already drained some earlier file" by
	// seeding lastIdent with the identity of a throwaway file. fileIdent
	// is platform-specific, so we derive a real-but-unrelated value
	// rather than hand-rolling one.
	sentinelPath := filepath.Join(dir, "sentinel")
	assert.NilError(t, os.WriteFile(sentinelPath, []byte("x"), 0o644))
	sentinelInfo, err := os.Stat(sentinelPath)
	assert.NilError(t, err)
	sentinelIdent := fileIdentOf(sentinelPath, sentinelInfo)

	r, err := NewReader(path, ReaderOption{Follow: false})
	assert.NilError(t, err)
	r.lastIdent = sentinelIdent

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	got := map[string]bool{}
	for rec := range r.Events() {
		if rec.Err != nil {
			t.Fatalf("reader error: %v", rec.Err)
		}
		got[rec.Event.ID] = true
	}
	for i := range 3 {
		want := fmt.Sprintf("intermediate%d", i)
		if !got[want] {
			t.Errorf("missing intermediate event %q (intermediate archive not replayed?); got %v",
				want, got)
		}
	}
	for i := range 3 {
		want := fmt.Sprintf("active%d", i)
		if !got[want] {
			t.Errorf("missing active event %q; got %v", want, got)
		}
	}
}

// TestReaderDoesNotReplayArchiveTwiceWhenActiveMissing verifies that a
// follow reader which starts with only the retained archive present does not
// replay that same archive again after the active file appears.
func TestReaderDoesNotReplayArchiveTwiceWhenActiveMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	archiveLine, err := marshalEvent(model.Event{
		Time: now,
		Type: model.EventTypeRunning,
		ID:   "archived",
	})
	assert.NilError(t, err)
	marker, err := rotationMarker(now.Add(time.Second))
	assert.NilError(t, err)
	assert.NilError(t, os.WriteFile(path+ArchiveSuffix, append(archiveLine, marker...), 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := NewReader(path, ReaderOption{
		Follow:       true,
		WatcherKind:  WatcherKindPoll,
		PollInterval: 20 * time.Millisecond,
	})
	assert.NilError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	got := map[string]int{}
	for got["archived"] == 0 {
		select {
		case rec, ok := <-r.Events():
			if !ok {
				t.Fatalf("reader closed before archive was delivered")
			}
			if rec.Err != nil {
				t.Fatalf("reader error: %v", rec.Err)
			}
			got[rec.Event.ID]++
		case <-ctx.Done():
			t.Fatalf("timed out waiting for archive event; got %v", got)
		}
	}

	w, err := NewWriter(path)
	assert.NilError(t, err)
	assert.NilError(t, w.Append(model.Event{
		Time: now.Add(2 * time.Second),
		Type: model.EventTypeRunning,
		ID:   "active",
	}))

	for got["active"] == 0 {
		select {
		case rec, ok := <-r.Events():
			if !ok {
				t.Fatalf("reader closed before active was delivered; got %v", got)
			}
			if rec.Err != nil {
				t.Fatalf("reader error: %v", rec.Err)
			}
			got[rec.Event.ID]++
		case <-ctx.Done():
			t.Fatalf("timed out waiting for active event; got %v", got)
		}
	}
	assert.Equal(t, got["archived"], 1, "archive replayed more than once; got %v", got)
	assert.Equal(t, got["active"], 1, "active event count mismatch; got %v", got)
}

// TestReaderSkipsBlankLines verifies that stray blank lines in the event
// log do not surface as zero-value Records.
func TestReaderSkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.log")

	good, err := marshalEvent(model.Event{
		Time: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		Type: model.EventTypeRunning,
		ID:   "real",
	})
	assert.NilError(t, err)
	// Sandwich a real record between blank lines (and trailing CRLF noise).
	content := append([]byte("\n\r\n"), good...)
	content = append(content, []byte("\n\n")...)
	assert.NilError(t, os.WriteFile(path, content, 0o644))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, err := NewReader(path, ReaderOption{Follow: false})
	assert.NilError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runDone
	})

	var records []Record
	for rec := range r.Events() {
		records = append(records, rec)
	}
	assert.Equal(t, len(records), 1, "expected exactly one record; got %+v", records)
	assert.NilError(t, records[0].Err)
	assert.Equal(t, records[0].Event.ID, "real")
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
