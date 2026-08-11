package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/ngicks/cmdman/cmdman/compose"
)

// composeUpMuxLayout is the layout selector the `compose up --mux` tail applies:
// the spec's first layout, always. Cycling to the next layout is what
// `compose mux up` is for; a bring-up that flipped the layout every time would
// not be re-runnable.
const composeUpMuxLayout = "0"

// ComposeUpMux opens the multiplexer dashboard for a project that was just
// brought up, reusing the spec `up` already loaded (no second load, no
// re-resolution). It is the tail of `compose up --mux`.
//
// Unlike `compose mux up`, a project without a "mux:" section is not an error:
// the up itself succeeded, so the missing section is reported as a warning on
// errOut and the dashboard is skipped. out receives mux's attach hint when the
// caller is outside the multiplexer.
func ComposeUpMux(
	ctx context.Context,
	svc *compose.Service,
	spec *compose.ComposeSpec,
	out, errOut io.Writer,
) error {
	if spec.Mux == nil {
		fmt.Fprintf(
			errOut,
			"warning: %s has no \"mux:\" section; --mux skipped\n",
			spec.ComposeFile,
		)
		return nil
	}
	return svc.MuxUp(ctx, compose.MuxUpOption{
		Selection: compose.SelectionFromSpec(spec),
		Layout:    composeUpMuxLayout,
		Stdout:    out,
	})
}
