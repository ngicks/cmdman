package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/compose"
)

// ResolveStatusTarget picks the command the status verbs address: the explicit
// argument when there is one, otherwise envID - the CMDMAN_CMD_ID cmdman injects
// into every command it supervises, which is how a command addresses itself
// without knowing its own id.
func ResolveStatusTarget(args []string, envID string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return args[0], nil
	}
	if envID != "" {
		return envID, nil
	}
	return "", fmt.Errorf(
		"no command given and %s is unset: pass ID|NAME, or run this from a supervised command",
		cmdman.ENV_CMDMAN_CMD_ID,
	)
}

// RenderReportedStatus writes the status a command reported about itself.
// format selects the layout:
//   - "": the status word alone, or the word and its detail separated by a tab.
//     A command that reported nothing prints nothing at all, so an empty output
//     is the answer to "did it report".
//   - "json": an indented JSON object.
//   - anything else: a Go text/template applied to the status.
func RenderReportedStatus(out io.Writer, info cmdman.ReportedStatusInfo, format string) error {
	switch format {
	case "":
		if info.Status == "" {
			return nil
		}
		if info.Detail != "" {
			_, err := fmt.Fprintf(out, "%s\t%s\n", info.Status, info.Detail)
			return err
		}
		_, err := fmt.Fprintln(out, info.Status)
		return err
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	default:
		return renderTemplate(out, []cmdman.ReportedStatusInfo{info}, format)
	}
}

// StatusFormatUsage returns the --format usage string for status get.
func StatusFormatUsage() string {
	t := reflect.TypeFor[cmdman.ReportedStatusInfo]()
	var fields []string
	for f := range t.Fields() {
		fields = append(fields, fmt.Sprintf(".%s (%s)", f.Name, f.Type))
	}
	return fmt.Sprintf(
		`Output format: "" (default, plain text), "json", or a Go text/template string.`+
			"\nTemplate fields:\n  %s\nTemplate functions: %s",
		strings.Join(fields, ", "),
		templateFuncList(),
	)
}

// composeStatusRow is the data model for the compose status table and its user
// --format, built like the other table rows (see composeLsRow).
type composeStatusRow struct {
	compose.CommandRuntimeState
	tableMeta
}

// Bell renders the bell column. It is a method rather than a template call so
// the published DefaultComposeStatusRowFormat keeps working; the rendering
// itself is the one every table shares.
func (r composeStatusRow) Bell() string { return bellMark(r.BellUnread) }

// DefaultComposeStatusRowFormat renders one row of the built-in compose status
// table: fixed columns laid out with "cell" against the measured widths, and
// the trailing TITLE column through "fit".
const DefaultComposeStatusRowFormat = `{{- cell .Command .W.Command -}}
{{- cell (printf "%v" .State) .W.State -}}
{{- cell (or .Status "-") .W.Status -}}
{{- cell .Bell .W.Bell -}}
{{- cell (or .Detail "-") .W.Detail -}}
{{- fit .Title .Win .W.Used -}}`

// RenderComposeStatus renders the compose status output. format selects the
// layout:
//   - "" or "table": the default width-aware aligned table.
//   - "json": an indented JSON array of per-command runtime states.
//   - anything else: a Go text/template applied per command.
func RenderComposeStatus(
	out io.Writer,
	states []compose.CommandRuntimeState,
	format string,
) error {
	if format == "json" {
		return renderJSONArray(out, states)
	}

	w := measureComposeStatus(states)
	meta := tableMeta{W: w, Win: terminalWidth(out)}
	rows := make([]composeStatusRow, len(states))
	for i, s := range states {
		rows[i] = composeStatusRow{CommandRuntimeState: s, tableMeta: meta}
	}

	if format == "" || format == "table" {
		fmt.Fprintln(out, cell("COMMAND", w["Command"])+cell("STATE", w["State"])+
			cell("STATUS", w["Status"])+cell("BELL", w["Bell"])+
			cell("DETAIL", w["Detail"])+"TITLE")
		format = DefaultComposeStatusRowFormat
	}
	return renderTemplate(out, rows, format)
}

func measureComposeStatus(states []compose.CommandRuntimeState) map[string]int {
	w := map[string]int{
		"Command": width("COMMAND"),
		"State":   width("STATE"),
		"Status":  width("STATUS"),
		"Bell":    width("BELL"),
		"Detail":  width("DETAIL"),
	}
	for _, s := range states {
		w["Command"] = max(w["Command"], width(s.Command))
		w["State"] = max(w["State"], width(fmt.Sprintf("%v", s.State)))
		w["Status"] = max(w["Status"], width(s.Status))
		w["Detail"] = max(w["Detail"], width(s.Detail))
	}
	w["Used"] = w["Command"] + w["State"] + w["Status"] + w["Bell"] +
		w["Detail"] + 5*len(columnGap)
	return w
}

// ComposeStatusFormatUsage returns the --format usage string for compose status.
func ComposeStatusFormatUsage() string {
	return composeFormatUsage[compose.CommandRuntimeState]()
}
