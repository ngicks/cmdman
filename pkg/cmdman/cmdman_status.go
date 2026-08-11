package cmdman

import (
	"context"
	"fmt"
	"strings"
	"time"

	cmdmanv1pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

// statusCallTimeout bounds one reported-status RPC. A command reporting about
// itself from a hook must fail fast when its monitor is gone instead of blocking
// the command that called it, so these calls are neither WaitForReady nor bound
// only by the caller's context.
const statusCallTimeout = 3 * time.Second

// ReportedStatusInfo is what a command last reported about itself. The zero
// value means "nothing reported", which is also what a command with no live
// monitor has: the status is per-run state held by the monitor.
//
// CLI-output type; see InspectOutput for why it carries no json name tags.
type ReportedStatusInfo struct {
	Status string `json:",omitzero"`
	Detail string `json:",omitzero"`
}

// ParseReportedStatus maps a status as spelled on the CLI onto its canonical
// name. Case and surrounding space are forgiven; anything outside the
// vocabulary is an error.
func ParseReportedStatus(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ReportedStatusWorking:
		return ReportedStatusWorking, nil
	case ReportedStatusWaiting:
		return ReportedStatusWaiting, nil
	case ReportedStatusDone:
		return ReportedStatusDone, nil
	}
	return "", fmt.Errorf(
		"unknown status %q: want %s, %s or %s",
		s, ReportedStatusWorking, ReportedStatusWaiting, ReportedStatusDone,
	)
}

func reportedStatusProto(name string) cmdmanv1pb.ReportedStatus {
	switch name {
	case ReportedStatusWorking:
		return cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WORKING
	case ReportedStatusWaiting:
		return cmdmanv1pb.ReportedStatus_REPORTED_STATUS_WAITING
	case ReportedStatusDone:
		return cmdmanv1pb.ReportedStatus_REPORTED_STATUS_DONE
	default:
		return cmdmanv1pb.ReportedStatus_REPORTED_STATUS_UNSPECIFIED
	}
}

// SetReportedStatus replaces the status the command reports about itself.
// status is parsed with [ParseReportedStatus]; detail replaces the previous
// detail, so an empty one clears it.
//
// The status lives in the monitor of the current run and dies with it, so a
// command that is not running is an error rather than a silent no-op: a report
// nobody can hold is a report that was lost.
func (s *Service) SetReportedStatus(ctx context.Context, idOrName, status, detail string) error {
	name, err := ParseReportedStatus(status)
	if err != nil {
		return err
	}

	conn, err := s.dialMonitorForStatus(ctx, idOrName)
	if err != nil {
		return err
	}
	if conn == nil {
		return errNoRunningMonitor(idOrName)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, statusCallTimeout)
	defer cancel()
	_, err = cmdmanv1pb.NewCommandMonitorServiceClient(conn).SetReportedStatus(
		ctx,
		&cmdmanv1pb.SetReportedStatusRequest{Status: reportedStatusProto(name), Detail: detail},
	)
	if err != nil {
		return statusRPCError("set reported status", idOrName, err)
	}
	return nil
}

// GetReportedStatus returns the status the command last reported about itself.
//
// Only an unresolvable command is an error. A command with no live monitor -
// never started, exited, or one that died between the store read and the dial -
// reports the zero value: nothing to say is a valid answer for a read.
func (s *Service) GetReportedStatus(
	ctx context.Context,
	idOrName string,
) (ReportedStatusInfo, error) {
	conn, err := s.dialMonitorForStatus(ctx, idOrName)
	if err != nil {
		return ReportedStatusInfo{}, err
	}
	if conn == nil {
		return ReportedStatusInfo{}, nil
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, statusCallTimeout)
	defer cancel()
	resp, err := cmdmanv1pb.NewCommandMonitorServiceClient(conn).GetReportedStatus(
		ctx,
		&cmdmanv1pb.GetReportedStatusRequest{},
	)
	if err != nil {
		return ReportedStatusInfo{}, nil
	}
	return ReportedStatusInfo{
		Status: reportedStatusName(resp.Status),
		Detail: resp.Detail,
	}, nil
}

// DeleteReportedStatus clears the status the command reported about itself.
// Like [Service.SetReportedStatus] it needs a running monitor to talk to.
func (s *Service) DeleteReportedStatus(ctx context.Context, idOrName string) error {
	conn, err := s.dialMonitorForStatus(ctx, idOrName)
	if err != nil {
		return err
	}
	if conn == nil {
		return errNoRunningMonitor(idOrName)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, statusCallTimeout)
	defer cancel()
	_, err = cmdmanv1pb.NewCommandMonitorServiceClient(conn).DeleteReportedStatus(
		ctx,
		&cmdmanv1pb.DeleteReportedStatusRequest{},
	)
	if err != nil {
		return statusRPCError("delete reported status", idOrName, err)
	}
	return nil
}

// statusRPCError names what a failed status call means to the user. An
// unreachable monitor is a monitor that is gone - the state JSON keeps naming
// its socket after the run ended - which is the same "nothing can hold a
// status" answer as a command that never started.
func statusRPCError(op, idOrName string, err error) error {
	if grpcstatus.Code(err) == codes.Unavailable {
		return errNoRunningMonitor(idOrName)
	}
	return fmt.Errorf("%s of %q: %w", op, idOrName, err)
}

// dialMonitorForStatus resolves idOrName and connects to its monitor. A nil
// conn without an error means the command has no monitor to hold a status;
// which of the two answers that deserves - an error or an empty read - is the
// caller's call.
func (s *Service) dialMonitorForStatus(
	ctx context.Context,
	idOrName string,
) (*grpc.ClientConn, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	id, _, _, err := st.GetCommandConfig(idOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	_, _, stateJSON, err := st.GetCommandState(id)
	if err != nil {
		return nil, fmt.Errorf("get state: %w", err)
	}
	conn, err := s.connectMonitor(ctx, stateJSON)
	if err != nil {
		return nil, nil
	}
	return conn, nil
}

func errNoRunningMonitor(idOrName string) error {
	return fmt.Errorf(
		"command %q has no running monitor: reported status lives only for the current run",
		idOrName,
	)
}
