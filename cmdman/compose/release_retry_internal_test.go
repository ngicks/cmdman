package compose

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/monitor"
)

// monitorDiedError is the error stale cleanup records for a command whose
// monitor died.
const monitorDiedError = "monitor died unexpectedly"

// failOnceThenSucceed makes the exec command named release exit 1 on its first
// run and 0 on every later one, and every other exec command exit 0. values
// gets the resource value of each run of release.
func failOnceThenSucceed(f *fakeCmdman, release string, values *[]string) {
	f.run = func(name string, req cmdman.CreateRequest) fakeRun {
		if name != release {
			return fakeRun{exit: new(0)}
		}
		v, _ := envValue(req.Env, ENV_CMDMAN_COMPOSE_RESOURCE_VALUE)
		*values = append(*values, v)
		if len(*values) == 1 {
			return fakeRun{exit: new(1)}
		}
		return fakeRun{exit: new(0)}
	}
}

// lastHookPhase returns the phase of the last hook event recorded for hook.
func lastHookPhase(rec *commandPhaseReporter, hook string) Phase {
	phases := hookPhases(rec, hook)
	if len(phases) == 0 {
		return ""
	}
	return phases[len(phases)-1]
}

func TestDownRetriesReleaseOfStoppedReplica(t *testing.T) {
	for _, onError := range []OnError{OnErrorContinue, OnErrorIgnore} {
		t.Run(string(onError), func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			s := f.service(rec)
			nc := stepCommand("web", 1, portHook(onError))
			r := acquirePort(t, f, s, nc, model.EventTypeRunning)
			release := ExecCommandName(r.Name, "port", LifecycleStopPost)
			var values []string
			failOnceThenSucceed(f, release, &values)

			res, err := s.Down(t.Context(), storedSelection(), DownOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(res.Releases), 0, "%+v", res.Releases)
			assert.Equal(t, len(unreleasedEvents(rec)), 0)
			assert.NilError(t, removeOutcomes(res)["web"])
			assert.DeepEqual(t, values, []string{"port-1", "port-1"})
			assert.Equal(t, execCreated(f, release), 2)
			assert.Equal(t, lastHookPhase(rec, "port"), PhaseHookSucceeded)
			assert.DeepEqual(t, lifecycleTrace(f, r.Name), []string{
				"port.start_pre", "stop", "port.stop_post", "port.stop_post", "remove",
			})
			_, held := holderValue(t, f, r, "port")
			assert.Assert(t, !held)
			all, err := f.list(t.Context(), cmdman.ListRequest{})
			assert.NilError(t, err)
			assert.Equal(t, len(all), 0, "nothing of the project is left: %v", all)
		})
	}
}

// withoutResourceEnv returns env less the resource key and value, which a
// release run from a holder sets once more.
func withoutResourceEnv(env []string) []string {
	return slices.DeleteFunc(slices.Clone(env), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return k == ENV_CMDMAN_COMPOSE_RESOURCE_KEY || k == ENV_CMDMAN_COMPOSE_RESOURCE_VALUE
	})
}

