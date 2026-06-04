package cmdman

import (
	"context"
	"fmt"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/model"
	"google.golang.org/grpc"
)

func (s *Service) Signal(ctx context.Context, idOrName string, sig int32) error {
	conn, err := s.connectMonitorByName(ctx, idOrName)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)

	if _, err := client.Signal(
		ctx,
		&cmdmanv1pb.SignalRequest{Signal: sig},
		grpc.WaitForReady(true),
	); err != nil {
		return fmt.Errorf("signal command %s: %w", idOrName, err)
	}

	s.emitEvent(ctx, model.Event{
		Time: time.Now().UTC(),
		Type: model.EventTypeSignaled,
		ID:   idOrName,
		Attrs: map[string]string{
			"signal": fmt.Sprintf("%d", sig),
		},
	})

	return nil
}
