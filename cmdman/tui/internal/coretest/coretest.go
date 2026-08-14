// Package coretest holds the test doubles the TUI's models are exercised
// against: a scriptable [core.Backend] with the stream fakes it hands out, and
// the key messages a driven model is fed.
//
// It is a normal package rather than a _test file because every model package
// under cmdman/tui needs the same backend, and a fake that lived in one of them
// could not be reached from the others.
package coretest

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ngicks/cmdman/cmdman/tui/internal/core"
)

// FakeBackend is a scripted [core.Backend]: the listings it answers with are
// set on it, and the calls it took are recorded for the test to assert on.
type FakeBackend struct {
	Cmds  []core.CommandInfo
	Projs []core.ProjectInfo
	// Dir is what Cwd reports; the accessor owns the name Cwd.
	Dir string

	Started     []string
	Stopped     []string
	Restarted   []string
	Removed     []string
	RemoveForce map[string]bool

	LogStreams  []*FakeLogStream // one per Logs call
	EventStream *FakeEventStream
	AttachIDs   []string
	AttachOut   string
	AttachErr   error

	MuxCycled []string // project names passed to CycleMux
	MuxErr    error

	Switched  []string // identities passed to SwitchToProject
	SwitchErr error    // error returned by SwitchToProject
	Hidden    int      // HideFrame calls
	HideErr   error    // error returned by HideFrame

	LayoutsInfo    core.LayoutsInfo // info returned by ListLayouts
	LayoutsErr     error            // error returned by ListLayouts
	LayoutsReq     []string         // project names passed to ListLayouts
	AppliedLayouts []string         // layout names passed to ApplyLayout
	ApplyLayoutErr error            // error returned by ApplyLayout

	Definition     string   // text returned by ProjectDefinition
	DefinitionErr  error    // error returned by ProjectDefinition
	DefRequested   []string // project names passed to ProjectDefinition
	ComposePath    string   // path returned by ComposeFilePath
	ComposePathErr error    // error returned by ComposeFilePath
	PathRequested  []string // project names passed to ComposeFilePath

	ComposeUpCalled []string              // project names passed to ComposeUp
	ComposeUpEvents []core.ComposeUpEvent // events pre-loaded into the stream
	ComposeUpErr    error                 // error returned by ComposeUp (open failure)
	ComposeUpStream *FakeComposeUpStream

	LaunchLocs       []core.LaunchLocation // locations returned by ListLaunchTargets
	LaunchErr        error                 // error returned by ListLaunchTargets
	ResolveDirLoc    core.LaunchLocation   // location returned by ResolveLaunchDir
	ResolveDirErr    error                 // error returned by ResolveLaunchDir
	ResolveDirReq    []string              // dirs passed to ResolveLaunchDir
	StartedProjects  []core.LaunchTarget   // targets passed to StartProject
	StartProjectErr  error                 // error returned by StartProject
	LaunchedProjects []core.LaunchTarget   // targets passed to LaunchProject
	LaunchOutcome    core.LaunchOutcome    // outcome returned by LaunchProject
	LaunchProjectErr error                 // error returned by LaunchProject
	ForgotTargets    []core.LaunchTarget   // targets passed to ForgetLaunchTarget
	ForgetErr        error                 // error returned by ForgetLaunchTarget

	RawIDs     []string         // ids passed to RawView
	RawChunks  [][]byte         // chunks pre-loaded into each RawView stream
	RawErr     error            // error returned by RawView (open failure)
	RawStreams []*FakeRawStream // one per RawView call
}

var _ core.Backend = (*FakeBackend)(nil)

func (f *FakeBackend) ListCommands(context.Context) ([]core.CommandInfo, error) {
	return f.Cmds, nil
}

func (f *FakeBackend) ListProjects(context.Context) ([]core.ProjectInfo, error) {
	return f.Projs, nil
}

func (f *FakeBackend) Cwd() string { return f.Dir }