func TestDownRetriesReleaseOfHolderWithoutStoredRelease(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	// Neither the hook name nor the directory of the replica can be told from
	// the holder: the hook is named apart from its resource, and the replica
	// runs in a directory of its own.
	hook := portHook(OnErrorContinue)
	hook.Name = "net"
	nc := stepCommand("web", 1, hook)
	nc.Dir = "/wd/web"
	spec := stepSpec(nc)
	putReplica(t, f, spec, nc, 1, model.EventTypeRunning)
	r := s.specHookReplica(spec, nc, 1)
	err := s.ResourceSet(t.Context(), storedSelection(),
		ResourceOption{Command: "web", Key: "port"}, "port-set")
	assert.NilError(t, err)
	stored, ok := f.get(HolderName(r.Name, "port"))
	assert.Assert(t, ok)
	assert.Equal(t, stored.ConfigJSON.Dir, "/wd")
	release := ExecCommandName(r.Name, "net", LifecycleStopPost)
	var runs []cmdman.CreateRequest
	f.run = func(name string, req cmdman.CreateRequest) fakeRun {
		if name != release {
			return fakeRun{exit: new(0)}
		}
		runs = append(runs, req)
		if len(runs) == 1 {
			return fakeRun{exit: new(1)}
		}
		return fakeRun{exit: new(0)}
	}

	res, err := s.Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.Equal(t, len(res.Releases), 0, "%+v", res.Releases)
	assert.Equal(
		t,
		execCreated(f, release),
		2,
		"the retry replaces the exec command of the first run",
	)
	assert.Equal(t, len(runs), 2)
	first, retry := runs[0], runs[1]
	assert.DeepEqual(t, first.Argv, []string{"free"})
	assert.DeepEqual(t, retry.Argv, first.Argv)
	assert.Equal(t, first.Dir, "/wd/web")
	assert.Equal(t, retry.Dir, first.Dir)
	assert.DeepEqual(t, retry.Labels, first.Labels)
	for key, want := range map[string]string{
		ENV_CMDMAN_COMPOSE_HOOK_NAME:      "net",
		ENV_CMDMAN_COMPOSE_HOOK_EVENT:     string(LifecycleStopPost),
		cmdman.ENV_CMDMAN_DATA_DIR:        "/data",
		cmdman.ENV_CMDMAN_RUNTIME_DIR:     "/run",
		ENV_CMDMAN_COMPOSE_RESOURCE_KEY:   "port",
		ENV_CMDMAN_COMPOSE_RESOURCE_VALUE: "port-set",
	} {
		for i, req := range runs {
			got, ok := envValue(req.Env, key)
			assert.Assert(t, ok, "run %d sets %s", i+1, key)
			assert.Equal(t, got, want, "run %d sets %s", i+1, key)
		}
	}
	assert.DeepEqual(t, withoutResourceEnv(retry.Env), withoutResourceEnv(first.Env))
	_, held := holderValue(t, f, r, "port")
	assert.Assert(t, !held)
}

// failFirstHolderLookup fails the first lookup of a holder by its resource key,
// as a busy store would, and lets every later one through.
func failFirstHolderLookup(f *fakeCmdman) {
	failed := false
	f.listErr = func(req cmdman.ListRequest) error {
		if failed || req.Labels[LabelResourceKey] == "" {
			return nil
		}
		failed = true
		return errors.New("database is locked")
	}
}

func TestDownDoesNotRetryReleaseWhoseHolderLookupFailed(t *testing.T) {
	for _, onError := range []OnError{OnErrorFail, OnErrorContinue} {
		t.Run(string(onError), func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			s := f.service(rec)
			nc := stepCommand("web", 1, portHook(onError))
			r := acquirePort(t, f, s, nc, model.EventTypeRunning)
			release := ExecCommandName(r.Name, "port", LifecycleStopPost)
			f.run = nil
			failFirstHolderLookup(f)

			res, err := s.Down(t.Context(), storedSelection(), DownOption{})

			assert.NilError(t, err)
			assert.Equal(t, execCreated(f, release), 0,
				"the release never ran, and no retry runs it without its value")
			assert.Equal(t, len(res.Releases), 1, "%+v", res.Releases)
			o := res.Releases[0]
			assert.Equal(t, o.Holder, HolderName(r.Name, "port"))
			assert.Equal(t, o.Command, "web")
			assert.Equal(t, o.Resource, "port")
			assert.Equal(t, o.Value, "")
			assert.Assert(t, !o.Retried)
			failure := o.Warning
			if onError == OnErrorFail {
				failure = o.Err
			}
			assert.ErrorContains(t, failure, "look up holder of resource")
			assert.ErrorContains(t, failure, "database is locked")
			assert.Equal(t, len(unreleasedEvents(rec)), 1)
			value, held := holderValue(t, f, r, "port")
			assert.Assert(t, held, "the stored holder survives")
			assert.Equal(t, value, "port-1")
		})
	}
}

