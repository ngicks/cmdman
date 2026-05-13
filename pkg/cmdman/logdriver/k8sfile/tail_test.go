package k8sfile

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestSkipLinesForward(t *testing.T) {
	fixture := "one\ntwo\nthree\nfour\n"
	offset, err := SkipLines(strings.NewReader(fixture), int64(len(fixture)), 0, 2)
	assert.NilError(t, err)
	assert.Equal(t, offset, int64(len("one\ntwo\n")))
}

func TestSkipLinesBackward(t *testing.T) {
	fixture := "one\ntwo\nthree\nfour\n"
	start := int64(len("one\ntwo\nthree\n"))
	offset, err := SkipLines(strings.NewReader(fixture), int64(len(fixture)), start, -2)
	assert.NilError(t, err)
	assert.Equal(t, offset, int64(len("one\n")))
}

func TestSkipLinesZeroSnapsToCurrentLineStart(t *testing.T) {
	fixture := "one\ntwo\nthree\n"
	offset, err := SkipLines(
		strings.NewReader(fixture),
		int64(len(fixture)),
		int64(len("one\ntw")),
		0,
	)
	assert.NilError(t, err)
	assert.Equal(t, offset, int64(len("one\n")))
}

func TestFindLastLine(t *testing.T) {
	fixture := "one\ntwo\nthree\n"
	offset, err := FindLastLine(strings.NewReader(fixture), int64(len(fixture)))
	assert.NilError(t, err)
	assert.Equal(t, offset, int64(len("one\ntwo\n")))
}

func TestFindLastLineIgnoresTrailingPartial(t *testing.T) {
	fixture := "one\ntwo\npartial"
	offset, err := FindLastLine(strings.NewReader(fixture), int64(len(fixture)))
	assert.NilError(t, err)
	assert.Equal(t, offset, int64(len("one\n")))
}

func TestFindLastLineEmptyFile(t *testing.T) {
	offset, err := FindLastLine(strings.NewReader(""), 0)
	assert.NilError(t, err)
	assert.Equal(t, offset, int64(0))
}
