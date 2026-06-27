package cmdman

import (
	"context"
	"encoding"
	"io"
	"syscall"

	pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type monitorServer struct {
	pb.UnimplementedCommandMonitorServiceServer
	monitor *Monitor
}

type monitorSubscription struct {
	Records      <-chan logdriver.LogLine
	StateChanges <-chan monitorStateChange
	unsubRecords func()
	unsubState   func()
	Offset       any
	Scrollback   []byte
	TerminalMode []byte
}

func (s monitorSubscription) Unsub() {
	if s.unsubRecords != nil {
		s.unsubRecords()
	}
	if s.unsubState != nil {
		s.unsubState()
	}
}

func (m *Monitor) subscribeOutput(scrollback bool) monitorSubscription {
	m.outputMu.Lock()
	defer m.outputMu.Unlock()
	records, unsub := m.outputBridge.Subscribe()
	var offset any
	if ow, ok := m.logWriter.(logdriver.OffsetWriter); ok {
		offset = ow.CurrentOffset()
	}
	sub := monitorSubscription{
		Records:      records,
		unsubRecords: unsub,
		Offset:       offset,
	}
	if scrollback {
		sub.Scrollback = m.ring.Bytes()
		sub.TerminalMode = m.terminalState.Replay()
	}
	sub.StateChanges, sub.unsubState = m.subscribeStateChange()
	return sub
}

func (s *monitorServer) Attach(stream pb.CommandMonitorService_AttachServer) error {
	sub := s.monitor.subscribeOutput(true)
	defer sub.Unsub()

	// Report the current PTY size first so a viewer sizes its terminal emulator
	// to the command's actual render dimensions before processing scrollback.
	if rows, cols, ok := s.monitor.PtySize(); ok {
		if err := stream.Send(&pb.AttachResponse{
			Resize: &pb.ResizeEvent{Rows: uint32(rows), Cols: uint32(cols)},
		}); err != nil {
			return err
		}
	}

	if len(sub.Scrollback) > 0 {
		if err := stream.Send(&pb.AttachResponse{Stdout: sub.Scrollback}); err != nil {
			return err
		}
	}
	if len(sub.TerminalMode) > 0 {
		if err := stream.Send(&pb.AttachResponse{Stdout: sub.TerminalMode}); err != nil {
			return err
		}
	}

	// The stdin/resize reader is registered on Monitor.wg, not joined
	// in-handler: stream.Recv blocks until either the client sends a
	// message or the gRPC framework cancels the stream context, and the
	// framework only cancels the context after this handler returns. So
	// joining the reader here would deadlock when the handler tries to
	// exit because the command died (e.g. Ctrl-C). Monitor.wg lets the
	// supervisor join all such per-request goroutines once GracefulStop
	// has torn down the streams.
	errCh := make(chan error, 1)
	s.monitor.wg.Go(func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			switch input := msg.Input.(type) {
			case *pb.AttachRequest_Stdin:
				if err := s.monitor.QueueStdin(stream.Context(), input.Stdin); err != nil {
					select {
					case errCh <- err:
					default:
					}
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

	for {
		select {
		case line, ok := <-sub.Records:
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
		case state, ok := <-sub.StateChanges:
			if !ok {
				return nil
			}
			if !isMonitorActiveState(state.State) {
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
	sub := s.monitor.subscribeOutput(false)
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
		case state, ok := <-sub.StateChanges:
			if !ok {
				return nil
			}
			if !isMonitorActiveState(state.State) {
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
		State:    string(state),
		ExitCode: int32(exitCode),
		Pid:      int32(pid),
	}, nil
}
