package k8sfile

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// FollowPollInterval is how often we re-read the file in --follow mode.
const FollowPollInterval = 100 * time.Millisecond

// NewReader opens retained k8s-file logs.
func (Driver) NewReader(
	ctx context.Context,
	dir string,
	opts map[string]string,
	ro logdriver.ReaderOption,
) (logdriver.Reader, error) {
	path, err := resolvePath(dir, opts)
	if err != nil {
		return nil, err
	}
	maxFile, err := parseLogMaxFile(opts[logOptMaxFile])
	if err != nil {
		return nil, fmt.Errorf("logdriver: k8s-file: %s: %w", logOptMaxFile, err)
	}
	return newReader(ctx, path, maxFile, ro)
}

type readSpan struct {
	fileSpan
	Start int64
	End   int64
}

type Reader struct {
	ctx        context.Context
	cancel     context.CancelFunc
	ro         logdriver.ReaderOption
	activePath string
	spans      []readSpan
	rec        chan logdriver.Record
	wg         sync.WaitGroup
	mu         sync.Mutex
}

func newReader(
	ctx context.Context,
	path string,
	maxFile int,
	ro logdriver.ReaderOption,
) (*Reader, error) {
	spans, err := discoverFiles(path, maxFile)
	if err != nil {
		return nil, err
	}
	readSpans, err := selectReadSpans(spans, ro)
	if err != nil {
		closeSpans(spans)
		return nil, err
	}
	closeUnselectedSpans(spans, readSpans)
	readerCtx, cancel := context.WithCancel(ctx)
	r := &Reader{
		ctx:        readerCtx,
		cancel:     cancel,
		ro:         ro,
		activePath: path,
		spans:      readSpans,
		rec:        make(chan logdriver.Record),
	}
	r.wg.Go(r.run)
	return r, nil
}

// NewRangeReader reads records between two driver offsets. It is used by the
// service to bridge the race between storage replay and monitor subscription.
func NewRangeReader(
	ctx context.Context,
	dir string,
	opts map[string]string,
	from Offset,
	to Offset,
) (logdriver.Reader, error) {
	path, err := resolvePath(dir, opts)
	if err != nil {
		return nil, err
	}
	if to.Path == "" || to.Bytes <= 0 {
		return closedReader(), nil
	}
	if from.Path != "" && from.Path != to.Path {
		from = Offset{}
	}
	if to.Path != path {
		return closedReader(), nil
	}
	span, err := openSpan(path)
	if err != nil {
		return nil, err
	}
	if from.Bytes >= to.Bytes {
		_ = span.File.Close()
		return closedReader(), nil
	}
	readerCtx, cancel := context.WithCancel(ctx)
	r := &Reader{
		ctx:        readerCtx,
		cancel:     cancel,
		activePath: path,
		spans: []readSpan{{
			fileSpan: span,
			Start:    from.Bytes,
			End:      to.Bytes,
		}},
		rec: make(chan logdriver.Record),
	}
	r.wg.Go(r.run)
	return r, nil
}

func closedReader() logdriver.Reader {
	ch := make(chan logdriver.Record)
	close(ch)
	return staticReader{rec: ch}
}

type staticReader struct {
	rec <-chan logdriver.Record
}

func (r staticReader) Records() <-chan logdriver.Record { return r.rec }
func (r staticReader) Close() error                     { return nil }

func closeUnselectedSpans(spans []fileSpan, selected []readSpan) {
	keep := make(map[*os.File]struct{}, len(selected))
	for _, span := range selected {
		keep[span.File] = struct{}{}
	}
	for _, span := range spans {
		if _, ok := keep[span.File]; ok {
			continue
		}
		if span.File != nil {
			_ = span.File.Close()
		}
	}
}

func selectReadSpans(spans []fileSpan, ro logdriver.ReaderOption) ([]readSpan, error) {
	if ro.Tail > 0 {
		return selectTailSpans(spans, ro.Tail)
	}
	start := 0
	if !ro.Since.IsZero() {
		start = len(spans)
		for i, span := range spans {
			if span.Empty {
				continue
			}
			if span.TailTime.Equal(ro.Since) || span.TailTime.After(ro.Since) {
				start = i
				break
			}
		}
	}
	if start == len(spans) {
		if ro.Follow && len(spans) > 0 {
			latest := spans[len(spans)-1]
			stat, err := latest.File.Stat()
			if err != nil {
				return nil, fmt.Errorf("logdriver: stat log file %s: %w", latest.Path, err)
			}
			return []readSpan{{fileSpan: latest, Start: stat.Size()}}, nil
		}
		return nil, nil
	}
	readSpans := make([]readSpan, 0, len(spans)-start)
	for _, span := range spans[start:] {
		readSpans = append(readSpans, readSpan{fileSpan: span})
	}
	return readSpans, nil
}

func selectTailSpans(spans []fileSpan, tail int) ([]readSpan, error) {
	if tail <= 0 {
		return nil, nil
	}
	var selected []readSpan
	remaining := tail
	for i := len(spans) - 1; i >= 0 && remaining > 0; i-- {
		span := spans[i]
		if span.Empty {
			continue
		}
		stat, err := span.File.Stat()
		if err != nil {
			return nil, fmt.Errorf("logdriver: stat log file %s: %w", span.Path, err)
		}
		lastOffset, ok, err := lastUserLineOffset(span.File, stat.Size())
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		start := lastOffset
		if remaining > 1 {
			start, err = SkipLines(span.File, stat.Size(), lastOffset, -(remaining - 1))
			if err != nil {
				return nil, err
			}
		}
		count, err := countUserLines(span.File, start)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			continue
		}
		if count > remaining {
			count = remaining
		}
		selected = append(selected, readSpan{fileSpan: span, Start: start})
		remaining -= count
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected, nil
}

