package k8sfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"time"
)

type fileSpan struct {
	Path     string
	File     *os.File
	HeadTime time.Time
	TailTime time.Time
	Empty    bool
}

// discoverFiles opens the active file and retained archives and returns them
// ordered oldest-to-newest.
func discoverFiles(path string, maxFile int) ([]fileSpan, error) {
	if path == "" {
		return nil, fmt.Errorf("logdriver: log file path is empty")
	}
	var spans []fileSpan
	active, err := openSpan(path)
	if err != nil {
		return nil, err
	}
	spans = append(spans, active)
	if maxFile > 1 {
		for i := 1; i < maxFile; i++ {
			archivePath := fmt.Sprintf("%s.%d", path, i)
			span, err := openSpan(archivePath)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					break
				}
				closeSpans(spans)
				return nil, err
			}
			spans = append(spans, span)
		}
	}
	for i := range spans {
		if err := fillSpanTimes(&spans[i]); err != nil {
			closeSpans(spans)
			return nil, err
		}
	}
	slices.Reverse(spans)
	return spans, nil
}

func closeSpans(spans []fileSpan) {
	for _, span := range spans {
		if span.File != nil {
			_ = span.File.Close()
		}
	}
}

func openSpan(path string) (fileSpan, error) {
	f, err := os.Open(path)
	if err != nil {
		return fileSpan{}, fmt.Errorf("logdriver: open log file %s: %w", path, err)
	}
	return fileSpan{Path: path, File: f}, nil
}

func fillSpanTimes(span *fileSpan) error {
	stat, err := span.File.Stat()
	if err != nil {
		return fmt.Errorf("logdriver: stat log file %s: %w", span.Path, err)
	}
	if stat.Size() == 0 {
		span.Empty = true
		return nil
	}
	head, ok, err := readFirstUserLine(span.File)
	if err != nil {
		return fmt.Errorf("logdriver: read log head %s: %w", span.Path, err)
	}
	if !ok {
		span.Empty = true
		return nil
	}
	tail, ok, err := readLastUserLine(span.File, stat.Size())
	if err != nil {
		return fmt.Errorf("logdriver: read log tail %s: %w", span.Path, err)
	}
	if !ok {
		span.Empty = true
		return nil
	}
	span.HeadTime = head.Time
	span.TailTime = tail.Time
	return nil
}

func readFirstUserLine(f *os.File) (logLineForSpan, bool, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return logLineForSpan{}, false, err
	}
	br := bufio.NewReaderSize(f, 32*1024)
	for {
		entry, err := br.ReadBytes('\n')
		if len(entry) > 0 {
			line, isRotation, parseErr := parseEntry(entry)
			if parseErr != nil {
				return logLineForSpan{}, false, parseErr
			}
			if isRotation {
				continue
			}
			return logLineForSpan{Time: line.Time}, true, nil
		}
		if err == io.EOF {
			return logLineForSpan{}, false, nil
		}
		if err != nil {
			return logLineForSpan{}, false, err
		}
	}
}

func readLastUserLine(f *os.File, size int64) (logLineForSpan, bool, error) {
	offset, err := FindLastLine(f, size)
	if err != nil {
		return logLineForSpan{}, false, err
	}
	for {
		entry, err := readEntryAt(f, offset)
		if err != nil {
			return logLineForSpan{}, false, err
		}
		if len(entry) == 0 {
			return logLineForSpan{}, false, nil
		}
		line, isRotation, err := parseEntry(entry)
		if err != nil {
			return logLineForSpan{}, false, err
		}
		if !isRotation {
			return logLineForSpan{Time: line.Time}, true, nil
		}
		if offset == 0 {
			return logLineForSpan{}, false, nil
		}
		next, err := SkipLines(f, size, offset, -1)
		if err != nil {
			return logLineForSpan{}, false, err
		}
		if next == offset {
			return logLineForSpan{}, false, nil
		}
		offset = next
	}
}

type logLineForSpan struct {
	Time time.Time
}

func readEntryAt(f *os.File, offset int64) ([]byte, error) {
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(f, 32*1024)
	entry, err := br.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return nil, err
	}
	if err == io.EOF && len(entry) > 0 {
		return nil, nil
	}
	return entry, nil
}
