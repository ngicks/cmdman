package commands

import (
	"github.com/spf13/cobra"
)

func muxFrameCmd(parent *cobra.Command, rf *rootFlags, parentSession *string) {
	cmd := &cobra.Command{
		Use:   "frame",
		Short: "Show, hide, or cycle the frame around the current window",
		Long: `Control the frame: the components docked around the edges of a window.

A frame is a per-window fixture, so the verbs act on the window you are sitting
in and must run inside the multiplexer. --session names a session instead, and
then its current window is the target.

A def is a bare name resolved under the frame config dir (<name>.yaml/.yml) or
a path to a def file. Only show takes one; with no def it falls back to the
config key default_frame.

Subcommands: show, hide, cycle, ls.`,
	}

	muxFrameShowCmd(cmd, rf, parentSession)
	muxFrameHideCmd(cmd, rf, parentSession)
	muxFrameCycleCmd(cmd, rf, parentSession)
	muxFrameLsCmd(cmd, parentSession)

	parent.AddCommand(cmd)
}
