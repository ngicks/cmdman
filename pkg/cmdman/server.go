package cmdman

import (
	"context"
	"io"
	"syscall"
	"time"

	pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

type monitorServer struct {
	pb.UnimplementedCommandMonitorServiceServer
	monitor *Monitor
}

func (s *monitorServer) Attach(stream pb.CommandMonitorService_AttachServer) error {
	// Send scrollback first.
	scrollback := s.monitor.ring.Bytes()
	if len(scrollback) > 0 {
		if err := stream.Send(&pb.AttachResponse{Stdout: scrollback}); err != nil {
			return err
		}
	}

	// Subscribe to live output.
	ch, unsub := s.monitor.fanout.Subscribe()
	defer unsub()

	// Read stdin from client in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				errCh <- err
				return
			}
			switch input := msg.Input.(type) {
			case *pb.AttachRequest_Stdin:
				if err := s.monitor.QueueStdin(stream.Context(), input.Stdin); err != nil {
					errCh <- err
					return
				}
			case *pb.AttachRequest_Resize:
				s.monitor.Resize(
					uint16(input.Resize.Rows),
					uint16(input.Resize.Cols),
				)
			}
		}
	}()

	// Send live output to client.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.AttachResponse{Stdout: data}); err != nil {
				return err
			}
		case err := <-errCh:
			if err == io.EOF {
				return nil
			}
			return err
		case <-ticker.C:
			state, _, _ := s.monitor.GetState()
			if state != store.StateStarting && state != store.StateRunning {
				return nil
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *monitorServer) Logs(
	req *pb.LogsRequest,
	stream pb.CommandMonitorService_LogsServer,
) error {
	// Send scrollback.
	scrollback := s.monitor.ring.Bytes()
	if len(scrollback) > 0 {
		if err := stream.Send(&pb.LogsResponse{Data: scrollback}); err != nil {
			return err
		}
	}

	if !req.Follow {
		return nil
	}

	// Follow live output.
	ch, unsub := s.monitor.fanout.Subscribe()
	defer unsub()

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.LogsResponse{Data: data}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *monitorServer) WriteStdin(
	ctx context.Context,
	req *pb.WriteStdinRequest,
) (*pb.WriteStdinResponse, error) {
	if err := s.monitor.QueueStdin(ctx, req.Stdin); err != nil {
		return nil, err
	}
	return &pb.WriteStdinResponse{}, nil
}

func (s *monitorServer) Signal(
	_ context.Context,
	req *pb.SignalRequest,
) (*pb.SignalResponse, error) {
	if err := s.monitor.SignalProcess(syscall.Signal(req.Signal)); err != nil {
		return nil, err
	}
	return &pb.SignalResponse{}, nil
}

func (s *monitorServer) Stop(_ context.Context, req *pb.StopRequest) (*pb.StopResponse, error) {
	if err := s.monitor.StopProcess(syscall.Signal(req.Signal)); err != nil {
		return nil, err
	}
	return &pb.StopResponse{}, nil
}

func (s *monitorServer) Status(_ context.Context, _ *pb.StatusRequest) (*pb.StatusResponse, error) {
	state, exitCode, pid := s.monitor.GetState()
	return &pb.StatusResponse{
		State:    state,
		ExitCode: int32(exitCode),
		Pid:      int32(pid),
	}, nil
}
