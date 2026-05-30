package compose

// Phase is a single lifecycle state in a compose up/start/stop/down state
// trace. The set is shared by every lifecycle operation; which phases actually
// appear depends on the operation (e.g. only stop/down emit stopping/stopped).
//
// Phases are either transient ("…ing": work is in flight) or terminal (a
// result). Reporters render transient phases as in-progress and terminal phases
// as the command's final outcome.
type Phase string

const (
	// Transient (create phase).
	PhaseCreating   Phase = "creating"
	PhaseRecreating Phase = "recreating"
	// Terminal (create phase).
	PhaseCreated   Phase = "created"
	PhaseRecreated Phase = "recreated"
	PhaseUnchanged Phase = "unchanged"

	// Transient (start phase).
	PhaseStarting Phase = "starting"
	PhaseWaiting  Phase = "waiting"
	// Terminal (start phase). PhaseExited is reported when a started command was
	// awaited to completion (an after.Condition needed its terminal state).
	PhaseStarted Phase = "started"
	PhaseExited  Phase = "exited"

	// Transient (stop / down stop phase).
	PhaseStopping Phase = "stopping"
	// Terminal (stop / down stop phase).
	PhaseStopped Phase = "stopped"

	// Transient (down remove phase).
	PhaseRemoving Phase = "removing"
	// Terminal (down remove phase).
	PhaseRemoved Phase = "removed"

	// Terminal, any phase. PhaseSkipped marks a command that needed no action
	// (e.g. an already-terminal command on stop, or a running command whose
	// recreate was declined). PhaseFailed marks a monitored process that ended
	// without an exit code. PhaseError marks a failed compose operation step.
	PhaseSkipped Phase = "skipped"
	PhaseFailed  Phase = "failed"
	PhaseError   Phase = "error"
)

// Terminal reports whether p is a terminal phase (a result rather than work in
// flight).
func (p Phase) Terminal() bool {
	switch p {
	case PhaseCreating, PhaseRecreating, PhaseStarting, PhaseWaiting,
		PhaseStopping, PhaseRemoving:
		return false
	default:
		return true
	}
}

// Failed reports whether p is a terminal phase that represents a failure.
func (p Phase) Failed() bool {
	return p == PhaseFailed || p == PhaseError
}

// Event is one lifecycle state-transition emitted while an operation runs. The
// reconcile walk emits these from multiple goroutines, so a Reporter must be
// safe for concurrent use.
type Event struct {
	// Command is the compose command name (YAML map key).
	Command string
	// Phase is the state the command transitioned into.
	Phase Phase
	// Err is non-nil for a failure phase and carries the detail.
	Err error
	// ExitCode is the observed exit code when known (set on PhaseExited).
	ExitCode *int
}

// Reporter receives lifecycle progress events for a single compose operation.
// Implementations must be safe for concurrent use.
type Reporter interface {
	Report(Event)
}

// ServiceOption configures a Service at construction.
type ServiceOption func(*Service)

// WithReporter installs a progress Reporter that receives a state-trace event
// stream during up/start/stop/down. A nil reporter (the default) disables
// reporting entirely.
func WithReporter(r Reporter) ServiceOption {
	return func(s *Service) { s.reporter = r }
}

// report emits a single event to the installed reporter, if any. It is safe to
// call when no reporter is configured.
func (s *Service) report(command string, phase Phase, err error, exit *int) {
	if s.reporter == nil {
		return
	}
	s.reporter.Report(Event{Command: command, Phase: phase, Err: err, ExitCode: exit})
}