func (f *FakeBackend) Start(_ context.Context, id string) error {
	f.Started = append(f.Started, id)
	return nil
}

func (f *FakeBackend) Stop(_ context.Context, id string) error {
	f.Stopped = append(f.Stopped, id)
	return nil
}

func (f *FakeBackend) Restart(_ context.Context, id string) error {
	f.Restarted = append(f.Restarted, id)
	return nil
}

func (f *FakeBackend) Remove(_ context.Context, id string, force bool) error {
	f.Removed = append(f.Removed, id)
	if f.RemoveForce == nil {
		f.RemoveForce = map[string]bool{}
	}
	f.RemoveForce[id] = force
	return nil
}

func (f *FakeBackend) Events(context.Context) (core.EventStream, error) {
	if f.EventStream == nil {
		f.EventStream = &FakeEventStream{Ch: make(chan core.EventSignal, 1)}
	}
	return f.EventStream, nil
}

func (f *FakeBackend) Logs(_ context.Context, _ string, _ int) (core.LogStream, error) {
	ls := &FakeLogStream{Ch: make(chan core.LogLine, 16)}
	f.LogStreams = append(f.LogStreams, ls)
	return ls, nil
}

func (f *FakeBackend) Attach(_ context.Context, id string) (string, error) {
	f.AttachIDs = append(f.AttachIDs, id)
	return f.AttachOut, f.AttachErr
}

func (f *FakeBackend) CycleMux(_ context.Context, projectName, _ string) error {
	f.MuxCycled = append(f.MuxCycled, projectName)
	return f.MuxErr
}

func (f *FakeBackend) SwitchToProject(_ context.Context, identity string) error {
	f.Switched = append(f.Switched, identity)
	return f.SwitchErr
}

func (f *FakeBackend) HideFrame(context.Context) error {
	f.Hidden++
	return f.HideErr
}

func (f *FakeBackend) ListLayouts(
	_ context.Context,
	projectName, _ string,
) (core.LayoutsInfo, error) {
	f.LayoutsReq = append(f.LayoutsReq, projectName)
	return f.LayoutsInfo, f.LayoutsErr
}

func (f *FakeBackend) ApplyLayout(_ context.Context, _, _, layoutName string) error {
	f.AppliedLayouts = append(f.AppliedLayouts, layoutName)
	return f.ApplyLayoutErr
}

func (f *FakeBackend) ProjectDefinition(_ context.Context, projectName, _ string) (string, error) {
	f.DefRequested = append(f.DefRequested, projectName)
	return f.Definition, f.DefinitionErr
}

func (f *FakeBackend) ComposeFilePath(_ context.Context, projectName, _ string) (string, error) {
	f.PathRequested = append(f.PathRequested, projectName)
	return f.ComposePath, f.ComposePathErr
}

func (f *FakeBackend) ComposeUp(
	_ context.Context,
	projectName, _ string,
) (core.ComposeUpStream, error) {
	f.ComposeUpCalled = append(f.ComposeUpCalled, projectName)
	if f.ComposeUpErr != nil {
		return nil, f.ComposeUpErr
	}
	s := &FakeComposeUpStream{Ch: make(chan core.ComposeUpEvent, len(f.ComposeUpEvents))}
	for _, ev := range f.ComposeUpEvents {
		s.Ch <- ev
	}
	f.ComposeUpStream = s
	return s, nil
}

func (f *FakeBackend) ListLaunchTargets(context.Context) ([]core.LaunchLocation, error) {
	return f.LaunchLocs, f.LaunchErr
}

func (f *FakeBackend) ResolveLaunchDir(
	_ context.Context,
	dir string,
) (core.LaunchLocation, error) {
	f.ResolveDirReq = append(f.ResolveDirReq, dir)
	return f.ResolveDirLoc, f.ResolveDirErr
}

