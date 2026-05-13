package logdriver_test

import (
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

func TestParseLogTime(t *testing.T) {
	now := time.Date(2023, 8, 7, 19, 56, 34, 123, time.FixedZone("test", 9*60*60))

	zero, err := logdriver.ParseLogTime("", func() time.Time { return now })
	assert.NilError(t, err)
	assert.Assert(t, zero.IsZero())

	gotNow, err := logdriver.ParseLogTime("now", func() time.Time { return now })
	assert.NilError(t, err)
	assert.Equal(t, gotNow, now.UTC())

	gotNano, err := logdriver.ParseLogTime("2023-08-07T19:56:34.123456789Z", nil)
	assert.NilError(t, err)
	assert.Equal(t, gotNano.Nanosecond(), 123456789)

	got, err := logdriver.ParseLogTime("2023-08-07T19:56:34Z", nil)
	assert.NilError(t, err)
	assert.Equal(t, got, time.Date(2023, 8, 7, 19, 56, 34, 0, time.UTC))

	_, err = logdriver.ParseLogTime("not-time", nil)
	assert.ErrorContains(t, err, "parse log time")
}
