package model

import (
	"fmt"
	"strconv"
	"strings"
)

// RestartPolicy determines how the monitor handles command exits.
type RestartPolicy string

const (
	RestartPolicyNo        RestartPolicy = "no"
	RestartPolicyOnFailure RestartPolicy = "on-failure"
	RestartPolicyAlways    RestartPolicy = "always"
)

// IsRestartPolicy reports whether s is a valid RestartPolicy value.
func IsRestartPolicy(s string) bool {
	switch RestartPolicy(s) {
	case RestartPolicyNo, RestartPolicyOnFailure, RestartPolicyAlways:
		return true
	}
	return false
}

// ParseRestartPolicy parses a restart spec of the form "POLICY" or
// "on-failure:N", returning the bare policy and the maximum retry count.
// A retry count is only accepted with the "on-failure" policy; a zero count
// means unlimited retries.
func ParseRestartPolicy(s string) (RestartPolicy, int, error) {
	name, count, hasCount := strings.Cut(s, ":")
	if !IsRestartPolicy(name) {
		return "", 0, fmt.Errorf("invalid restart policy %q", name)
	}
	policy := RestartPolicy(name)
	if !hasCount {
		return policy, 0, nil
	}
	if policy != RestartPolicyOnFailure {
		return "", 0, fmt.Errorf(
			"max retry count is only valid with %q restart policy, got %q",
			RestartPolicyOnFailure, name,
		)
	}
	n, err := strconv.Atoi(count)
	if err != nil {
		return "", 0, fmt.Errorf("invalid max retry count %q: %w", count, err)
	}
	if n < 0 {
		return "", 0, fmt.Errorf("max retry count must be non-negative: %d", n)
	}
	return policy, n, nil
}
