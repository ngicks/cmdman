package monitor

import "time"

// The run end applies these on every platform, so they live outside the
// platform-specific sweeps.
const (
	// orphanGrace is how long a process the command left behind has to exit on
	// SIGTERM before it is killed outright.
	orphanGrace = 2 * time.Second
	// sweepBound caps the whole sweep, so a process that refuses to die cannot
	// keep the run from finishing. A process sitting in an uninterruptible wait
	// ignores SIGKILL too, and without a cap the run would hang on it.
	sweepBound = 10 * time.Second
)
