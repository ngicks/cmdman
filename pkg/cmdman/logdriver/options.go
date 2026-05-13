package logdriver

import (
	"fmt"
	"time"
)

// ReaderOption configures log reads.
type ReaderOption struct {
	Follow bool
	Since  time.Time
	Until  time.Time
	Head   int
	Tail   int
}

// Validate rejects incompatible log reader options.
func (ro ReaderOption) Validate() error {
	if ro.Head > 0 && ro.Tail > 0 {
		return fmt.Errorf("logdriver: head and tail cannot both be set")
	}
	if ro.Follow && !ro.Until.IsZero() {
		return fmt.Errorf("logdriver: follow and until cannot both be set")
	}
	if !ro.Since.IsZero() && !ro.Until.IsZero() && ro.Since.After(ro.Until) {
		return fmt.Errorf("logdriver: since must not be after until")
	}
	return nil
}

// ParseLogTime parses a CLI-facing log timestamp.
func ParseLogTime(value string, now func() time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if value == "now" {
		if now == nil {
			return time.Time{}, fmt.Errorf("logdriver: now function is nil")
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
		"logdriver: parse log time %q: expected %q, %q, or %q",
		value,
		"now",
		time.RFC3339Nano,
		time.RFC3339,
	)
}