// setLabel sets label key of the command named name to value.
func setLabel(f *fakeCmdman, name, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookup(name).entry.ConfigJSON.Labels[key] = value
}

func TestDownDoesNotRetryReleaseOfUndecodableHolder(t *testing.T) {
	f := newFakeCmdman()
	rec := &commandPhaseReporter{}
	s := f.service(rec)
	nc := stepCommand("web", 1, portHook(OnErrorContinue))
	r := acquirePort(t, f, s, nc, model.EventTypeRunning)
	// The labels that find the holder and its value stay readable.
	setLabel(f, HolderName(r.Name, "port"), LabelResourceRelease, "{")
	release := ExecCommandName(r.Name, "port", LifecycleStopPost)
	var values []string
	failOnceThenSucceed(f, release, &values)

	res, err := s.Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.DeepEqual(t, values, []string{"port-1"})
	assert.Equal(t, len(res.Releases), 1, "%+v", res.Releases)
	o := res.Releases[0]
	assert.Equal(t, o.Holder, HolderName(r.Name, "port"))
	assert.Equal(t, o.Value, "port-1")
	assert.Assert(t, !o.Retried)
	assert.NilError(t, o.Err)
	assert.ErrorContains(t, o.Warning, "exited with code 1")
	events := unreleasedEvents(rec)
	assert.Equal(t, len(events), 1, "%+v", events)
	assert.Equal(t, events[0].Value, "port-1")
	value, held := holderValue(t, f, r, "port")
	assert.Assert(t, held, "the stored holder survives")
	assert.Equal(t, value, "port-1")
}

// reporterFunc reports each event to the func it is.
type reporterFunc func(Event)

func (f reporterFunc) Report(ev Event) { f(ev) }

func TestDownRetriesOnceEveryStopHasReturned(t *testing.T) {
	f := newFakeCmdman()
	rec := &commandPhaseReporter{}
	firstStopped := make(chan struct{})
	var once sync.Once
	s := f.service(reporterFunc(func(ev Event) {
		rec.Report(ev)
		if ev.Command == "web-1" && ev.Phase == PhaseStopped {
			once.Do(func() { close(firstStopped) })
		}
	}))
	nc := stepCommand("web", 2, portHook(OnErrorContinue))
	r1 := acquireReplicaPort(t, f, s, nc, 1, model.EventTypeRunning)
	r2 := acquireReplicaPort(t, f, s, nc, 2, model.EventTypeRunning)
	release1 := ExecCommandName(r1.Name, "port", LifecycleStopPost)
	release2 := ExecCommandName(r2.Name, "port", LifecycleStopPost)
	// Replica 2 shares the resource of replica 1, as two containers attached to
	// one network do, so its release works only once replica 2 has stopped.
	var released []bool
	f.run = func(name string, _ cmdman.CreateRequest) fakeRun {
		if name != release1 {
			return fakeRun{exit: new(0)}
		}
		ok := rec.reached("web-2", PhaseStopped)
		released = append(released, ok)
		if !ok {
			return fakeRun{exit: new(1)}
		}
		return fakeRun{exit: new(0)}
	}
	// Replica 2 is still in its stop_post when the stop of replica 1 returns.
	f.started = func(name string) {
		if name != release2 {
			return
		}
		select {
		case <-firstStopped:
		case <-time.After(10 * time.Second):
			t.Error("the stop of replica 1 never returned")
		}
	}

	res, err := s.Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.DeepEqual(t, released, []bool{false, true})
	assert.Equal(t, len(res.Releases), 0, "%+v", res.Releases)
	calls := f.callLog()
	var runs []int
	for i, call := range calls {
		if call == "create "+release1 {
			runs = append(runs, i)
		}
	}
	stopped2 := slices.Index(calls, "remove "+release2)
	assert.Equal(t, len(runs), 2, "%v", calls)
	assert.Assert(t, runs[0] < stopped2 && stopped2 < runs[1], "%v", calls)
	_, held := holderValue(t, f, r1, "port")
	assert.Assert(t, !held)
}

