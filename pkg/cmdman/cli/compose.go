package cli

import (
	"fmt"
	"io"

	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// PrintComposeLogs consumes log messages from compose.Service.Logs and writes
// each line to out (stdout) or errOut (stderr) by stream, prefixed with the
// originating compose command name. It returns the first write error.
func PrintComposeLogs(out, errOut io.Writer, msgs <-chan compose.LogMessage) error {
	for msg := range msgs {
		w := out
		if msg.Record.Line.Stream == logdriver.StreamStderr {
			w = errOut
		}
		if w == nil {
			continue
		}
		line := msg.Record.Line.Line
		if _, err := fmt.Fprintf(w, "[%s] %s", msg.Command, line); err != nil {
			return fmt.Errorf("write logs for command %q: %w", msg.Command, err)
		}
		// Add a newline when the line doesn't already end with one (partial lines).
		if len(line) > 0 && line[len(line)-1] != '\n' {
			if _, err := fmt.Fprintln(w); err != nil {
				return fmt.Errorf("write logs for command %q: %w", msg.Command, err)
			}
		}
	}
	return nil
}

// printStatusLine writes a left-aligned status column followed by a command name.
func printStatusLine(out io.Writer, status, command string) {
	fmt.Fprintf(out, "%-12s %s\n", status, command)
}

// reportErrors writes each error to errOut and, when any are present, returns a
// combined error naming how many of the given operations failed. It returns nil
// when errs is empty.
func reportErrors(errOut io.Writer, noun string, errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	for _, e := range errs {
		fmt.Fprintln(errOut, "error:", e)
	}
	return fmt.Errorf("%d %s(s) failed", len(errs), noun)
}

// printActions writes a status line per create action and returns the errors.
func printActions(out io.Writer, actions []compose.ActionOutcome) []error {
	var errs []error
	for _, a := range actions {
		status := a.Action
		if a.Err != nil {
			status = "error"
			errs = append(errs, a.Err)
		}
		printStatusLine(out, status, a.Command)
	}
	return errs
}

// printStartOutcomes writes a status line per start outcome and returns the errors.
func printStartOutcomes(out io.Writer, starts []compose.StartOutcome) []error {
	var errs []error
	for _, s := range starts {
		status := "started"
		if s.Err != nil {
			status = "error"
			errs = append(errs, s.Err)
		}
		printStatusLine(out, status, s.Command)
	}
	return errs
}

// printStopOutcomes writes a status line per stop outcome and returns the errors.
func printStopOutcomes(out io.Writer, stops []compose.StopOutcome) []error {
	var errs []error
	for _, s := range stops {
		status := "stopped"
		if s.Err != nil {
			status = "error"
			errs = append(errs, s.Err)
		}
		printStatusLine(out, status, s.Command)
	}
	return errs
}

// PrintCreateResult writes a status line per create action and returns a combined
// error when any action failed.
func PrintCreateResult(out, errOut io.Writer, result *compose.CreateResult) error {
	return reportErrors(errOut, "compose action", printActions(out, result.Actions))
}

// PrintUpResult writes a status line per create action and per start, then
// returns a combined error when any operation failed.
func PrintUpResult(out, errOut io.Writer, result *compose.UpResult) error {
	errs := printActions(out, result.Actions)
	errs = append(errs, printStartOutcomes(out, result.Starts)...)
	return reportErrors(errOut, "compose operation", errs)
}

// PrintStartResult writes a status line per start outcome and returns a combined
// error when any start failed.
func PrintStartResult(out, errOut io.Writer, starts []compose.StartOutcome) error {
	return reportErrors(errOut, "compose start operation", printStartOutcomes(out, starts))
}

// PrintStopResult writes a status line per stop outcome and returns a combined
// error when any stop failed.
func PrintStopResult(out, errOut io.Writer, stops []compose.StopOutcome) error {
	return reportErrors(errOut, "compose stop operation", printStopOutcomes(out, stops))
}

// PrintDownResult writes a status line per stop and remove outcome and returns a
// combined error when any operation failed.
func PrintDownResult(out, errOut io.Writer, result *compose.DownResult) error {
	errs := printStopOutcomes(out, result.Stops)
	for _, r := range result.Removes {
		status := "removed"
		if r.Err != nil {
			status = "error"
			errs = append(errs, r.Err)
		}
		printStatusLine(out, status, r.Command)
	}
	return reportErrors(errOut, "compose down operation", errs)
}

// PrintSignalResult writes a status line per signal outcome and returns a
// combined error when any signal failed.
func PrintSignalResult(out, errOut io.Writer, outcomes []compose.SignalOutcome) error {
	var errs []error
	for _, o := range outcomes {
		status := "signaled"
		if o.Err != nil {
			status = "error"
			errs = append(errs, o.Err)
		}
		printStatusLine(out, status, o.Command)
	}
	return reportErrors(errOut, "compose signal operation", errs)
}

// PrintWaitResult writes a status line per wait outcome, including the exit code
// when available, and returns a combined error when any wait failed.
func PrintWaitResult(out, errOut io.Writer, outcomes []compose.WaitOutcome) error {
	var errs []error
	for _, o := range outcomes {
		switch {
		case o.Err != nil:
			fmt.Fprintf(out, "%-12s %s (error: %v)\n", "error", o.Command, o.Err)
			errs = append(errs, o.Err)
		case o.ExitCode != nil:
			fmt.Fprintf(out, "%-12s %s (exit code: %d)\n", "done", o.Command, *o.ExitCode)
		default:
			printStatusLine(out, "done", o.Command)
		}
	}
	return reportErrors(errOut, "compose wait operation", errs)
}

// PrintRestartResult writes a status line per restart outcome and returns a
// combined error when any stop or start failed.
func PrintRestartResult(out, errOut io.Writer, restarts []compose.RestartOutcome) error {
	for _, r := range restarts {
		switch {
		case r.StopErr != nil:
			fmt.Fprintf(out, "%-12s %s (stop: %v)\n", "error", r.Command, r.StopErr)
		case r.StartErr != nil:
			fmt.Fprintf(out, "%-12s %s (start: %v)\n", "error", r.Command, r.StartErr)
		default:
			printStatusLine(out, "restarted", r.Command)
		}
	}
	var errs []error
	for _, r := range restarts {
		if r.StopErr != nil {
			errs = append(errs, r.StopErr)
		}
		if r.StartErr != nil {
			errs = append(errs, r.StartErr)
		}
	}
	return reportErrors(errOut, "compose restart operation", errs)
}
