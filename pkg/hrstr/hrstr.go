// Package hrstr parses human-readable strings (e.g. CLI flag values) into
// typed values.
package hrstr

import (
	"fmt"
	"time"
)

// ParseTime parses a human-readable timestamp such as an empty string,
// "now", or an RFC3339 / RFC3339Nano value. An empty input returns the
// zero time. "now" returns now().UTC().
func ParseTime(value string, now func() time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if value == "now" {
		if now == nil {
			return time.Time{}, fmt.Errorf("hrstr: now function is nil")
		}
		return now().UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}
	return time.Time{}, fmt.Errorf(
		"hrstr: parse time %q: expected %q, %q, or %q",
		value,
		"now",
		time.RFC3339Nano,
		time.RFC3339,
	)
}
