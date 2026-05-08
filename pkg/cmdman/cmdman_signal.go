package cmdman

import (
	"context"
	"fmt"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func (s *Service) Signal(ctx context.Context, idOrName string, sig int32) error {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	id, err := st.ResolveID(idOrName)
	if err != nil {
		return fmt.Errorf("resolve command: %w", err)
	}
	if err := signalOne(ctx, st, id, sig); err != nil {
		return fmt.Errorf("signal command %s: %w", idOrName, err)
	}
	return nil
}

func signalOne(ctx context.Context, st *store.Store, id string, sig int32) error {
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return err
	}
	if stateJSON.SocketPath == "" {
		return fmt.Errorf("no socket path")
	}

	conn, err := grpc.NewClient(
		"unix://"+stateJSON.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	_, err = client.Signal(ctx, &cmdmanv1pb.SignalRequest{Signal: sig})
	return err
}
