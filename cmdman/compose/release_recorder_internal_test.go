package compose

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// unreleasedEvents returns the PhaseUnreleased events rec got, in order.
func unreleasedEvents(rec *commandPhaseReporter) []Event {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []Event
	for _, ev := range rec.events {
		if ev.Phase == PhaseUnreleased {
			out = append(out, ev)
		}
	}
	return out
}

func TestDownReportsEveryFailedRelease(t *testing.T) {
	for _, tc := range []struct {
		onError  OnError
		fails    bool
		wantHeld bool
		wantKept bool
	}{
		{onError: OnErrorFail, fails: true, wantHeld: true, wantKept: true},
		{onError: OnErrorContinue, wantHeld: true},
		{onError: OnErrorIgnore},
	} {
		t.Run(string(tc.onError), func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			s := f.service(rec)
			nc := stepCommand("web", 1, portHook(tc.onError))
			r := acquirePort(t, f, s, nc, model.EventTypeRunning)
			failExecNamed(f, ExecCommandName(r.Name, "port", LifecycleStopPost))

			res, err := s.Down(t.Context(), storedSelection(), DownOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(res.Releases), 1, "%+v", res.Releases)
			o := res.Releases[0]
			assert.Equal(t, o.Holder, HolderName(r.Name, "port"))
			assert.Equal(t, o.Resource, "port")
			assert.Equal(t, o.Value, "port-1")
			assert.Assert(t, !o.Retried)
			failure := o.Warning
			if tc.fails {
				failure = o.Err
				assert.NilError(t, o.Warning)
			} else {
				assert.NilError(t, o.Err)
			}
			assert.ErrorContains(t, failure, `hook "port" stop_post of web`)
			assert.ErrorContains(t, failure, "exited with code 1")

			events := unreleasedEvents(rec)
			assert.Equal(t, len(events), 1, "%+v", events)
			ev := events[0]
			assert.Equal(t, ev.Command, "web")
			assert.Equal(t, ev.Resource, "port")
			assert.Equal(t, ev.Value, "port-1")
			assert.Assert(t, !ev.Retried)
			assert.Equal(t, ev.Err, failure)
			assert.Assert(t, ev.Phase.Terminal())
			assert.Assert(t, !ev.Phase.Failed())

			_, held := holderValue(t, f, r, "port")
			assert.Equal(t, held, tc.wantHeld)
			_, kept := f.get(r.Name)
			assert.Equal(t, kept, tc.wantKept)
		})
	}
}

func TestRunLifecycleEventRecordsFailedRelease(t *testing.T) {
	for _, onError := range []OnError{OnErrorFail, OnErrorContinue, OnErrorIgnore} {
		t.Run(string(onError), func(t *testing.T) {
			f := newFakeCmdman()
			s := f.service(nil)
			r := testHookReplica()
			hooks := []LifecycleHook{scratchHook("", onError)}
			f.run = func(string, cmdman.CreateRequest) fakeRun {
				return fakeRun{exit: new(0), stdout: []string{"/tmp/scratch.1"}}
			}
			_, err := s.runLifecycleEvent(t.Context(), r, hooks, LifecycleCreatePre)
			assert.NilError(t, err)
			stored, ok := f.get(HolderName(r.Name, "scratch"))
			assert.Assert(t, ok)
			want, err := decodeHolder(stored)
			assert.NilError(t, err)
			f.run = func(string, cmdman.CreateRequest) fakeRun { return fakeRun{exit: new(1)} }
			rec := &releaseRecorder{}

			_, _ = s.runLifecycleEvent(
				withReleaseRecorder(t.Context(), rec), r, hooks, LifecycleRemovePost)

			failed := rec.failures()
			assert.Equal(t, len(failed), 1)
			assert.DeepEqual(t, failed[0].holder, want)
			assert.Equal(t, failed[0].holder.Value, "/tmp/scratch.1")
			assert.DeepEqual(t, failed[0].holder.Release, &resourceRelease{
				Event:   LifecycleRemovePost,
				Args:    []string{"rm", "-rf"},
				OnError: onError,
			})
			assert.Equal(t, failed[0].display, "web")
			assert.Equal(t, failed[0].onError, onError)
			assert.Equal(t, failed[0].fails(), onError == OnErrorFail)
			assert.ErrorContains(t, failed[0].err, `value "/tmp/scratch.1"`)
			_, held := holderValue(t, f, r, "scratch")
			assert.Equal(t, held, onError != OnErrorIgnore,
				"ignore drops the holder, and the record still has its copy")
		})
	}
}

