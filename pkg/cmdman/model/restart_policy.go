package model

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
