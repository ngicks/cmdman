package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman"
)

func logsCmd(parent *cobra.Command, rootCfg *cmdman.CmdmanConfig) {
	var flagFollow bool

	cmd := &cobra.Command{
		Use:   "logs [flags] ID|NAME",
		Short: "Show command output from scrollback buffer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(cmd, args, rootCfg, flagFollow)
		},
	}

	cmd.Flags().BoolVarP(&flagFollow, "follow", "f", false, "Follow output")

	parent.AddCommand(cmd)
}

func runLogs(cmd *cobra.Command, args []string, rootCfg *cmdman.CmdmanConfig, follow bool) error {
	svc, err := cmdmanService(rootCfg)
	if err != nil {
		return err
	}
	endpoint, err := svc.ResolveMonitor(cmd.Context(), args[0])
	if err != nil {
		return err
	}

	conn, err := grpc.NewClient(
		"unix://"+endpoint.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("connect to monitor: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client := pb.NewCommandMonitorServiceClient(conn)
	stream, err := client.Logs(cmd.Context(), &pb.LogsRequest{Follow: follow})
	if err != nil {
		return fmt.Errorf("logs: %w", err)
	}

	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		_, _ = os.Stdout.Write(msg.Data)
	}
}