func TestRunLifecycleEventRecordsReleaseWithoutHolder(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun { return fakeRun{exit: new(1)} }
	s := f.service(nil)
	r := testHookReplica()
	hook := scratchHook("", OnErrorContinue)
	rec := &releaseRecorder{}

	_, err := s.runLifecycleEvent(
		withReleaseRecorder(t.Context(), rec), r, []LifecycleHook{hook}, LifecycleRemovePost)

	assert.NilError(t, err)
	failed := rec.failures()
	assert.Equal(t, len(failed), 1)
	h := failed[0].holder
	assert.Equal(t, h.name(), HolderName(r.Name, "scratch"))
	assert.Equal(t, h.Ref, r.resourceRef("scratch"))
	assert.Equal(t, h.Value, "")
	assert.DeepEqual(t, h.Release, &resourceRelease{
		Event:   LifecycleRemovePost,
		Args:    []string{"rm", "-rf"},
		OnError: OnErrorContinue,
	})
	assert.Equal(t, h.Dir, r.Dir)
	assert.DeepEqual(t, h.Env, slices.Concat(r.Env, s.hookEnv(r, hook, LifecycleRemovePost)))
}

func TestRunLifecycleEventRecordsCancelledReleaseAsFailing(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun { return fakeRun{block: true} }
	ctx, cancel := context.WithCancel(t.Context())
	f.started = func(string) { cancel() }
	s := f.service(nil)
	r := testHookReplica()
	putTestHolder(f, r, "scratch", "/tmp/old")
	rec := &releaseRecorder{}

	_, err := s.runLifecycleEvent(withReleaseRecorder(ctx, rec), r,
		[]LifecycleHook{scratchHook("", OnErrorIgnore)}, LifecycleRemovePost)

	assert.Assert(t, errors.Is(err, context.Canceled), "got %v", err)
	failed := rec.failures()
	assert.Equal(t, len(failed), 1)
	assert.Equal(t, failed[0].onError, OnErrorFail)
	assert.Equal(t, failed[0].holder.Value, "/tmp/old")
	_, held := holderValue(t, f, r, "scratch")
	assert.Assert(t, held, "a cancelled release keeps the holder")
}

func TestOnlyDownReportsUnreleasedResources(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, s *Service, nc Command)
	}{
		{
			name: "stop",
			run: func(t *testing.T, s *Service, _ Command) {
				_, err := s.Stop(t.Context(), storedSelection(), StopOption{})
				assert.NilError(t, err)
			},
		},
		{
			name: "restart",
			run: func(t *testing.T, s *Service, _ Command) {
				_, err := s.Restart(t.Context(), storedSelection(), RestartOption{})
				assert.NilError(t, err)
			},
		},
		{
			name: "recreate",
			run: func(t *testing.T, s *Service, nc Command) {
				changed := nc
				changed.Args = []string{"sleep", "600"}
				_, err := s.Create(t.Context(), stepSpec(changed), CreateOption{})
				assert.NilError(t, err)
			},
		},
		{
			name: "scale-down",
			run: func(t *testing.T, s *Service, nc Command) {
				scaled := nc
				scaled.Scale = 1
				_, err := s.Create(t.Context(), stepSpec(scaled), CreateOption{})
				assert.NilError(t, err)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			s := f.service(rec)
			nc := stepCommand("web", 2, portHook(OnErrorContinue))
			acquireReplicaPort(t, f, s, nc, 1, model.EventTypeRunning)
			acquireReplicaPort(t, f, s, nc, 2, model.EventTypeRunning)
			f.run = func(name string, _ cmdman.CreateRequest) fakeRun {
				switch {
				case strings.HasSuffix(name, ".port.stop_post"):
					return fakeRun{exit: new(1)}
				case strings.HasSuffix(name, ".port.start_pre"):
					return fakeRun{exit: new(0), stdout: []string{"port-new"}}
				}
				return fakeRun{exit: new(0)}
			}

			tc.run(t, s, nc)

			assert.Assert(t, rec.hookPhaseSeen(PhaseHookWarning), "a release failed")
			assert.Equal(t, len(unreleasedEvents(rec)), 0)
		})
	}
}

