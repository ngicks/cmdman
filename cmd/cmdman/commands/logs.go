package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/ngicks/crabswarm/pkg/api/gen/proto/go/cmdman/v1"
)

func init() {
	rootCmd.AddCommand(logsCmd)
	logsCmd.Flags().BoolP("follow", "f", false, "Follow output")
}

var logsCmd = &cobra.Command{
	Use:   "logs [flags] ID|NAME",
	Short: "Show command output from scrollback buffer",
	Args:  cobra.ExactArgs(1),
	RunE:  runLogs,
}

func runLogs(cmd *cobra.Command, args []string) error {
	follow, _ := cmd.Flags().GetBool("follow")

	svc, err := cmdmanService()
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
	defer conn.Close()

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
		os.Stdout.Write(msg.Data)
	}
}
