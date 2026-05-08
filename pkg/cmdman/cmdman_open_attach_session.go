package cmdman

import (
	"context"
	"fmt"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func (s *Service) OpenAttachSession(
	ctx context.Context,
	idOrName string,
) (*Session, error) {
	endpoint, err := s.ResolveMonitor(ctx, idOrName)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(
		"unix://"+endpoint.SocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to monitor: %w", err)
	}

	client := cmdmanv1pb.NewCommandMonitorServiceClient(conn)
	stream, err := client.Attach(ctx)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("attach: %w", err)
	}

	return &Session{
		conn:   conn,
		client: client,
		stream: stream,
	}, nil
}
