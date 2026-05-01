package store

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const DefaultStopSignal = "SIGTERM"

// ParseSignal resolves a POSIX signal name or number into its numeric and canonical forms.
func ParseSignal(s string) (int32, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, "", fmt.Errorf("signal is empty")
	}

	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return 0, "", fmt.Errorf("signal number must be positive: %d", n)
		}
		name := unix.SignalName(syscall.Signal(n))
		if name == "" {
			return 0, "", fmt.Errorf("unknown signal number: %d", n)
		}
		return int32(n), name, nil
	}

	normalized := strings.ToUpper(s)
	if !strings.HasPrefix(normalized, "SIG") {
		normalized = "SIG" + normalized
	}
	sig := unix.SignalNum(normalized)
	if sig <= 0 {
		return 0, "", fmt.Errorf("unknown signal: %s", s)
	}
	name := unix.SignalName(sig)
	if name == "" {
		return 0, "", fmt.Errorf("unknown signal: %s", s)
	}
	return int32(sig), name, nil
}
