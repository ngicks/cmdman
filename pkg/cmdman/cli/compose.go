package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"text/template"
	"time"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/compose"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
)

// PrintComposeLogs consumes log messages from compose.Service.Logs and writes
// each line to out (stdout) or errOut (stderr) by stream, prefixed with the log
// timestamp and originating compose command name. It returns the first write
// error.
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
		if _, err := fmt.Fprintf(
			w,
			"%s %s |%s",
			formatLogTime(msg.Record.Line.Time),
			msg.Command,
			line,
		); err != nil {
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

// RenderComposeProjects renders the compose ls output. format selects the
// layout:
//   - "" or "table": the default tab-separated table.
//   - "json": an indented JSON array of project summaries.
//   - anything else: a Go text/template applied per project summary.
func RenderComposeProjects(out io.Writer, summaries []compose.ProjectSummary, format string) error {
	switch format {
	case "", "table":
		printComposeProjectsTable(out, summaries)
		return nil
	case "json":
		return renderJSONArray(out, summaries)
	default:
		return renderTemplate(out, summaries, format)
	}
}

func printComposeProjectsTable(out io.Writer, summaries []compose.ProjectSummary) {
	fmt.Fprintln(out, "PROJECT\tCOMMANDS\tRUNNING\tEXITED\tFAILED\tWORKDIR\tFILE")
	for _, summary := range summaries {
		fmt.Fprintf(
			out,
			"%s\t%d\t%d\t%d\t%d\t%s\t%s\n",
			summary.Project,
			summary.Commands,
			summary.Running,
			summary.Exited,
			summary.Failed,
			summary.WorkDir,
			summary.ComposeFile,
		)
	}
}

// RenderComposePs renders the compose ps output. format selects the layout:
//   - "" or "table": the default tab-separated table.
//   - "json": an indented JSON array of command statuses.
//   - anything else: a Go text/template applied per command status.
func RenderComposePs(out io.Writer, statuses []compose.CommandStatus, format string) error {
	switch format {
	case "", "table":
		printComposePsTable(out, statuses)
		return nil
	case "json":
		return renderJSONArray(out, statuses)
	default:
		return renderTemplate(out, statuses, format)
	}
}

func printComposePsTable(out io.Writer, statuses []compose.CommandStatus) {
	fmt.Fprintln(out, "COMMAND\tID\tNAME\tSTATE\tEXIT CODE\tARGV")
	for _, status := range statuses {
		exitCode := "-"
		if status.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *status.ExitCode)
		}
		id := status.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(
			out,
			"%s\t%s\t%s\t%s\t%s\t%s\n",
			status.Command,
			id,
			status.Name,
			status.State,
			exitCode,
			strings.Join(status.Argv, " "),
		)
	}
}

// RenderComposeInspect renders the compose inspect output. When format is empty
// it emits an indented JSON array of inspect outputs. Otherwise format is a Go
// text/template applied per output, newline-terminated.
func RenderComposeInspect(out io.Writer, outputs []*cmdman.InspectOutput, format string) error {
	if format == "" {
		return renderJSONArray(out, outputs)
	}
	return renderTemplate(out, outputs, format)
}

// renderJSONArray writes v as an indented JSON document. A nil slice is
// normalized to an empty array so the output is always valid JSON ("[]" rather
// than "null").
func renderJSONArray[T any](out io.Writer, items []T) error {
	if items == nil {
		items = []T{}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

// renderTemplate applies a Go text/template to each item, newline-terminated.
func renderTemplate[T any](out io.Writer, items []T, format string) error {
	tmpl, err := template.New("format").Funcs(templateFuncMap).Parse(format)
	if err != nil {
		return fmt.Errorf("parse format template: %w", err)
	}
	for _, item := range items {
		if err := tmpl.Execute(out, item); err != nil {
			return fmt.Errorf("execute format template: %w", err)
		}
		fmt.Fprintln(out)
	}
	return nil
}

// ComposePsFormatUsage returns the --format usage string for compose ps.
func ComposePsFormatUsage() string {
	return composeFormatUsage[compose.CommandStatus]()
}

// ComposeLsFormatUsage returns the --format usage string for compose ls.
func ComposeLsFormatUsage() string {
	return composeFormatUsage[compose.ProjectSummary]()
}

func composeFormatUsage[T any]() string {
	t := reflect.TypeFor[T]()
	var fields []string
	for f := range t.Fields() {
		fields = append(fields, fmt.Sprintf(".%s (%s)", f.Name, f.Type))
	}
	return fmt.Sprintf(
		`Output format: "table" (default), "json", or a Go text/template string.`+
			"\nTemplate fields:\n  %s\nTemplate functions: %s",
		strings.Join(fields, ", "),
		templateFuncList(),
	)
}

func formatLogTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
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

// PrintCreateResult writes a status line per create action and returns a combined
// error when any action failed.
func PrintCreateResult(out, errOut io.Writer, result *compose.CreateResult) error {
	return reportErrors(errOut, "compose action", printActions(out, result.Actions))
}

// Note: compose up/start/stop/down no longer print a static summary here; they
// stream a live state trace (or JSONL) through cli.ComposeProgress and derive
// the command exit status from {Up,Start,Stop,Down}ResultErr in progress.go.

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

// PrintSendKeysResult writes a status line per send-keys outcome and returns a
// combined error when any send failed.
func PrintSendKeysResult(out, errOut io.Writer, outcomes []compose.SendKeysOutcome) error {
	var errs []error
	for _, o := range outcomes {
		status := "sent"
		if o.Err != nil {
			status = "error"
			errs = append(errs, o.Err)
		}
		printStatusLine(out, status, o.Command)
	}
	return reportErrors(errOut, "compose send-keys operation", errs)
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
