package k8sfile

import (
	"fmt"
	"math"
	"strconv"

	"github.com/dustin/go-humanize"
)

const (
	// DefaultLogMaxSize is the default active log size limit for k8s-file.
	DefaultLogMaxSize = 5 * 1024 * 1024
	// DefaultLogMaxFile is the default number of active plus archived log files.
	DefaultLogMaxFile = 3
)

func parseLogMaxSizeOption(opts map[string]string) (int64, error) {
	value, ok := opts[logOptMaxSize]
	if !ok {
		return DefaultLogMaxSize, nil
	}
	return parseLogMaxSize(value)
}

func parseLogMaxSize(value string) (int64, error) {
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

func parseLogMaxFileOption(opts map[string]string) (int, error) {
	value, ok := opts[logOptMaxFile]
	if !ok {
		return DefaultLogMaxFile, nil
	}
	return parseLogMaxFile(value)
}

func parseLogMaxFile(value string) (int, error) {
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
