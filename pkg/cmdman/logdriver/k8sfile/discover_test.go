package k8sfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func writeK8sFixture(t *testing.T, path, body string) {
	t.Helper()
	assert.NilError(t, os.WriteFile(path, []byte(body), 0o640))
}

func mustK8sTime(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(K8sLogTimeFormat, value)
	assert.NilError(t, err)
	return ts
}

func TestDiscoverFilesOrdersOldestToNewestAndReadsTimes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	writeK8sFixture(t, path,
		"2023-08-07T19:56:38.000000000Z stdout F active-head\n"+
			"2023-08-07T19:56:39.000000000Z stdout F active-tail\n",
	)
	writeK8sFixture(t, path+".1",
		"2023-08-07T19:56:36.000000000Z stdout F one-head\n"+
			"2023-08-07T19:56:37.000000000Z stdout F one-tail\n"+
			"2023-08-07T19:56:37.500000000Z stdout R -\n",
	)
	writeK8sFixture(t, path+".2",
		"2023-08-07T19:56:34.000000000Z stdout F two-head\n"+
			"2023-08-07T19:56:35.000000000Z stdout F two-tail\n"+
			"2023-08-07T19:56:35.500000000Z stdout R -\n",
	)

	spans, err := discoverFiles(path, 3)
	assert.NilError(t, err)
	defer closeSpans(spans)

	assert.Equal(t, len(spans), 3)
	assert.Equal(t, spans[0].Path, path+".2")
	assert.Equal(t, spans[1].Path, path+".1")
	assert.Equal(t, spans[2].Path, path)
	assert.Assert(t, spans[0].HeadTime.Equal(mustK8sTime(t, "2023-08-07T19:56:34.000000000Z")))
	assert.Assert(t, spans[0].TailTime.Equal(mustK8sTime(t, "2023-08-07T19:56:35.000000000Z")))
	assert.Assert(t, spans[2].HeadTime.Equal(mustK8sTime(t, "2023-08-07T19:56:38.000000000Z")))
	assert.Assert(t, spans[2].TailTime.Equal(mustK8sTime(t, "2023-08-07T19:56:39.000000000Z")))
}