func TestDownRetryKeepsTheReplicaAFailedStopHookKept(t *testing.T) {
	f := newFakeCmdman()
	rec := &commandPhaseReporter{}
	s := f.service(rec)
	nc := stepCommand("web", 1, portHook(""), eventHook("mark", "", LifecycleStopPost))
	r := acquirePort(t, f, s, nc, model.EventTypeRunning)
	release := ExecCommandName(r.Name, "port", LifecycleStopPost)
	var values []string
	failOnceThenSucceed(f, release, &values)

	res, err := s.Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.Equal(t, len(res.Releases), 0, "%+v", res.Releases)
	assert.Equal(t, len(unreleasedEvents(rec)), 0)
	assert.DeepEqual(t, values, []string{"port-1", "port-1"})
	_, held := holderValue(t, f, r, "port")
	assert.Assert(t, !held, "the retry released the resource")

	errs := stopErrs(res.Stops)
	assert.Equal(t, len(errs), 1)
	assert.ErrorContains(t, errs[0], `hook "port" stop_post`)
	assert.ErrorContains(t, removeOutcomes(res)["web"], "kept after a failed stop hook")
	assert.ErrorContains(t, removeOutcomes(res)["web"], `hook "port" stop_post`)
	assert.Assert(t, errors.Is(removeOutcomes(res)["web"], ErrReplicaKept))
	assert.DeepEqual(t, lifecycleTrace(f, r.Name), []string{
		"port.start_pre", "stop", "port.stop_post", "port.stop_post",
	})
	assert.Equal(t, len(hookPhases(rec, "mark")), 0, "the hook after the failed one never runs")
	kept, ok := f.get(r.Name)
	assert.Assert(t, ok, "the replica stays kept")
	assert.Equal(t, kept.State, model.EventTypeExited)
}

func TestDownDoesNotRetryReleaseOfLiveReplica(t *testing.T) {
	// stopPreHook acquires resource port at start_pre and releases it at
	// stop_pre under onError.
	stopPreHook := func(onError OnError) LifecycleHook {
		return LifecycleHook{
			Name:     "port",
			Resource: "port",
			Events: map[LifecycleEvent]LifecycleExec{
				LifecycleStartPre: {Args: []string{"alloc"}},
				LifecycleStopPre:  {Args: []string{"free"}, OnError: onError},
			},
		}
	}
	for _, tc := range []struct {
		name string
		hook LifecycleHook
		// release is the event of hook that releases the resource.
		release LifecycleEvent
		// stopFails fails the stop of the replica.
		stopFails   bool
		monitorDied bool
		wantErr     bool
		wantState   model.EventType
	}{
		{
			name:      "a failed stop_pre keeps the replica running",
			hook:      stopPreHook(""),
			release:   LifecycleStopPre,
			wantErr:   true,
			wantState: model.EventTypeRunning,
		},
		{
			name:      "the stop failed",
			hook:      stopPreHook(OnErrorContinue),
			release:   LifecycleStopPre,
			stopFails: true,
		},
		{
			name:        "the monitor died",
			hook:        portHook(OnErrorContinue),
			release:     LifecycleStopPost,
			monitorDied: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			s := f.service(rec)
			nc := stepCommand("web", 1, tc.hook)
			r := acquirePort(t, f, s, nc, model.EventTypeRunning)
			release := ExecCommandName(r.Name, "port", tc.release)
			var values []string
			failOnceThenSucceed(f, release, &values)
			if tc.stopFails {
				f.stopErr = func(name string) error {
					if name == r.Name {
						return errors.New("timeout waiting for stop")
					}
					return nil
				}
			}
			if tc.monitorDied {
				f.monitorDied = func(name string) bool { return name == r.Name }
			}

			res, err := s.Down(t.Context(), storedSelection(), DownOption{})

			assert.NilError(t, err)
			assert.Equal(t, execCreated(f, release), 1, "the release runs once")
			assert.Equal(t, len(res.Releases), 1, "%+v", res.Releases)
			o := res.Releases[0]
			assert.Equal(t, o.Holder, HolderName(r.Name, "port"))
			assert.Assert(t, !o.Retried)
			failure := o.Warning
			if tc.wantErr {
				failure = o.Err
			}
			assert.ErrorContains(t, failure, "exited with code 1")
			events := unreleasedEvents(rec)
			assert.Equal(t, len(events), 1, "%+v", events)
			assert.Assert(t, !events[0].Retried)
			value, held := holderValue(t, f, r, "port")
			assert.Assert(t, held)
			assert.Equal(t, value, "port-1")
			replica, kept := f.get(r.Name)
			assert.Equal(t, kept, tc.wantState != "")
			if kept {
				assert.Equal(t, replica.State, tc.wantState)
			}
		})
	}
}

