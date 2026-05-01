package commands

import (
	"github.com/ngicks/crabswarm/pkg/cmdman"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(sendKeysCmd)
	sendKeysCmd.Flags().BoolP("literal", "l", false, "Send keys literally, without translating key names")
	sendKeysCmd.Flags().BoolP("hex", "H", false, "Treat keys as hexadecimal byte values")
	sendKeysCmd.Flags().IntP("repeat-count", "N", 1, "Repeat the key sequence N times")
}

var sendKeysCmd = &cobra.Command{
	Use:     "send-keys [flags] ID|NAME KEY [KEY...]",
	Aliases: []string{"send"},
	Short:   "Send key input to a running command PTY",
	Args:    cobra.MinimumNArgs(2),
	RunE:    runSendKeys,
}

func runSendKeys(cmd *cobra.Command, args []string) error {
	literal, _ := cmd.Flags().GetBool("literal")
	hexMode, _ := cmd.Flags().GetBool("hex")
	repeatCount, _ := cmd.Flags().GetInt("repeat-count")

	svc, err := cmdmanService()
	if err != nil {
		return err
	}

	return svc.SendKeys(cmd.Context(), args[0], cmdman.SendKeysRequest{
		Keys:        args[1:],
		Literal:     literal,
		Hex:         hexMode,
		RepeatCount: repeatCount,
	})
}
