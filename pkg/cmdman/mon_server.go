package cmdman

import (
	"context"
	"encoding"
	"io"
	"sync"
	"syscall"
	"time"

	pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type monitorServer struct {
	pb.UnimplementedCommandMonitorServiceServer
	monitor *Monitor
}

type SubscribeResult struct {
	Records <-chan logdriver.LogLine
	Unsub   func()
	Offset  any
}

func (m *Monitor) Subscribe(context.Context) (SubscribeResult, error) {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	records, unsub := m.outputBridge.Subscribe()
	var offset any
	if ow, ok := m.logWriter.(logdriver.OffsetWriter); ok {
		offset = ow.CurrentOffset()
	}
	return SubscribeResult{
		Records: records,
		Unsub:   unsub,
		Offset:  offset,
	}, nil
}

func (m *Monitor) readMonitorOut() (
	scrollback []byte,
	live <-chan logdriver.LogLine,
	unsub func(),
) {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	live, unsub = m.outputBridge.Subscribe()
	scrollback = m.ring.Bytes()
	return
}

func (s *monitorServer) Attach(stream pb.CommandMonitorService_AttachServer) error {
	scrollback, ch, unsub := s.monitor.readMonitorOut()
	defer unsub()

	if len(scrollback) > 0 {
		if err := stream.Send(&pb.AttachResponse{Stdout: scrollback}); err != nil {
			return err
		}
	}

	var wg sync.WaitGroup
	defer wg.Wait()

	errCh := make(chan error, 1)
	wg.Go(func() {
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
	})

	// Send live output to client.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case line, ok := <-ch:
			if !ok {
				return nil
			}
			data := line.Line
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

func (s *monitorServer) Subscribe(
	_ *pb.SubscribeRequest,
	stream pb.CommandMonitorService_SubscribeServer,
) error {
	sub, err := s.monitor.Subscribe(stream.Context())
	if err != nil {
		return err
	}
	defer sub.Unsub()

	offsetBytes, err := marshalOffset(sub.Offset)
	if err != nil {
		return err
	}
	if err := stream.Send(&pb.SubscribeResponse{
		Event: &pb.SubscribeResponse_Offset{
			Offset: &pb.SubscribeOffset{
				Driver: string(s.monitor.cfg.LogDriver),
				Offset: offsetBytes,
			},
		},
	}); err != nil {
		return err
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case line, ok := <-sub.Records:
			if !ok {
				return nil
			}
			if err := stream.Send(&pb.SubscribeResponse{
				Event: &pb.SubscribeResponse_Line{Line: logLineToProto(line)},
			}); err != nil {
				return err
			}
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

func marshalOffset(offset any) ([]byte, error) {
	if offset == nil {
		return nil, nil
	}
	if m, ok := offset.(encoding.BinaryMarshaler); ok {
		return m.MarshalBinary()
	}
	return nil, nil
}

func logLineToProto(line logdriver.LogLine) *pb.LogLine {
	return &pb.LogLine{
		Time:    timestamppb.New(line.Time),
		Stream:  protoLogStream(line.Stream),
		Partial: line.Partial,
		Line:    line.Line,
	}
}

func protoLogStream(s logdriver.Stream) pb.LogStream {
	switch s {
	case logdriver.StreamStdout, "":
		return pb.LogStream_LOG_STREAM_STDOUT
	case logdriver.StreamStderr:
		return pb.LogStream_LOG_STREAM_STDERR
	default:
		return pb.LogStream_LOG_STREAM_UNSPECIFIED
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