// setState leaves the command named name in state, with stateJSON.
func setState(f *fakeCmdman, name string, state model.EventType, stateJSON *model.CommandState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.lookup(name)
	c.entry.State = state
	c.entry.StateJSON = stateJSON
}

func TestRetryReleasesNeedsAVerifiedStop(t *testing.T) {
	died := &model.CommandState{Error: monitorDiedError}
	assert.Assert(t, monitor.DiedUnexpectedly(died), "the fake records what stale cleanup does")
	for _, tc := range []struct {
		name      string
		state     model.EventType
		stateJSON *model.CommandState
		missing   bool
		wantRetry bool
	}{
		{name: "exited", state: model.EventTypeExited, wantRetry: true},
		{name: "failed", state: model.EventTypeFailed, wantRetry: true},
		{
			name:      "failed with an error of the run",
			state:     model.EventTypeFailed,
			stateJSON: &model.CommandState{Error: "exec: no such file"},
			wantRetry: true,
		},
		{name: "failed as its monitor died", state: model.EventTypeFailed, stateJSON: died},
		{name: "running", state: model.EventTypeRunning},
		{name: "starting", state: model.EventTypeStarting},
		{name: "created", state: model.EventTypeCreated},
		{name: "missing", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			s := f.service(nil)
			nc := stepCommand("web", 1, portHook(OnErrorContinue))
			r := acquirePort(t, f, s, nc, model.EventTypeExited)
			if tc.missing {
				_, err := f.remove(t.Context(), cmdman.RemoveRequest{Targets: []string{r.Name}})
				assert.NilError(t, err)
			} else {
				setState(f, r.Name, tc.state, tc.stateJSON)
			}
			stored, ok := f.get(HolderName(r.Name, "port"))
			assert.Assert(t, ok)
			holder, err := decodeHolder(stored)
			assert.NilError(t, err)
			firstErr := errors.New("first run failed")
			rec := &releaseRecorder{}
			rec.record(failedRelease{
				holder:  holder,
				display: "web",
				err:     firstErr,
				onError: OnErrorContinue,
			})
			release := ExecCommandName(r.Name, "port", LifecycleStopPost)
			f.run = nil

			s.retryReleases(t.Context(), rec, storedSelection(), false)

			left := rec.failures()
			if tc.wantRetry {
				assert.Equal(t, execCreated(f, release), 1)
				assert.Equal(t, len(left), 0, "the retry worked: %+v", left)
				_, held := holderValue(t, f, r, "port")
				assert.Assert(t, !held)
				return
			}
			assert.Equal(t, execCreated(f, release), 0)
			assert.Equal(t, len(left), 1)
			assert.Equal(t, left[0].err, firstErr)
			assert.Assert(t, !left[0].retried)
			_, held := holderValue(t, f, r, "port")
			assert.Assert(t, held)
		})
	}
}

