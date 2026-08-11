package monitor

import (
	"context"
	"encoding"
	"io"
	"syscall"

	"github.com/ngicks/cmdman/cmdman/logdriver"
	pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
		// For a TTY command, hand over a coherent snapshot of the current screen
		// from the server-side mirror; it reconstructs exactly regardless of ring
		// rotation. Fall back to raw ring bytes when the mirror is absent or a vt
		// hazard disabled it.
		if m.cfg.Tty {
			sub.Scrollback = m.screen.snapshot()
		}
		if sub.Scrollback == nil {
			sub.Scrollback = m.ring.Bytes()
		}
		sub.TerminalMode = m.terminalState.Replay()
	}
	sub.StateChanges, sub.unsubState = m.subscribeStateChange()
	return sub
}

func (s *monitorServer) Attach(stream pb.CommandMonitorService_AttachServer) error {
	// Before anything that can fail: an attach clears the bell (D11), and the
	// paired end must run even when the stream dies on its first Send.
	s.monitor.runtimeState.attachBegin()
	defer s.monitor.runtimeState.attachEnd()

	sub := s.monitor.subscribeOutput(true)
	defer sub.Unsub()

	// Blocked sequences are removed here rather than at ingest, so the ring
	// buffer, the log file, and the monitor's own capture keep what the command
	// actually wrote (D40). The filter is per-stream because it carries parse
	// state across chunks; the block set is sampled once, so an attach that
	// outlives a restart keeps the hook config it opened with until it
	// reattaches.
	//
	// The filter starts at ground, and so does the viewer it feeds: a sequence
	// straddling the subscription point had its opening bytes written before
	// this attach existed, so its tail is visible text to the viewer's own
	// terminal with or without a filter in the way. The filter tracks that
	// terminal rather than diverging from it, which is why starting at ground
	// is right and not merely convenient. Nothing is missed between the two
	// either: subscribeOutput takes outputMu, and logCommandOutput holds it
	// while writing both the ring and the broadcaster, so the snapshot below
	// and the first live record meet exactly - no gap, no overlap.
	filter := newHookFilter(s.monitor.hooks.blocks())

	// Report the current PTY size first so a viewer sizes its terminal emulator
	// to the command's actual render dimensions before processing scrollback.
	if rows, cols, ok := s.monitor.PtySize(); ok {
		if err := stream.Send(&pb.AttachResponse{
			Resize: &pb.ResizeEvent{Rows: uint32(rows), Cols: uint32(cols)},
		}); err != nil {
			return err
		}
	}

	if scrollback := filter.filter(sub.Scrollback); len(scrollback) > 0 {
		if err := stream.Send(&pb.AttachResponse{Stdout: scrollback}); err != nil {
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
			data := filter.filter(line.Line)
			if len(data) == 0 {
				// The whole chunk was a blocked sequence.
				continue
			}
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
		State:        string(state),
		ExitCode:     int32(exitCode),
		Pid:          int32(pid),
		RuntimeState: protoRuntimeState(s.monitor.runtimeState.snapshot().view()),
	}, nil
}

func (s *monitorServer) WatchRuntimeState(
	_ *pb.WatchRuntimeStateRequest,
	stream pb.CommandMonitorService_WatchRuntimeStateServer,
) error {
	// End the stream once the monitor stops supervising, the way Attach and
	// Subscribe do: a handler parked forever would block GracefulStop.
	states, unsubState := s.monitor.subscribeStateChange()
	defer unsubState()

	return streamRuntimeState(
		stream.Context(),
		s.monitor.runtimeState,
		states,
		defaultTitleDebounce,
		func(v runtimeView) error {
			return stream.Send(&pb.WatchRuntimeStateResponse{State: protoRuntimeState(v)})
		},
	)
}

func (s *monitorServer) SetReportedStatus(
	_ context.Context,
	req *pb.SetReportedStatusRequest,
) (*pb.SetReportedStatusResponse, error) {
	reported, ok := reportedStatusFromProto(req.Status)
	if !ok || reported == reportedStatusNone {
		// Clearing is DeleteReportedStatus, so an unset status here is a bug in
		// the caller, not a request to clear.
		return nil, status.Errorf(codes.InvalidArgument, "unknown status %v", req.Status)
	}
	s.monitor.runtimeState.setReport(reported, req.Detail)
	return &pb.SetReportedStatusResponse{}, nil
}

func (s *monitorServer) GetReportedStatus(
	_ context.Context,
	_ *pb.GetReportedStatusRequest,
) (*pb.GetReportedStatusResponse, error) {
	snap := s.monitor.runtimeState.snapshot()
	return &pb.GetReportedStatusResponse{
		Status: protoReportedStatus(snap.Status),
		Detail: snap.Detail,
	}, nil
}

func (s *monitorServer) DeleteReportedStatus(
	_ context.Context,
	_ *pb.DeleteReportedStatusRequest,
) (*pb.DeleteReportedStatusResponse, error) {
	s.monitor.runtimeState.clearReport()
	return &pb.DeleteReportedStatusResponse{}, nil
}

func protoRuntimeState(v runtimeView) *pb.RuntimeState {
	return &pb.RuntimeState{
		Title:      v.Title,
		Status:     protoReportedStatus(v.Status),
		Detail:     v.Detail,
		BellUnread: v.BellUnread,
	}
}

func protoReportedStatus(s reportedStatus) pb.ReportedStatus {
	switch s {
	case reportedStatusWorking:
		return pb.ReportedStatus_REPORTED_STATUS_WORKING
	case reportedStatusWaiting:
		return pb.ReportedStatus_REPORTED_STATUS_WAITING
	case reportedStatusDone:
		return pb.ReportedStatus_REPORTED_STATUS_DONE
	default:
		return pb.ReportedStatus_REPORTED_STATUS_UNSPECIFIED
	}
}

func reportedStatusFromProto(s pb.ReportedStatus) (reportedStatus, bool) {
	switch s {
	case pb.ReportedStatus_REPORTED_STATUS_WORKING:
		return reportedStatusWorking, true
	case pb.ReportedStatus_REPORTED_STATUS_WAITING:
		return reportedStatusWaiting, true
	case pb.ReportedStatus_REPORTED_STATUS_DONE:
		return reportedStatusDone, true
	case pb.ReportedStatus_REPORTED_STATUS_UNSPECIFIED:
		return reportedStatusNone, true
	default:
		return reportedStatusNone, false
	}
}
