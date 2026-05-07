package store

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestParseLogMaxSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"0", 0},
		{"1024", 1024},
		{"10mb", 10 * 1000 * 1000},
		{"10MB", 10 * 1000 * 1000},
		{"10 MB", 10 * 1000 * 1000},
		{"1kb", 1000},
		{"1mib", 1024 * 1024},
		{"1gib", 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseLogMaxSize(tc.in)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestParseLogMaxSize_Errors(t *testing.T) {
	cases := []string{
		"abc",
		"10xb",
		"-1",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := ParseLogMaxSize(in)
			assert.Assert(t, err != nil, "expected error for %q", in)
		})
	}
}

func TestValidateLogOpt_MaxSize(t *testing.T) {
	assert.NilError(t, ValidateLogOpt(LogDriverK8sFile, LogOptMaxSize, "10mb"))
	assert.NilError(t, ValidateLogOpt(LogDriverK8sFile, LogOptMaxSize, "0"))

	err := ValidateLogOpt(LogDriverK8sFile, LogOptMaxSize, "not-a-size")
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "max-size"))

	// max-size is not valid for the none driver.
	err = ValidateLogOpt(LogDriverNone, LogOptMaxSize, "10mb")
	assert.Assert(t, err != nil)
}

func TestParseLogMaxFile(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"1", 1},
		{"3", 3},
		{"10", 10},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseLogMaxFile(tc.in)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestParseLogMaxFile_Errors(t *testing.T) {
	cases := []string{
		"abc",
		"-1",
		"1.5",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			_, err := ParseLogMaxFile(in)
			assert.Assert(t, err != nil, "expected error for %q", in)
		})
	}
}

func TestValidateLogOpt_MaxFile(t *testing.T) {
	assert.NilError(t, ValidateLogOpt(LogDriverK8sFile, LogOptMaxFile, "3"))
	assert.NilError(t, ValidateLogOpt(LogDriverK8sFile, LogOptMaxFile, "0"))

	err := ValidateLogOpt(LogDriverK8sFile, LogOptMaxFile, "not-a-number")
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "max-file"))

	err = ValidateLogOpt(LogDriverNone, LogOptMaxFile, "3")
	assert.Assert(t, err != nil)
}
