package hrstr_test

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/pkg/hrstr"
)

func TestParseTimeout(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    time.Duration
		wantErr string
	}{
		{in: "30", want: 30 * time.Second},
		{in: " 30 ", want: 30 * time.Second},
		{in: "30s", want: 30 * time.Second},
		{in: "1m30s", want: 90 * time.Second},
		{in: "250ms", want: 250 * time.Millisecond},
		{in: "0", wantErr: "timeout must be positive"},
		{in: "0s", wantErr: "timeout must be positive"},
		{in: "-5", wantErr: "timeout must be positive"},
		{in: "-5s", wantErr: "timeout must be positive"},
		{in: "", wantErr: "parse timeout"},
		{in: "1.5", wantErr: "parse timeout"},
		{in: "garbage", wantErr: "parse timeout"},
		{in: "9223372036854775807", wantErr: "too large"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := hrstr.ParseTimeout(tc.in)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}