func (f *FakeBackend) StartProject(_ context.Context, t core.LaunchTarget) error {
	f.StartedProjects = append(f.StartedProjects, t)
	return f.StartProjectErr
}

func (f *FakeBackend) LaunchProject(
	_ context.Context,
	t core.LaunchTarget,
) (core.LaunchOutcome, error) {
	f.LaunchedProjects = append(f.LaunchedProjects, t)
	return f.LaunchOutcome, f.LaunchProjectErr
}

func (f *FakeBackend) ForgetLaunchTarget(_ context.Context, t core.LaunchTarget) error {
	f.ForgotTargets = append(f.ForgotTargets, t)
	return f.ForgetErr
}

func (f *FakeBackend) RawView(_ context.Context, id string) (core.RawStream, error) {
	f.RawIDs = append(f.RawIDs, id)
	if f.RawErr != nil {
		return nil, f.RawErr
	}
	s := NewFakeRawStream(len(f.RawChunks) + 1)
	for _, c := range f.RawChunks {
		s.Ch <- core.RawChunk{Bytes: c}
	}
	f.RawStreams = append(f.RawStreams, s)
	return s, nil
}

// FakeRawStream is closed off the update loop (see closeRawAsync), so its
// closed state is mutex-guarded and closedCh lets a test wait for an async
// close without racing the goroutine.
type FakeRawStream struct {
	Ch        chan core.RawChunk
	closedCh  chan struct{}
	closeOnce sync.Once

	mu     sync.Mutex
	closed bool
}

func NewFakeRawStream(buf int) *FakeRawStream {
	return &FakeRawStream{Ch: make(chan core.RawChunk, buf), closedCh: make(chan struct{})}
}

func (s *FakeRawStream) Chunks() <-chan core.RawChunk { return s.Ch }

func (s *FakeRawStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.Ch)
		close(s.closedCh)
	})
	return nil
}

// IsClosed reports the close state without racing an async Close.
func (s *FakeRawStream) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// WaitClosed blocks briefly for an async Close, failing the test if it never
// happens (closeRawAsync runs Close in a goroutine).
func (s *FakeRawStream) WaitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-s.closedCh:
	case <-time.After(time.Second):
		t.Fatalf("raw stream was not closed")
	}
}

type FakeComposeUpStream struct {
	Ch chan core.ComposeUpEvent
	// OpErr is the operation-level error; the accessor owns the name Err.
	OpErr  error
	Closed bool
}

func (s *FakeComposeUpStream) Events() <-chan core.ComposeUpEvent { return s.Ch }
func (s *FakeComposeUpStream) Err() error                         { return s.OpErr }
func (s *FakeComposeUpStream) Close() error {
	if !s.Closed {
		s.Closed = true
		close(s.Ch)
	}
	return nil
}

type FakeLogStream struct {
	Ch     chan core.LogLine
	Closed bool
}

func (s *FakeLogStream) Lines() <-chan core.LogLine { return s.Ch }
func (s *FakeLogStream) Close() error {
	if !s.Closed {
		s.Closed = true
		close(s.Ch)
	}
	return nil
}

type FakeEventStream struct {
	Ch     chan core.EventSignal
	Closed bool
}

func (s *FakeEventStream) Signals() <-chan core.EventSignal { return s.Ch }
func (s *FakeEventStream) Close() error {
	if !s.Closed {
		s.Closed = true
		close(s.Ch)
	}
	return nil
}

// Kr is a single-rune key press.
func Kr(s string) tea.KeyMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

// The named key presses a driven model is fed.
var (
	KEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	KEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	KTab   = tea.KeyPressMsg{Code: tea.KeyTab}
)

// MsgIsQuit reports whether a command's message is bubbletea's quit.
func MsgIsQuit(msg tea.Msg) bool {
	// tea.Quit returns a tea.QuitMsg.
	_, ok := msg.(tea.QuitMsg)
	return ok
}
