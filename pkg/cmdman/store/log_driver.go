package store

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"

	"github.com/dustin/go-humanize"
)

// LogDriver determines how the monitor persists command output.
type LogDriver string

const (
	// LogDriverK8sFile writes a per-command log file in the same format
	// podman uses for its k8s-file driver (a.k.a. json-file). Each entry
	// is `<RFC3339Nano> <stream> <F|P> <content>\n`.
	LogDriverK8sFile LogDriver = "k8s-file"
	// LogDriverNone disables on-disk log capture.
	LogDriverNone LogDriver = "none"
)

// DefaultLogDriver is the log driver used when no explicit value is supplied.
const DefaultLogDriver = LogDriverK8sFile

// IsLogDriver reports whether s is a valid LogDriver value.
func IsLogDriver(s string) bool {
	switch LogDriver(s) {
	case LogDriverK8sFile, LogDriverNone:
		return true
	}
	return false
}

// LogOpt key constants. These mirror keys in podman's `--log-opt`
// vocabulary; only a subset is currently implemented.
const (
	// LogOptPath overrides the on-disk log file path for file-based
	// drivers. The value must be an absolute path.
	LogOptPath = "path"
	// LogOptMaxSize caps the size of the active log file. When a write
	// would push the file to or past this byte count, the k8s-file driver
	// rotates: the active file becomes <path>.1, the previous .1 becomes
	// .2, and so on. Accepted as a human-readable string parsed by
	// dustin/go-humanize.ParseBytes (e.g. "10mb", "1KiB", "1024"). The
	// default is 5MiB when omitted. The empty string and "0" both
	// disable the cap when explicitly supplied.
	LogOptMaxSize = "max-size"
	// LogOptMaxFile caps the total number of log files kept (active +
	// archives). When a rotation would push the count past this value,
	// the oldest archive is dropped. Only meaningful when LogOptMaxSize
	// is set; with LogOptMaxFile <= 1 the active file is truncated in
	// place instead of being rotated. Default is 3 when omitted. Empty /
	// "0" means "unset" when explicitly supplied.
	LogOptMaxFile = "max-file"
)

// IsValidLogOpt reports whether key is meaningful for the given driver.
func IsValidLogOpt(driver LogDriver, key string) bool {
	switch driver {
	case LogDriverK8sFile:
		switch key {
		case LogOptPath, LogOptMaxSize, LogOptMaxFile:
			return true
		}
	}
	return false
}

// ValidateLogOpt checks that key is meaningful for the driver and that
// value satisfies any per-key constraints.
func ValidateLogOpt(driver LogDriver, key, value string) error {
	if !IsValidLogOpt(driver, key) {
		return fmt.Errorf("log_opt %q not valid for driver %q", key, driver)
	}
	switch key {
	case LogOptPath:
		if !filepath.IsAbs(value) {
			return fmt.Errorf("log_opt %q must be an absolute path: %q", key, value)
		}
	case LogOptMaxSize:
		if _, err := ParseLogMaxSize(value); err != nil {
			return fmt.Errorf("log_opt %q: %w", key, err)
		}
	case LogOptMaxFile:
		if _, err := ParseLogMaxFile(value); err != nil {
			return fmt.Errorf("log_opt %q: %w", key, err)
		}
	}
	return nil
}

// ParseLogMaxSize parses a max-size log-opt value into a non-negative
// byte count. It accepts the inputs supported by
// dustin/go-humanize.ParseBytes: bare integers ("1024"), SI suffixes
// ("10mb", "10 MB"), and IEC suffixes ("10mib"). The empty string and
// "0" both mean "no limit" and return 0 with no error.
func ParseLogMaxSize(value string) (int64, error) {
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

// ParseLogMaxFile parses a max-file log-opt value into a positive file
// count. The empty string and "0" both mean "unset" and return 0;
// non-empty values must be a positive integer.
func ParseLogMaxFile(value string) (int, error) {
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
