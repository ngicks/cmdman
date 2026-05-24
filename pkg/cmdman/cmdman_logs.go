package cmdman

import (
	"context"
	"fmt"
	"io"
	"maps"
	"sync"
	"time"

	pb "github.com/ngicks/cmdman/pkg/api/gen/proto/go/cmdman/v1"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver"
	"github.com/ngicks/cmdman/pkg/cmdman/logdriver/k8sfile"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// LogsRequest defines a log read operation.
type LogsRequest struct {
	IDOrName string
	Follow   bool
	Since    time.Time
	Until    time.Time
	Head     int
	Tail     int
}

// Logs opens a structured reader for command output. It replays persisted
// storage first; with Follow=true it then subscribes to the running monitor
// and bridges the storage/subscription race with a bounded reread.
func (s *Service) Logs(ctx context.Context, req LogsRequest) (logdriver.Reader, error) {
	st, err := s.openStore(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	id, _, cfg, err := st.GetCommandConfig(req.IDOrName)
	if err != nil {
		return nil, fmt.Errorf("resolve command: %w", err)
	}
	opts := maps.Clone(cfg.LogOpts)
	storageReader, err := logdriver.NewReader(
		ctx,
		string(cfg.LogDriver),
		cfg.CommandDir,
		opts,
		logdriver.ReaderOption{
			Since: req.Since,
			Until: req.Until,
			Head:  req.Head,
			Tail:  req.Tail,
		},
	)
	if err != nil {
		return nil, err
	}

	// When following, capture where stored logs currently end. If the storage
	// replay yields nothing (e.g. Since=now filters out every stored record),
	// the subscription bridge resumes from here instead of byte zero, so it
	// does not re-emit already-stored history.
	var followStart any
	if req.Follow && cfg.LogDriver == store.LogDriverK8sFile {
		end, err := k8sfile.CurrentEnd(cfg.CommandDir, opts)
		if err != nil {
			_ = storageReader.Close()
			return nil, fmt.Errorf("capture follow start offset: %w", err)
		}
		followStart = end
	}

	readerCtx, cancel := context.WithCancel(ctx)
	r := &serviceLogsReader{
		cancel: cancel,
		rec:    make(chan logdriver.Record),
	}
	r.wg.Go(func() {
		defer close(r.rec)
		defer storageReader.Close()
		s.streamLogs(readerCtx, st, id, cfg, opts, req.Follow, storageReader, followStart, r.rec)
	})
	return r, nil
}

type serviceLogsReader struct {
	cancel context.CancelFunc
	rec    chan logdriver.Record
	wg     sync.WaitGroup
}

func (r *serviceLogsReader) Records() <-chan logdriver.Record {
	return r.rec
}

func (r *serviceLogsReader) Close() error {
	r.cancel()
	r.wg.Wait()
	return nil
}

func (s *Service) streamLogs(
	ctx context.Context,
	st *store.Store,
	id string,
	cfg *store.CommandConfigJSON,
	opts map[string]string,
	follow bool,
	storageReader logdriver.Reader,
	followStart any,
	out chan<- logdriver.Record,
) {
	lastOffset, ok := forwardRecords(ctx, storageReader, out)
	if !ok || !follow {
		return
	}
	// With no replayed record to anchor on, fall back to the captured end of
	// stored logs so the bridge does not rewind to byte zero.
	if lastOffset == nil {
		lastOffset = followStart
	}

	state, _, _, err := st.GetCommandState(id)
	if err != nil {
		sendRecordErr(ctx, out, fmt.Errorf("get command state: %w", err))
		return
	}
	if state != store.StateStarting && state != store.StateRunning {
		return
	}

	conn, err := s.connectMonitorByName(ctx, id)
	if err != nil {
		sendRecordErr(ctx, out, err)
		return
	}
	defer conn.Close()

	client := pb.NewCommandMonitorServiceClient(conn)
	sub, err := client.Subscribe(ctx, &pb.SubscribeRequest{})
	if err != nil {
		sendRecordErr(ctx, out, fmt.Errorf("subscribe logs: %w", err))
		return
	}

	first, err := sub.Recv()
	if err != nil {
		if err != io.EOF {
			sendRecordErr(ctx, out, fmt.Errorf("subscribe logs: %w", err))
		}
		return
	}
	captured := first.GetOffset()
	if captured == nil {
		sendRecordErr(ctx, out, fmt.Errorf("subscribe logs: missing initial offset"))
		return
	}
	if err := s.bridgeReread(ctx, cfg, opts, lastOffset, captured, out); err != nil {
		sendRecordErr(ctx, out, err)
		return
	}
	for {
		msg, err := sub.Recv()
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				sendRecordErr(ctx, out, fmt.Errorf("subscribe logs: %w", err))
			}
			return
		}
		line := msg.GetLine()
		if line == nil {
			continue
		}
		if !sendRecord(ctx, out, logdriver.Record{Line: protoToLogLine(line)}) {
			return
		}
	}
}

