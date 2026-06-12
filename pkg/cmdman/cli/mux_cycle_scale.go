package cli

import (
	"fmt"
	"io"

	"github.com/ngicks/cmdman/pkg/cmdman/mux"
)

// RenderCycleScaleResult renders the result of a cycle-scale operation to out.
// One line is written per window result in the format:
//
//	<session>:<window> <command> -> <resolvedName>
//
// When the pane was not visible in the current layout, an advisory suffix is
// appended:
//
//	<session>:<window> <command> -> <resolvedName> (not visible in layout "<layoutName>")
//
// Failures are not rendered here; the caller reports the error returned by
// CycleScale itself.
func RenderCycleScaleResult(out io.Writer, result mux.CycleScaleResult) {
	for _, r := range result.Results {
		if r.Visible {
			fmt.Fprintf(out, "%s:%s %s -> %s\n",
				r.SessionName, r.WindowName, r.Command, r.ResolvedName,
			)
		} else {
			fmt.Fprintf(out, "%s:%s %s -> %s (not visible in layout %q)\n",
				r.SessionName, r.WindowName, r.Command, r.ResolvedName, r.LayoutName,
			)
		}
	}
}
