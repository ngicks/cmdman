// Package hrstr parses human-readable strings (e.g. CLI flag values) into
// typed values.
package hrstr

import (
	"fmt"
	"time"
)

// ParseTime parses a human-readable timestamp such as an empty string,
// "now", an RFC3339 / RFC3339Nano value, or a Go [time.ParseDuration]
// string interpreted as a signed offset from now (e.g. "5m" means five
// minutes from now, "-5m" means five minutes ago).
//
// An empty input returns the zero time. "now" and any duration form
// require now to be non-nil and resolve relative to now().UTC().
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
	if d, err := time.ParseDuration(value); err == nil {
		if now == nil {
			return time.Time{}, fmt.Errorf("hrstr: now function is nil")
		}
		return now().UTC().Add(d), nil
	}
	return time.Time{}, fmt.Errorf(
		"hrstr: parse time %q: expected %q, %q, %q, or a Go duration like %q",
		value,
		"now",
		time.RFC3339Nano,
		time.RFC3339,
		"5m",
	)
}