func (s *Service) bridgeReread(
	ctx context.Context,
	cfg *store.CommandConfigJSON,
	opts map[string]string,
	lastOffset any,
	captured *pb.SubscribeOffset,
	out chan<- logdriver.Record,
) error {
	if captured.GetDriver() != string(store.LogDriverK8sFile) {
		return nil
	}
	if len(captured.GetOffset()) == 0 {
		return nil
	}
	var from k8sfile.Offset
	if lastOffset != nil {
		offset, ok := lastOffset.(k8sfile.Offset)
		if !ok {
			return fmt.Errorf("bridge reread: unexpected offset type %T", lastOffset)
		}
		from = offset
	}
	var to k8sfile.Offset
	if err := to.UnmarshalBinary(captured.GetOffset()); err != nil {
		return fmt.Errorf("bridge reread: decode offset: %w", err)
	}
	r, err := k8sfile.NewRangeReader(ctx, cfg.CommandDir, opts, from, to)
	if err != nil {
		return err
	}
	defer r.Close()
	_, ok := forwardRecords(ctx, r, out)
	if !ok {
		return ctx.Err()
	}
	return nil
}

func forwardRecords(
	ctx context.Context,
	r logdriver.Reader,
	out chan<- logdriver.Record,
) (any, bool) {
	var lastOffset any
	for rec := range r.Records() {
		if rec.Err != nil {
			return lastOffset, sendRecord(ctx, out, rec)
		}
		if rec.Offset != nil {
			lastOffset = rec.Offset
		}
		if !sendRecord(ctx, out, rec) {
			return lastOffset, false
		}
	}
	return lastOffset, true
}

func sendRecordErr(ctx context.Context, out chan<- logdriver.Record, err error) bool {
	return sendRecord(ctx, out, logdriver.Record{Err: err})
}

func sendRecord(ctx context.Context, out chan<- logdriver.Record, rec logdriver.Record) bool {
	select {
	case <-ctx.Done():
		return false
	case out <- rec:
		return true
	}
}

func protoToLogLine(line *pb.LogLine) logdriver.LogLine {
	var ts time.Time
	if line.GetTime() != nil {
		ts = line.GetTime().AsTime()
	}
	return logdriver.LogLine{
		Time:    ts,
		Stream:  logStreamFromProto(line.GetStream()),
		Partial: line.GetPartial(),
		Line:    line.GetLine(),
	}
}

func logStreamFromProto(s pb.LogStream) logdriver.Stream {
	switch s {
	case pb.LogStream_LOG_STREAM_STDERR:
		return logdriver.StreamStderr
	case pb.LogStream_LOG_STREAM_STDOUT, pb.LogStream_LOG_STREAM_UNSPECIFIED:
		return logdriver.StreamStdout
	default:
		return logdriver.StreamStdout
	}
}