func TestRetryReleasesRunsEachReleaseOnce(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	nc := stepCommand("web", 1, portHook(OnErrorContinue))
	r := acquirePort(t, f, s, nc, model.EventTypeExited)
	stored, ok := f.get(HolderName(r.Name, "port"))
	assert.Assert(t, ok)
	holder, err := decodeHolder(stored)
	assert.NilError(t, err)
	release := ExecCommandName(r.Name, "port", LifecycleStopPost)
	f.run = func(name string, _ cmdman.CreateRequest) fakeRun {
		if name == release {
			return fakeRun{exit: new(2)}
		}
		return fakeRun{exit: new(0)}
	}
	rec := &releaseRecorder{}
	ctx := withReleaseRecorder(t.Context(), rec)
	for range 2 {
		releaseRecorderFrom(ctx).record(failedRelease{
			holder:  holder,
			display: "web",
			err:     errors.New("first run failed"),
			onError: OnErrorContinue,
		})
	}

	s.retryReleases(ctx, rec, storedSelection(), false)

	assert.Equal(t, execCreated(f, release), 1, "a holder recorded twice runs once")
	left := rec.failures()
	assert.Equal(t, len(left), 1, "the retry is not recorded as a failure of its own: %+v", left)
	assert.Assert(t, left[0].retried)
	assert.Equal(t, left[0].onError, OnErrorContinue)
	assert.ErrorContains(t, left[0].err, "exited with code 2")
	assert.Equal(t, left[0].display, "web")
	assert.DeepEqual(t, left[0].holder, holder)
}

func TestRetryReleasesRunsNothingWhenTheReplicasCannotBeListed(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	nc := stepCommand("web", 1, portHook(OnErrorContinue))
	r := acquirePort(t, f, s, nc, model.EventTypeExited)
	stored, ok := f.get(HolderName(r.Name, "port"))
	assert.Assert(t, ok)
	holder, err := decodeHolder(stored)
	assert.NilError(t, err)
	f.listErr = func(cmdman.ListRequest) error { return errors.New("database is locked") }
	rec := &releaseRecorder{}
	rec.record(failedRelease{holder: holder, display: "web", err: errors.New("failed")})

	s.retryReleases(t.Context(), rec, storedSelection(), false)

	assert.Equal(t, execCreated(f, ExecCommandName(r.Name, "port", LifecycleStopPost)), 0)
	left := rec.failures()
	assert.Equal(t, len(left), 1)
	assert.Assert(t, !left[0].retried)
}

func TestRetryReleasesRunsNothingOnceCancelled(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	nc := stepCommand("web", 1, portHook(OnErrorContinue))
	r := acquirePort(t, f, s, nc, model.EventTypeExited)
	stored, ok := f.get(HolderName(r.Name, "port"))
	assert.Assert(t, ok)
	holder, err := decodeHolder(stored)
	assert.NilError(t, err)
	lists := 0
	f.listErr = func(cmdman.ListRequest) error {
		lists++
		return nil
	}
	firstErr := errors.New("first run failed")
	rec := &releaseRecorder{}
	rec.record(failedRelease{
		holder:  holder,
		display: "web",
		err:     firstErr,
		onError: OnErrorContinue,
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	s.retryReleases(ctx, rec, storedSelection(), false)

	assert.Equal(t, lists, 0, "the replicas are not even listed")
	assert.Equal(t, execCreated(f, ExecCommandName(r.Name, "port", LifecycleStopPost)), 0)
	left := rec.failures()
	assert.Equal(t, len(left), 1, "the failure is still reported")
	assert.Equal(t, left[0].err, firstErr)
	assert.Equal(t, left[0].onError, OnErrorContinue)
	assert.Assert(t, !left[0].retried)
	value, held := holderValue(t, f, r, "port")
	assert.Assert(t, held)
	assert.Equal(t, value, "port-1")
}
