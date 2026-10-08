package compose

import "github.com/ngicks/cmdman/cmdman/logdriver"

// Phase is a single lifecycle state in the state trace of a compose lifecycle
// operation: create, up, start, stop, restart, down or scale. The set is shared
// by every lifecycle operation; which phases actually appear depends on the
// operation (e.g. only the operations that stop a command emit
// stopping/stopped).
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
	// Terminal (start phase). PhaseExited is reported when a running command was
	// awaited to completion (an after.Condition needed its terminal state).
	PhaseRunning Phase = "running"
	PhaseExited  Phase = "exited"

	// Transient (stop / down stop phase).
	PhaseStopping Phase = "stopping"
	// Terminal (stop / down stop phase).
	PhaseStopped Phase = "stopped"

	// Transient (down remove phase).
	PhaseRemoving Phase = "removing"
	// Terminal (down remove phase).
	PhaseRemoved Phase = "removed"

	// Terminal, any phase. PhaseSkipped marks a command the operation left as
	// it was. Without [Event.Err] it needed no action (e.g. an
	// already-terminal command on stop, or a running orphan left in place).
	// With Err an earlier failure held it back: up does not start a replica
	// whose create failed, and down keeps a replica whose stop hook failed.
	// PhaseFailed marks a monitored process that ended without an exit code.
	// PhaseError marks a failed compose operation step.
	PhaseSkipped Phase = "skipped"
	PhaseFailed  Phase = "failed"
	PhaseError   Phase = "error"

	// Hook phases belong to one run of one lifecycle hook event rather than to
	// the replica itself; their events set [Event.Hook]. PhaseHookRunning is
	// transient. PhaseHookSucceeded, PhaseHookFailed, PhaseHookWarning and
	// PhaseHookIgnored end the run; PhaseHookWarning is a failure that on_error:
	// continue let pass, and PhaseHookIgnored one that on_error: ignore let pass.
	// PhaseHookOutput is no state at all: it carries one line of output while
	// the hook runs.
	//
	// A PhaseHookWarning event without [Event.Hook] belongs to the replica
	// itself: a forced teardown could not read the hooks stored on it and went on
	// without them.
	PhaseHookRunning   Phase = "hook-running"
	PhaseHookSucceeded Phase = "hook-succeeded"
	PhaseHookFailed    Phase = "hook-failed"
	PhaseHookWarning   Phase = "hook-warning"
	PhaseHookIgnored   Phase = "hook-ignored"
	PhaseHookOutput    Phase = "hook-output"
)

// Terminal reports whether p is a terminal phase (a result rather than work in
// flight).
func (p Phase) Terminal() bool {
	switch p {
	case PhaseCreating, PhaseRecreating, PhaseStarting, PhaseWaiting,
		PhaseStopping, PhaseRemoving, PhaseHookRunning, PhaseHookOutput:
		return false
	default:
		return true
	}
}

// Failed reports whether p is a terminal phase that represents a failure.
func (p Phase) Failed() bool {
	return p == PhaseFailed || p == PhaseError || p == PhaseHookFailed
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
	// ExitCode is the observed exit code when known (set on PhaseExited and on
	// the phases that end a hook run).
	ExitCode *int
	// ForceKilled is set on PhaseStopped when the stop ran out the replica's
	// grace period and ended it with SIGKILL. A process that left the replica's
	// session may have survived it.
	ForceKilled bool

	// ScaleIndex is the 1-based scale index of the replica, set on hook events.
	ScaleIndex int
	// Hook is the name of the hook item a hook event belongs to. It is empty
	// for every other event, and for a PhaseHookWarning of the replica itself.
	Hook string
	// Lifecycle is the event the hook runs for.
	Lifecycle LifecycleEvent
	// Exec is the cmdman command name of the exec command running the hook.
	Exec string
	// Stream and Line carry one line of the hook's output on PhaseHookOutput.
	Stream logdriver.Stream
	Line   string
}

// Reporter receives lifecycle progress events for a single compose operation.
// Implementations must be safe for concurrent use.
type Reporter interface {
	Report(Event)
}

// ServiceOption configures a Service at construction.
type ServiceOption func(*Service)

// WithReporter installs a progress Reporter that receives a state-trace event
// stream during the lifecycle operations: create, up, start, stop, restart, down
// and scale. A nil reporter (the default) disables reporting entirely.
func WithReporter(r Reporter) ServiceOption {
	return func(s *Service) { s.reporter = r }
}

// report emits a single event to the installed reporter, if any. It is safe to
// call when no reporter is configured.
func (s *Service) report(command string, phase Phase, err error, exit *int) {
	s.reportEvent(Event{Command: command, Phase: phase, Err: err, ExitCode: exit})
}

// reportStopped emits PhaseStopped for command, saying whether its stop
// force-killed it.
func (s *Service) reportStopped(command string, exit *int, forceKilled bool) {
	s.reportEvent(Event{
		Command:     command,
		Phase:       PhaseStopped,
		ExitCode:    exit,
		ForceKilled: forceKilled,
	})
}

// reportEvent emits ev to the installed reporter, if any.
func (s *Service) reportEvent(ev Event) {
	if s.reporter == nil {
		return
	}
	s.reporter.Report(ev)
}

// reportReplicas emits phase for each replica of cmd whose 1-based scale index
// is in scaleIndices, labeling each via [instanceDisplayName]. This keeps the
// progress view consistent with the create phase: a scaled command's replicas
// each get their own line ("<name>-1".."<name>-N") rather than collapsing onto
// the bare command name. An unscaled command yields the single bare-name line.
func (s *Service) reportReplicas(
	cmd Command,
	scaleIndices []int,
	phase Phase,
	err error,
	exit *int,
) {
	if s.reporter == nil {
		return
	}
	for _, idx := range scaleIndices {
		s.report(instanceDisplayName(cmd, idx), phase, err, exit)
	}
}
