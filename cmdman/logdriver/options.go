package logdriver

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dustin/go-humanize"
)

type LogDriver string

const (
	DriverK8sFile LogDriver = "k8s-file"
	DriverNone    LogDriver = "none"
)

// ReaderOption configures log reads.
type ReaderOption struct {
	Follow bool
	Since  time.Time
	Until  time.Time
	Head   int
	Tail   int
	// StartOffset bounds the read to what was stored from one driver-specific
	// position onward — the value a [Record.Offset] or an [OffsetWriter]
	// carries, so it is opaque here and only the driver that minted it can read
	// it. Nil, or a driver's zero value, reads everything retained.
	//
	// It picks where the read starts, so it stands in place of Since and Tail
	// rather than beside them; Since, Until and Head still apply to the records
	// it does reach. A position the driver can no longer honour — its file
	// rotated away or truncated under it — reads everything retained, because
	// what was cut is gone either way and the alternative is reading nothing.
	StartOffset any
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

const (
	LogOptPath    = "path"
	LogOptMaxSize = "max-size"
	LogOptMaxFile = "max-file"
)

func IsDriver(s string) bool {
	switch LogDriver(s) {
	case DriverK8sFile, DriverNone:
		return true
	}
	return false
}

func IsValidOpt(driver, key string) bool {
	switch LogDriver(driver) {
	case DriverK8sFile:
		switch key {
		case LogOptPath, LogOptMaxSize, LogOptMaxFile:
			return true
		}
	}
	return false
}

func ValidateOpt(driver, key, value string) error {
	if !IsValidOpt(driver, key) {
		return fmt.Errorf("log_opt %q not valid for driver %q", key, driver)
	}
	switch key {
	case LogOptPath:
		if !filepath.IsAbs(value) {
			return fmt.Errorf("log_opt %q must be an absolute path: %q", key, value)
		}
	case LogOptMaxSize:
		if _, err := ParseMaxSize(value); err != nil {
			return fmt.Errorf("log_opt %q: %w", key, err)
		}
	case LogOptMaxFile:
		if _, err := ParseMaxFile(value); err != nil {
			return fmt.Errorf("log_opt %q: %w", key, err)
		}
	}
	return nil
}

func ParseMaxSize(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	n, err := humanize.ParseBytes(value)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", value, err)
	}
	if n > math.MaxInt64 {
		return 0, fmt.Errorf("size %q overflows int64", value)
	}
	return int64(n), nil
}

func ParseMaxFile(value string) (int, error) {
	if value == "" || value == "0" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid count %q: %w", value, err)
	}
	if n < 1 {
		return 0, fmt.Errorf("count %q must be >= 1", value)
	}
	return n, nil
}
