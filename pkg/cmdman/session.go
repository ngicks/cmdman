package cmdman

import (
	"context"

	cmdmanv1pb "github.com/ngicks/crabswarm/pkg/api/gen/proto/go/cmdman/v1"
	"google.golang.org/grpc"
)

// Session is an opaque attach session over the monitor protocol.
type Session struct {
	conn   *grpc.ClientConn
	client cmdmanv1pb.CommandMonitorServiceClient
	stream cmdmanv1pb.CommandMonitorService_AttachClient
}

// Signal forwards a signal to the remote command.
func (s *Session) Signal(ctx context.Context, sig int32) error {
	_, err := s.client.Signal(ctx, &cmdmanv1pb.SignalRequest{Signal: sig})
	return err
}

// Close closes the underlying transport.
func (s *Session) Close() error {
	return s.conn.Close()
}

// CloseSend closes the send side of the attach stream.
func (s *Session) CloseSend() error {
	return s.stream.CloseSend()
}

// Recv reads the next stdout chunk from the attach stream.
func (s *Session) Recv() ([]byte, error) {
	msg, err := s.stream.Recv()
	if err != nil {
		return nil, err
	}
	return msg.Stdout, nil
}

// SendStdin forwards stdin bytes to the remote command.
func (s *Session) SendStdin(data []byte) error {
	return s.stream.Send(&cmdmanv1pb.AttachRequest{
		Input: &cmdmanv1pb.AttachRequest_Stdin{Stdin: data},
	})
}

// Resize forwards a terminal resize event to the remote command.
func (s *Session) Resize(rows, cols int) error {
	return s.stream.Send(&cmdmanv1pb.AttachRequest{
		Input: &cmdmanv1pb.AttachRequest_Resize{
			Resize: &cmdmanv1pb.ResizeEvent{
				Rows: uint32(rows),
				Cols: uint32(cols),
			},
		},
	})
}