func TestAddUnreleasedListsEachResourceOnce(t *testing.T) {
	rec := &commandPhaseReporter{}
	s := &Service{reporter: rec}
	holder := func(key, value string) resourceHolder {
		r := testHookReplica()
		return resourceHolder{Ref: r.resourceRef(key), Owner: r.Name, Value: value}
	}
	stranded, kept, dropped := holder("a", "va"), holder("b", "vb"), holder("c", "vc")
	listErr := errors.New("list failed")
	strandedErr := errors.New("stranded failed")
	keptErr := errors.New("kept failed")
	droppedErr := errors.New("dropped failed")
	outcomes := []ReleaseOutcome{
		{Err: listErr},
		{Holder: stranded.name(), Resource: "a", Value: "va", Err: strandedErr},
	}
	failed := []failedRelease{
		{holder: stranded, display: "web", err: strandedErr, onError: OnErrorFail},
		{holder: kept, display: "web", err: keptErr, onError: OnErrorContinue},
		{holder: kept, display: "web", err: keptErr, onError: OnErrorContinue},
		{holder: dropped, display: "web", err: droppedErr, onError: OnErrorIgnore},
	}

	got := s.addUnreleased(outcomes, failed)

	assert.Equal(t, len(got), 4, "%+v", got)
	assert.Equal(t, got[0].Holder, "")
	assert.Equal(t, got[0].Err, listErr)
	for i, want := range []struct {
		holder resourceHolder
		err    error
		warn   error
	}{
		{holder: stranded, err: strandedErr},
		{holder: kept, warn: keptErr},
		{holder: dropped, warn: droppedErr},
	} {
		o := got[i+1]
		assert.Equal(t, o.Holder, want.holder.name())
		assert.Equal(t, o.Resource, want.holder.Ref.Key)
		assert.Equal(t, o.Value, want.holder.Value)
		assert.Equal(t, o.Err, want.err)
		assert.Equal(t, o.Warning, want.warn)
	}
	events := unreleasedEvents(rec)
	assert.Equal(t, len(events), 3, "one event per resource: %+v", events)
	for i, key := range []string{"a", "b", "c"} {
		assert.Equal(t, events[i].Resource, key)
		assert.Equal(t, events[i].Command, "web")
	}
}

func TestReleaseRecorderIsSafeForConcurrentUse(t *testing.T) {
	rec := &releaseRecorder{}
	ctx := withReleaseRecorder(t.Context(), rec)
	var eg errgroup.Group
	for i := range 20 {
		eg.Go(func() error {
			releaseRecorderFrom(ctx).record(failedRelease{holder: resourceHolder{
				Ref:   resourceRef{Key: "k"},
				Owner: fmt.Sprintf("r%02d", 19-i),
			}})
			return nil
		})
	}
	assert.NilError(t, eg.Wait())

	failed := rec.failures()
	assert.Equal(t, len(failed), 20)
	assert.Assert(t, slices.IsSortedFunc(failed, func(a, b failedRelease) int {
		return strings.Compare(a.holder.name(), b.holder.name())
	}))

	assert.Assert(t, releaseRecorderFrom(t.Context()) == nil)
	var none *releaseRecorder
	none.record(failedRelease{})
	assert.Equal(t, len(none.failures()), 0)
}
