package store

// CommandStateJSON stores mutable runtime fields in CommandState.JSON.
type CommandStateJSON struct {
	// MonitorPID is the PID of the monitor process.
	MonitorPID int `json:"monitor_pid,omitempty"`
	// SocketPath is the Unix socket path for the monitor gRPC server.
	SocketPath string `json:"socket_path,omitempty"`
	// StartedAt is the RFC3339 timestamp when the command started.
	StartedAt string `json:"started_at,omitempty"`
	// FinishedAt is the RFC3339 timestamp when the command finished.
	FinishedAt string `json:"finished_at,omitempty"`
	// RestartCount is how many times the command has been restarted.
	RestartCount int `json:"restart_count"`
	// Error contains error details when the command is in failed state.
	Error string `json:"error,omitempty"`
}
