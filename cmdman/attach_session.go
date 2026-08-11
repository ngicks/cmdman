package cmdman

import (
	"context"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
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

// AttachMessage is one decoded attach-stream message: either raw stdout bytes or
// a PTY size report (Resize == true).
type AttachMessage struct {
	Stdout []byte
	Rows   int
	Cols   int
	Resize bool
}

// RecvMessage reads the next attach message, distinguishing a PTY size report
// from raw stdout bytes.
func (s *Session) RecvMessage() (AttachMessage, error) {
	msg, err := s.stream.Recv()
	if err != nil {
		return AttachMessage{}, err
	}
	if r := msg.GetResize(); r != nil {
		return AttachMessage{Resize: true, Rows: int(r.Rows), Cols: int(r.Cols)}, nil
	}
	return AttachMessage{Stdout: msg.Stdout}, nil
}

// Recv reads the next stdout chunk, skipping PTY size reports (the interactive
// attach drives its own resizes and ignores server size reports).
func (s *Session) Recv() ([]byte, error) {
	for {
		m, err := s.RecvMessage()
		if err != nil {
			return nil, err
		}
		if m.Resize {
			continue
		}
		return m.Stdout, nil
	}
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
