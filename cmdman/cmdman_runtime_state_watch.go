package cmdman

import (
	"context"
	"fmt"
	"io"

	"golang.org/x/sync/errgroup"

	cmdmanv1pb "github.com/ngicks/cmdman/api/gen/proto/go/cmdman/v1"
)

// RuntimeStateRecord is one pushed runtime-state view from a watched monitor,
// or the stream's terminal read error.
type RuntimeStateRecord struct {
	State RuntimeState
	Err   error
}

// RuntimeStateSubscription delivers runtime-state records (initial snapshot,
// then push on change) until closed. Records' channel closes when the monitor
// leaves an active state or Close is called.
type RuntimeStateSubscription struct {
	out    chan RuntimeStateRecord
	cancel context.CancelFunc
	eg     *errgroup.Group
}

// WatchRuntimeState dials the command's monitor and subscribes to its
// runtime-state stream.
func (s *Service) WatchRuntimeState(
	ctx context.Context,
	idOrName string,
) (*RuntimeStateSubscription, error) {
	conn, err := s.connectMonitorByName(ctx, idOrName)
	if err != nil {
		return nil, err
	}

	subCtx, cancel := context.WithCancel(ctx)
	stream, err := cmdmanv1pb.NewCommandMonitorServiceClient(conn).
		WatchRuntimeState(subCtx, &cmdmanv1pb.WatchRuntimeStateRequest{})
	if err != nil {
		cancel()
		conn.Close()
		return nil, fmt.Errorf("watch runtime state: %w", err)
	}

	out := make(chan RuntimeStateRecord, 16)
	eg := &errgroup.Group{}
	// The connection is released here rather than in Close so that a stream
	// ending on its own - the monitor left an active state - frees it too; a
	// ClientConn must not be closed twice.
	eg.Go(func() error {
		defer close(out)
		defer conn.Close()
		// A stream ending on its own leaves nothing for Close to do, and a
		// consumer that stops at the closed channel never calls it - so release
		// the child context here rather than leaving it on the parent, which in
		// the TUI outlives every subscription it opens.
		defer cancel()
		for {
			msg, err := stream.Recv()
			if err != nil {
				// A cancelled subscription reads as an error on the wire but is
				// how Close ends the stream, so it is not one to report.
				if err != io.EOF && subCtx.Err() == nil {
					sendRuntimeState(subCtx, out, RuntimeStateRecord{
						Err: fmt.Errorf("watch runtime state: %w", err),
					})
				}
				return nil
			}
			state := msg.GetState()
			if state == nil {
				continue
			}
			if !sendRuntimeState(
				subCtx,
				out,
				RuntimeStateRecord{State: runtimeStateFromProto(state)},
			) {
				return nil
			}
		}
	})

	return &RuntimeStateSubscription{out: out, cancel: cancel, eg: eg}, nil
}

// Records returns the runtime-state channel.
func (sub *RuntimeStateSubscription) Records() <-chan RuntimeStateRecord {
	return sub.out
}

// Close ends the stream and releases the monitor connection. It is safe to call
// while the subscription is parked waiting for the next push.
func (sub *RuntimeStateSubscription) Close() error {
	sub.cancel()
	return sub.eg.Wait()
}

func sendRuntimeState(
	ctx context.Context,
	out chan<- RuntimeStateRecord,
	rec RuntimeStateRecord,
) bool {
	select {
	case out <- rec:
		return true
	case <-ctx.Done():
		return false
	}
}
