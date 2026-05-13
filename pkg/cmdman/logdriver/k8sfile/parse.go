package k8sfile

import (
	"bytes"
	"fmt"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// parseEntry splits a single k8s-file entry of the form
//
//	<RFC3339Nano> <stream> <F|P> <content>\n
//
// and returns the unframed log line. The trailing '\n' that the writer
// appends to partial (P) entries is stripped because it is framing rather
// than original output.
func parseEntry(entry []byte) (logdriver.LogLine, bool, error) {
	sp1 := bytes.IndexByte(entry, ' ')
	if sp1 < 0 {
		return logdriver.LogLine{}, false, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp2 := bytes.IndexByte(entry[sp1+1:], ' ')
	if sp2 < 0 {
		return logdriver.LogLine{}, false, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp2 += sp1 + 1
	sp3 := bytes.IndexByte(entry[sp2+1:], ' ')
	if sp3 < 0 {
		return logdriver.LogLine{}, false, fmt.Errorf("logdriver: malformed log entry: %q", entry)
	}
	sp3 += sp2 + 1
	ts, err := time.Parse(K8sLogTimeFormat, string(entry[:sp1]))
	if err != nil {
		return logdriver.LogLine{}, false, fmt.Errorf(
			"logdriver: malformed log entry timestamp: %w",
			err,
		)
	}
	stream := logdriver.Stream(entry[sp1+1 : sp2])
	tag := entry[sp2+1 : sp3]
	content := entry[sp3+1:]
	partial := bytes.Equal(tag, []byte(tagPartial))
	switch {
	case partial:
		content = bytes.TrimRight(content, "\n")
	case bytes.Equal(tag, []byte(tagFull)):
	case bytes.Equal(tag, []byte(tagRotation)):
		return logdriver.LogLine{}, true, nil
	default:
		return logdriver.LogLine{}, false, fmt.Errorf("logdriver: malformed log entry tag %q", tag)
	}
	return logdriver.LogLine{
		Time:    ts,
		Stream:  stream,
		Partial: partial,
		Line:    content,
	}, false, nil
}