func lastUserLineOffset(f *os.File, size int64) (int64, bool, error) {
	offset, err := FindLastLine(f, size)
	if err != nil {
		return 0, false, err
	}
	for {
		entry, err := readEntryAt(f, offset)
		if err != nil {
			return 0, false, err
		}
		if len(entry) == 0 {
			return 0, false, nil
		}
		_, isRotation, err := parseEntry(entry)
		if err != nil {
			return 0, false, err
		}
		if !isRotation {
			return offset, true, nil
		}
		if offset == 0 {
			return 0, false, nil
		}
		next, err := SkipLines(f, size, offset, -1)
		if err != nil {
			return 0, false, err
		}
		if next == offset {
			return 0, false, nil
		}
		offset = next
	}
}

func countUserLines(f *os.File, offset int64) (int, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	br := bufio.NewReaderSize(f, 32*1024)
	var count int
	for {
		entry, err := br.ReadBytes('\n')
		if len(entry) > 0 {
			_, isRotation, parseErr := parseEntry(entry)
			if parseErr != nil {
				return 0, parseErr
			}
			if isRotation {
				return count, nil
			}
			count++
		}
		if err == io.EOF {
			return count, nil
		}
		if err != nil {
			return 0, err
		}
	}
}

func (r *Reader) Records() <-chan logdriver.Record {
	return r.rec
}

func (r *Reader) run() {
	defer close(r.rec)
	sent := 0
	for i := 0; i < len(r.spans); i++ {
		done, ok := r.scanSpan(&r.spans[i], i == len(r.spans)-1, &sent)
		if !ok || done {
			return
		}
	}
	if r.ro.Follow {
		r.followLatest(sent)
	}
}

func (r *Reader) scanSpan(span *readSpan, latest bool, sent *int) (bool, bool) {
	if _, err := span.File.Seek(span.Start, io.SeekStart); err != nil {
		r.sendErr(fmt.Errorf("logdriver: seek log file: %w", err))
		return false, false
	}
	br := bufio.NewReaderSize(span.File, 32*1024)
	offset := span.Start
	for {
		if r.ctx.Err() != nil {
			return false, false
		}
		entry, err := br.ReadBytes('\n')
		if len(entry) > 0 {
			offset += int64(len(entry))
			if span.End > 0 && offset > span.End {
				return true, true
			}
			line, isRotation, parseErr := parseEntry(entry)
			if parseErr != nil {
				r.sendErr(parseErr)
				return false, false
			}
			if isRotation {
				return false, true
			}
			if r.skipLine(line) {
				continue
			}
			if r.afterUntil(line) {
				return true, true
			}
			if !r.sendLine(line, Offset{Path: span.Path, Bytes: offset}) {
				return false, false
			}
			(*sent)++
			if r.ro.Head > 0 && *sent >= r.ro.Head {
				return true, true
			}
			continue
		}
		if err != nil && err != io.EOF {
			r.sendErr(fmt.Errorf("logdriver: read log file: %w", err))
			return false, false
		}
		if !r.ro.Follow || !latest {
			return false, true
		}
		if !r.waitFollow() {
			return false, false
		}
	}
}

func (r *Reader) followLatest(sent int) {
	// TODO Stage 4: follow across rotation with monitor bridge reread.
	if len(r.spans) == 0 {
		span, err := openSpan(r.activePath)
		if err != nil {
			r.sendErr(err)
			return
		}
		stat, err := span.File.Stat()
		if err != nil {
			_ = span.File.Close()
			r.sendErr(fmt.Errorf("logdriver: stat log file %s: %w", span.Path, err))
			return
		}
		r.mu.Lock()
		r.spans = append(r.spans, readSpan{fileSpan: span, Start: stat.Size()})
		r.mu.Unlock()
	}
	latest := &r.spans[len(r.spans)-1]
	r.scanSpan(latest, true, &sent)
}

func (r *Reader) skipLine(line logdriver.LogLine) bool {
	return !r.ro.Since.IsZero() && line.Time.Before(r.ro.Since)
}

func (r *Reader) afterUntil(line logdriver.LogLine) bool {
	return !r.ro.Until.IsZero() && line.Time.After(r.ro.Until)
}

func (r *Reader) sendLine(line logdriver.LogLine, offset Offset) bool {
	select {
	case <-r.ctx.Done():
		return false
	case r.rec <- logdriver.Record{Line: line, Offset: offset}:
		return true
	}
}

func (r *Reader) sendErr(err error) bool {
	select {
	case <-r.ctx.Done():
		return false
	case r.rec <- logdriver.Record{Err: err}:
		return true
	}
}

func (r *Reader) waitFollow() bool {
	timer := time.NewTimer(FollowPollInterval)
	defer timer.Stop()
	select {
	case <-r.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (r *Reader) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	var closeErr error
	for i := range r.spans {
		if r.spans[i].File == nil {
			continue
		}
		if err := r.spans[i].File.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
		r.spans[i].File = nil
	}
	return closeErr
}
