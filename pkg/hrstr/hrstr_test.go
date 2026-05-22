package hrstr_test

import (
	"testing"
	"time"

	"github.com/ngicks/cmdman/pkg/hrstr"
	"gotest.tools/v3/assert"
)

func TestParseTime(t *testing.T) {
	now := time.Date(2023, 8, 7, 19, 56, 34, 123, time.FixedZone("test", 9*60*60))

	zero, err := hrstr.ParseTime("", func() time.Time { return now })
	assert.NilError(t, err)
	assert.Assert(t, zero.IsZero())

	gotNow, err := hrstr.ParseTime("now", func() time.Time { return now })
	assert.NilError(t, err)
	assert.Equal(t, gotNow, now.UTC())

	gotNano, err := hrstr.ParseTime("2023-08-07T19:56:34.123456789Z", nil)
	assert.NilError(t, err)
	assert.Equal(t, gotNano.Nanosecond(), 123456789)

	got, err := hrstr.ParseTime("2023-08-07T19:56:34Z", nil)
	assert.NilError(t, err)
	assert.Equal(t, got, time.Date(2023, 8, 7, 19, 56, 34, 0, time.UTC))

	_, err = hrstr.ParseTime("not-time", nil)
	assert.ErrorContains(t, err, "parse time")

	_, err = hrstr.ParseTime("now", nil)
	assert.ErrorContains(t, err, "now function is nil")
}
