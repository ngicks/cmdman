package logdriver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"gotest.tools/v3/assert"
)

func TestReaderOptionValidate(t *testing.T) {
	now := time.Date(2023, 8, 7, 19, 56, 34, 0, time.UTC)

	tests := []struct {
		name string
		ro   logdriver.ReaderOption
		want string
	}{
		{
			name: "head tail conflict",
			ro:   logdriver.ReaderOption{Head: 1, Tail: 1},
			want: "head and tail",
		},
		{
			name: "follow until conflict",
			ro:   logdriver.ReaderOption{Follow: true, Until: now},
			want: "follow and until",
		},
		{
			name: "since after until",
			ro:   logdriver.ReaderOption{Since: now.Add(time.Second), Until: now},
			want: "since",
		},
		{
			name: "valid",
			ro:   logdriver.ReaderOption{Since: now, Tail: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ro.Validate()
			if tt.want == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

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
			got, err := logdriver.ParseMaxSize(tc.in)
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
			_, err := logdriver.ParseMaxSize(in)
			assert.Assert(t, err != nil, "expected error for %q", in)
		})
	}
}

func TestValidateLogOpt_MaxSize(t *testing.T) {
	assert.NilError(
		t,
		logdriver.ValidateOpt(string(logdriver.DriverK8sFile), logdriver.LogOptMaxSize, "10mb"),
	)
	assert.NilError(
		t,
		logdriver.ValidateOpt(string(logdriver.DriverK8sFile), logdriver.LogOptMaxSize, "0"),
	)

	err := logdriver.ValidateOpt(
		string(logdriver.DriverK8sFile),
		logdriver.LogOptMaxSize,
		"not-a-size",
	)
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "max-size"))

	// max-size is not valid for the none driver.
	err = logdriver.ValidateOpt(string(logdriver.DriverNone), logdriver.LogOptMaxSize, "10mb")
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
			got, err := logdriver.ParseMaxFile(tc.in)
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
			_, err := logdriver.ParseMaxFile(in)
			assert.Assert(t, err != nil, "expected error for %q", in)
		})
	}
}

func TestValidateLogOpt_MaxFile(t *testing.T) {
	assert.NilError(
		t,
		logdriver.ValidateOpt(string(logdriver.DriverK8sFile), logdriver.LogOptMaxFile, "3"),
	)
	assert.NilError(
		t,
		logdriver.ValidateOpt(string(logdriver.DriverK8sFile), logdriver.LogOptMaxFile, "0"),
	)

	err := logdriver.ValidateOpt(
		string(logdriver.DriverK8sFile),
		logdriver.LogOptMaxFile,
		"not-a-number",
	)
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "max-file"))

	err = logdriver.ValidateOpt(string(logdriver.DriverNone), logdriver.LogOptMaxFile, "3")
	assert.Assert(t, err != nil)
}
