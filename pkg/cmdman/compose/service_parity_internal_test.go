package compose

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ngicks/go-common/contextkey"

	"github.com/ngicks/cmdman/pkg/cmdman"
	"github.com/ngicks/cmdman/pkg/cmdman/store"
)

// warnLogger returns a logger that captures records into buf and a context with
// it injected, so service-layer warnings can be asserted.
func warnLogger() (*bytes.Buffer, context.Context) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return buf, contextkey.WithSlogLogger(context.Background(), logger)
}

func TestSendKeysBroadcastsToProjectCommands(t *testing.T) {
	var (
		mu  sync.Mutex
		got = map[string]cmdman.SendKeysRequest{}
	)
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return []store.CommandEntry{
				buildTestEntry("id-alpha", "alpha"),
				buildTestEntry("id-beta", "beta"),
			}, nil
		},
		sendKeys: func(_ context.Context, id string, req cmdman.SendKeysRequest) error {
			mu.Lock()
			got[id] = req
			mu.Unlock()
			return nil
		},
	}}

	res, err := svc.SendKeys(context.Background(), ProjectSelection{WorkDir: "/wd"}, SendKeysOption{
		Keys: []string{"Enter"},
	})
	if err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}
	if len(res.Outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %#v", res.Outcomes)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, id := range []string{"id-alpha", "id-beta"} {
		req, ok := got[id]
		if !ok {
			t.Fatalf("expected send-keys delivered to %q; got %#v", id, got)
		}
		if !slices.Equal(req.Keys, []string{"Enter"}) {
			t.Fatalf("unexpected keys for %q: %#v", id, req.Keys)
		}
	}
}

func TestSendKeysEmptyProjectWarns(t *testing.T) {
	buf, ctx := warnLogger()
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return nil, nil
		},
	}}

	res, err := svc.SendKeys(ctx, ProjectSelection{WorkDir: "/wd", Project: "p"}, SendKeysOption{
		Keys: []string{"Enter"},
	})
	if err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}
	if len(res.Outcomes) != 0 {
		t.Fatalf("expected no outcomes for empty project, got %#v", res.Outcomes)
	}
	if !strings.Contains(buf.String(), "no commands found") {
		t.Fatalf("expected empty-project warning; got log:\n%s", buf.String())
	}
}

func TestSendKeysRequiresKeys(t *testing.T) {
	svc := &Service{svc: testCmdmanSvc{}}
	if _, err := svc.SendKeys(
		context.Background(),
		ProjectSelection{WorkDir: "/wd"},
		SendKeysOption{},
	); err == nil {
		t.Fatal("expected error when no keys are supplied")
	}
}

func TestEventsSetsProjectIDFilter(t *testing.T) {
	var gotReq cmdman.EventsRequest
	sub := &cmdman.EventsSubscription{}
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return []store.CommandEntry{
				buildTestEntry("id-alpha", "alpha"),
				buildTestEntry("id-beta", "beta"),
			}, nil
		},
		events: func(_ context.Context, req cmdman.EventsRequest) (*cmdman.EventsSubscription, error) {
			gotReq = req
			return sub, nil
		},
	}}

	out, err := svc.Events(context.Background(), ProjectSelection{WorkDir: "/wd"}, EventsOption{
		NoFollow: true,
	})
	if err != nil {
		t.Fatalf("Events failed: %v", err)
	}
	if out != sub {
		t.Fatal("expected the underlying subscription to be passed through")
	}
	if !gotReq.NoFollow {
		t.Fatal("expected NoFollow to be passed through")
	}
	if !slices.Equal(gotReq.IDFilter, []string{"id-alpha", "id-beta"}) {
		t.Fatalf("expected IDFilter from project entries, got %v", gotReq.IDFilter)
	}
}

func TestEventsEmptyProjectReturnsNilAndWarns(t *testing.T) {
	buf, ctx := warnLogger()
	called := false
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return nil, nil
		},
		events: func(context.Context, cmdman.EventsRequest) (*cmdman.EventsSubscription, error) {
			called = true
			return &cmdman.EventsSubscription{}, nil
		},
	}}

	sub, err := svc.Events(ctx, ProjectSelection{WorkDir: "/wd", Project: "p"}, EventsOption{})
	if err != nil {
		t.Fatalf("Events failed: %v", err)
	}
	if sub != nil {
		t.Fatal("expected nil subscription for empty project")
	}
	if called {
		t.Fatal("underlying svc.Events must not be called for an empty project")
	}
	if !strings.Contains(buf.String(), "no commands found") {
		t.Fatalf("expected empty-project warning; got log:\n%s", buf.String())
	}
}

func TestInspectReturnsProjectOutputsSorted(t *testing.T) {
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			// Intentionally out of name order to exercise the sort.
			return []store.CommandEntry{
				buildTestEntry("id-beta", "beta"),
				buildTestEntry("id-alpha", "alpha"),
			}, nil
		},
		inspect: func(_ context.Context, id string) (*cmdman.InspectOutput, error) {
			return &cmdman.InspectOutput{ID: id}, nil
		},
	}}

	out, err := svc.Inspect(context.Background(), ProjectSelection{WorkDir: "/wd"}, nil)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(out))
	}
	if out[0].ID != "id-alpha" || out[1].ID != "id-beta" {
		t.Fatalf("expected outputs sorted by command name; got %q, %q", out[0].ID, out[1].ID)
	}
}

func TestResolveCommandID(t *testing.T) {
	svc := &Service{svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return []store.CommandEntry{buildTestEntry("id-alpha", "alpha")}, nil
		},
	}}

	id, err := svc.ResolveCommandID(
		context.Background(), ProjectSelection{WorkDir: "/wd"}, "alpha", 0)
	if err != nil {
		t.Fatalf("resolveCommandID(alpha) failed: %v", err)
	}
	if id != "id-alpha" {
		t.Fatalf("expected id-alpha, got %q", id)
	}

	if _, err := svc.ResolveCommandID(
		context.Background(), ProjectSelection{WorkDir: "/wd"}, "ghost", 0); err == nil {
		t.Fatal("expected error for an unknown command name")
	}
}
