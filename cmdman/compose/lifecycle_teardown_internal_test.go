package compose

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// storedSelection selects project proj in /wd without a compose file.
func storedSelection() ProjectSelection {
	return ProjectSelection{WorkDir: "/wd", Project: "proj"}
}

// failExecNamed makes the exec command named failing exit 1.
func failExecNamed(f *fakeCmdman, failing string) {
	f.run = func(name string, _ cmdman.CreateRequest) fakeRun {
		if name == failing {
			return fakeRun{exit: new(1), stdout: []string{"boom"}}
		}
		return fakeRun{exit: new(0)}
	}
}

func execCreated(f *fakeCmdman, exec string) int {
	n := 0
	for _, call := range f.callLog() {
		if call == "create "+exec {
			n++
		}
	}
	return n
}

func removeOutcomes(res *DownResult) map[string]error {
	out := map[string]error{}
	for _, o := range res.Removes {
		out[o.Command] = o.Err
	}
	return out
}

func stopErrs(stops []StopOutcome) []error {
	var out []error
	for _, o := range stops {
		if o.Err != nil {
			out = append(out, o.Err)
		}
	}
	return out
}

func TestStopRunsStoredStopHooks(t *testing.T) {
	nc := stepCommand("web", 2, eventHook("mark", "", lifecycleEvents[:]...))
	// The file declares other hooks: stop hooks come from the stored replica.
	fromFile := stepCommand("web", 2, eventHook("file", "", lifecycleEvents[:]...))
	fileSpec := stepSpec(fromFile)
	for name, selection := range map[string]ProjectSelection{
		"spec":   SelectionFromSpec(&fileSpec),
		"stored": storedSelection(),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeCmdman()
			spec := stepSpec(nc)
			putReplica(t, f, spec, nc, 1, model.EventTypeRunning)
			putReplica(t, f, spec, nc, 2, model.EventTypeExited)

			res, err := f.service(nil).Stop(t.Context(), selection, StopOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(stopErrs(res.Stops)), 0)
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(nc, 1)),
				[]string{"mark.stop_pre", "stop", "mark.stop_post"})
			assert.Equal(t, len(lifecycleTrace(f, replicaName(nc, 2))), 0,
				"an exited replica runs no stop hooks")
		})
	}
}

func TestStopHookFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failing   string
		onError   OnError
		wantErr   string
		wantTrace []string
		wantState model.EventType
	}{
		{
			name:      "stop_pre keeps the replica running",
			failing:   ".stop_pre",
			wantErr:   `hook "mark" stop_pre`,
			wantTrace: []string{"mark.stop_pre"},
			wantState: model.EventTypeRunning,
		},
		{
			name:      "stop_post fails after the stop",
			failing:   ".stop_post",
			wantErr:   `hook "mark" stop_post`,
			wantTrace: []string{"mark.stop_pre", "stop", "mark.stop_post"},
			wantState: model.EventTypeExited,
		},
		{
			name:      "continue only warns",
			failing:   ".stop_pre",
			onError:   OnErrorContinue,
			wantTrace: []string{"mark.stop_pre", "stop", "mark.stop_post"},
			wantState: model.EventTypeExited,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			rec := &commandPhaseReporter{}
			nc := stepCommand("web", 1,
				eventHook("mark", tc.onError, LifecycleStopPre, LifecycleStopPost))
			putReplica(t, f, stepSpec(nc), nc, 1, model.EventTypeRunning)

			res, err := f.service(rec).Stop(t.Context(), storedSelection(), StopOption{})

			assert.NilError(t, err)
			errs := stopErrs(res.Stops)
			if tc.wantErr == "" {
				assert.Equal(t, len(errs), 0)
				assert.Assert(t, rec.hookPhaseSeen(PhaseHookWarning))
			} else {
				assert.Equal(t, len(errs), 1)
				assert.ErrorContains(t, errs[0], tc.wantErr)
			}
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(nc, 1)), tc.wantTrace)
			replica, _ := f.get(replicaName(nc, 1))
			assert.Equal(t, replica.State, tc.wantState)
		})
	}
}

func TestStopReleasesStartResourceAndStartAcquiresIt(t *testing.T) {
	f := newFakeCmdman()
	var released []string
	acquired := 0
	f.run = func(name string, req cmdman.CreateRequest) fakeRun {
		switch {
		case strings.HasSuffix(name, ".port.start_pre"):
			acquired++
			return fakeRun{exit: new(0), stdout: []string{"port-" + string(rune('0'+acquired))}}
		case strings.HasSuffix(name, ".port.stop_post"):
			v, _ := envValue(req.Env, ENV_CMDMAN_COMPOSE_RESOURCE_VALUE)
			released = append(released, v)
		}
		return fakeRun{exit: new(0)}
	}
	nc := stepCommand("web", 1, LifecycleHook{
		Name:     "port",
		Resource: "port",
		Events: map[LifecycleEvent]LifecycleExec{
			LifecycleStartPre: {Args: []string{"alloc"}},
			LifecycleStopPost: {Args: []string{"free"}},
		},
	})
	spec := stepSpec(nc)
	putReplica(t, f, spec, nc, 1, model.EventTypeRunning)
	s := f.service(nil)
	r := s.specHookReplica(spec, nc, 1)
	putTestHolder(f, r, "port", "port-0")

	_, err := s.Stop(t.Context(), storedSelection(), StopOption{})
	assert.NilError(t, err)
	assert.DeepEqual(t, released, []string{"port-0"})
	_, held := holderValue(t, f, r, "port")
	assert.Assert(t, !held, "the stop released the resource")

	res, err := s.Start(t.Context(), storedSelection(), StartOption{})
	assert.NilError(t, err)
	for _, o := range res.Starts {
		assert.NilError(t, o.Err, o.Command)
	}
	value, held := holderValue(t, f, r, "port")
	assert.Assert(t, held, "the start acquired the resource again")
	assert.Equal(t, value, "port-1")
}

func TestDownRunsStopAndRemoveHooks(t *testing.T) {
	nc := stepCommand("web", 1,
		eventHook("mark", "", lifecycleEvents[:]...), scratchHook("", ""))
	spec := stepSpec(nc)
	for name, selection := range map[string]ProjectSelection{
		"spec":   SelectionFromSpec(&spec),
		"stored": storedSelection(),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeCmdman()
			var releaseEnv []string
			f.run = func(name string, req cmdman.CreateRequest) fakeRun {
				if strings.HasSuffix(name, ".scratch.remove_post") {
					releaseEnv = req.Env
				}
				return fakeRun{exit: new(0)}
			}
			putReplica(t, f, spec, nc, 1, model.EventTypeRunning)
			s := f.service(nil)
			r := s.specHookReplica(spec, nc, 1)
			putTestHolder(f, r, "scratch", "/tmp/web")
			// An exec command an earlier failed hook left for inspection.
			left := ExecCommandName(r.Name, "mark", LifecycleStartPost)
			f.put(cmdman.CreateRequest{
				Name:   left,
				Argv:   []string{"false"},
				Labels: execLabels(r, "mark", LifecycleStartPost),
			}, model.EventTypeExited)

			res, err := s.Down(t.Context(), selection, DownOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(stopErrs(res.Stops)), 0)
			for command, err := range removeOutcomes(res) {
				assert.NilError(t, err, command)
			}
			assert.Equal(t, len(res.Releases), 0)
			assert.DeepEqual(t, lifecycleTrace(f, r.Name), []string{
				"mark.stop_pre", "stop", "mark.stop_post",
				"mark.remove_pre", "remove", "mark.remove_post", "scratch.remove_post",
			})
			value, _ := envValue(releaseEnv, ENV_CMDMAN_COMPOSE_RESOURCE_VALUE)
			assert.Equal(t, value, "/tmp/web", "remove_post reads the value of the removed replica")
			all, err := f.list(t.Context(), cmdman.ListRequest{})
			assert.NilError(t, err)
			assert.Equal(t, len(all), 0, "nothing of the project is left: %v", all)
		})
	}
}

func TestDownKeepsReplicaWhoseStopHookFailed(t *testing.T) {
	for _, tc := range []struct {
		event     LifecycleEvent
		wantTrace []string
		wantState model.EventType
	}{
		{
			event:     LifecycleStopPre,
			wantTrace: []string{"mark.stop_pre"},
			wantState: model.EventTypeRunning,
		},
		{
			event:     LifecycleStopPost,
			wantTrace: []string{"mark.stop_pre", "stop", "mark.stop_post"},
			wantState: model.EventTypeExited,
		},
	} {
		t.Run(string(tc.event), func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			web := stepCommand("web", 2, eventHook("mark", "", lifecycleEvents[:]...))
			db := stepCommand("db", 1)
			spec := stepSpec(web, db)
			putReplica(t, f, spec, web, 1, model.EventTypeRunning)
			putReplica(t, f, spec, web, 2, model.EventTypeRunning)
			putReplica(t, f, spec, db, 1, model.EventTypeRunning)
			failExecNamed(f, ExecCommandName(replicaName(web, 1), "mark", tc.event))

			res, err := f.service(rec).Down(t.Context(), SelectionFromSpec(&spec), DownOption{})

			assert.NilError(t, err)
			removes := removeOutcomes(res)
			assert.ErrorContains(t, removes["web-1"], "kept after a failed stop hook")
			assert.ErrorContains(t, removes["web-1"], `hook "mark" `+string(tc.event))
			assert.NilError(t, removes["web-2"])
			assert.NilError(t, removes["db"])
			assert.Assert(t, rec.reached("web-1", PhaseSkipped))

			assert.DeepEqual(t, lifecycleTrace(f, replicaName(web, 1)), tc.wantTrace)
			kept, ok := f.get(replicaName(web, 1))
			assert.Assert(t, ok, "the replica whose stop hook failed is kept")
			assert.Equal(t, kept.State, tc.wantState)

			assert.DeepEqual(t, lifecycleTrace(f, replicaName(web, 2)), []string{
				"mark.stop_pre", "stop", "mark.stop_post", "mark.remove_pre", "remove",
				"mark.remove_post",
			})
			for _, gone := range []string{replicaName(web, 2), replicaName(db, 1)} {
				_, ok := f.get(gone)
				assert.Assert(t, !ok, "%s should be removed", gone)
			}
		})
	}
}

func TestDownRemovesReplicaWhoseStopFailed(t *testing.T) {
	f := newFakeCmdman()
	nc := stepCommand("web", 1, eventHook("mark", "", lifecycleEvents[:]...))
	spec := stepSpec(nc)
	putReplica(t, f, spec, nc, 1, model.EventTypeRunning)
	replica := replicaName(nc, 1)
	f.stopErr = func(name string) error {
		if name == replica {
			return errors.New("timeout waiting for stop")
		}
		return nil
	}

	res, err := f.service(nil).Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	errs := stopErrs(res.Stops)
	assert.Equal(t, len(errs), 1)
	assert.ErrorContains(t, errs[0], "timeout waiting for stop")
	assert.NilError(t, removeOutcomes(res)["web"])
	assert.DeepEqual(t, lifecycleTrace(f, replica), []string{
		"mark.stop_pre", "stop", "mark.remove_pre", "remove", "mark.remove_post",
	})
	_, left := f.get(replica)
	assert.Assert(t, !left, "a replica whose stop failed is removed by force")
}

func TestDownRemovePreFailureKeepsReplica(t *testing.T) {
	f := newFakeCmdman()
	failExec(f, ".remove_pre")
	nc := stepCommand("web", 1, eventHook("mark", "", lifecycleEvents[:]...))
	putReplica(t, f, stepSpec(nc), nc, 1, model.EventTypeExited)
	replica := replicaName(nc, 1)

	res, err := f.service(nil).Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.ErrorContains(t, removeOutcomes(res)["web"], `hook "mark" remove_pre`)
	assert.DeepEqual(t, lifecycleTrace(f, replica), []string{"mark.remove_pre"})
	_, kept := f.get(replica)
	assert.Assert(t, kept)
	_, execKept := f.get(ExecCommandName(replica, "mark", LifecycleRemovePre))
	assert.Assert(t, execKept, "the failed exec command of a kept replica stays")
}

func TestDownForceLetsHookFailuresPass(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(name string, _ cmdman.CreateRequest) fakeRun {
		if strings.Contains(name, ".hook.") {
			return fakeRun{exit: new(1)}
		}
		return fakeRun{exit: new(0)}
	}
	rec := &commandPhaseReporter{}
	nc := stepCommand("web", 1, eventHook("mark", "", lifecycleEvents[:]...))
	putReplica(t, f, stepSpec(nc), nc, 1, model.EventTypeRunning)
	replica := replicaName(nc, 1)

	res, err := f.service(rec).Down(t.Context(), storedSelection(), DownOption{Force: true})

	assert.NilError(t, err)
	assert.Equal(t, len(stopErrs(res.Stops)), 0)
	assert.NilError(t, removeOutcomes(res)["web"])
	assert.Assert(t, rec.hookPhaseSeen(PhaseHookWarning))
	assert.Assert(t, !rec.hookPhaseSeen(PhaseHookFailed))
	assert.DeepEqual(t, lifecycleTrace(f, replica), []string{
		"mark.stop_pre", "stop", "mark.stop_post", "mark.remove_pre", "remove",
		"mark.remove_post",
	})
	all, err := f.list(t.Context(), cmdman.ListRequest{})
	assert.NilError(t, err)
	assert.Equal(t, len(all), 0, "the replica and the exec commands of its failed hooks go")
}

// putUndecodableReplica stores replica idx of nc as putReplica does, left in
// state, with a hooks label that does not decode.
func putUndecodableReplica(
	t *testing.T,
	f *fakeCmdman,
	nc Command,
	idx int,
	state model.EventType,
) {
	t.Helper()
	hash, err := Hash(nc)
	assert.NilError(t, err)
	req := buildCreateRequest(stepSpec(nc), nc, hash, replicaName(nc, idx), idx)
	req.Labels[LabelHooks] = "not-json"
	f.put(req, state)
}

// replicaWarnings returns the errors of the hook-warning events reported for
// the replica command itself rather than for a hook run of it.
func replicaWarnings(rec *commandPhaseReporter, command string) []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []string
	for _, ev := range rec.events {
		if ev.Command == command && ev.Hook == "" && ev.Phase == PhaseHookWarning {
			out = append(out, ev.Err.Error())
		}
	}
	return out
}

func TestDownUndecodableStoredHooks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state model.EventType
		force bool
		// steps are the teardown steps that go on without the hooks.
		steps []string
	}{
		{name: "running", state: model.EventTypeRunning},
		{name: "exited", state: model.EventTypeExited},
		{
			name:  "running forced",
			state: model.EventTypeRunning,
			force: true,
			steps: []string{"stop", "remove"},
		},
		{name: "exited forced", state: model.EventTypeExited, force: true, steps: []string{"remove"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			nc := stepCommand("web", 1, eventHook("mark", "", lifecycleEvents[:]...))
			putUndecodableReplica(t, f, nc, 1, tc.state)
			replica := replicaName(nc, 1)

			res, err := f.service(rec).Down(
				t.Context(), storedSelection(), DownOption{Force: tc.force})

			assert.NilError(t, err)
			assert.DeepEqual(t, lifecycleTrace(f, replica), tc.steps)
			warnings := replicaWarnings(rec, "web")
			assert.Equal(t, len(warnings), len(tc.steps), "warnings: %q", warnings)
			for i, step := range tc.steps {
				assert.Assert(t, strings.HasPrefix(warnings[i], step+" without the stored hooks"),
					warnings[i])
				assert.Assert(t, strings.Contains(warnings[i], "decode "+LabelHooks), warnings[i])
			}
			stored, kept := f.get(replica)
			if tc.force {
				assert.Equal(t, len(stopErrs(res.Stops)), 0)
				assert.NilError(t, removeOutcomes(res)["web"])
				assert.Assert(t, !kept, "a forced down removes the replica without its hooks")
				return
			}
			assert.ErrorContains(t, removeOutcomes(res)["web"], "decode "+LabelHooks)
			if tc.state == model.EventTypeRunning {
				errs := stopErrs(res.Stops)
				assert.Equal(t, len(errs), 1)
				assert.ErrorContains(t, errs[0], "decode "+LabelHooks)
				assert.ErrorContains(t, removeOutcomes(res)["web"], "kept after a failed stop hook")
			}
			assert.Assert(t, kept, "the replica whose hooks cannot be read is kept")
			assert.Equal(t, stored.State, tc.state)
		})
	}
}

func TestUndecodableStoredHooksKeepTheReplica(t *testing.T) {
	nc := stepCommand("web", 2, eventHook("mark", "", lifecycleEvents[:]...))
	changed := nc
	changed.Hooks = []LifecycleHook{eventHook("new", "", LifecycleCreatePre)}
	scaled := nc
	scaled.Scale = 1
	createErrs := func(t *testing.T, s *Service, spec ComposeSpec) error {
		res, err := s.Create(t.Context(), spec, CreateOption{})
		assert.NilError(t, err)
		var errs []error
		for _, a := range res.Actions {
			errs = append(errs, a.Err)
		}
		return errors.Join(errs...)
	}
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, s *Service) error
	}{
		{
			name: "stop",
			run: func(t *testing.T, s *Service) error {
				res, err := s.Stop(t.Context(), storedSelection(), StopOption{})
				assert.NilError(t, err)
				return errors.Join(stopErrs(res.Stops)...)
			},
		},
		{
			name: "restart",
			run: func(t *testing.T, s *Service) error {
				res, err := s.Restart(t.Context(), storedSelection(), RestartOption{})
				assert.NilError(t, err)
				var errs []error
				for _, o := range res.Restarts {
					errs = append(errs, o.StopErr)
				}
				return errors.Join(errs...)
			},
		},
		{
			name: "recreate",
			run: func(t *testing.T, s *Service) error {
				return createErrs(t, s, stepSpec(changed))
			},
		},
		{
			name: "scale-down",
			run: func(t *testing.T, s *Service) error {
				return createErrs(t, s, stepSpec(scaled))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			putReplica(t, f, stepSpec(nc), nc, 1, model.EventTypeRunning)
			putUndecodableReplica(t, f, nc, 2, model.EventTypeRunning)

			err := tc.run(t, f.service(nil))

			assert.ErrorContains(t, err, "decode "+LabelHooks)
			assert.Equal(t, len(lifecycleTrace(f, replicaName(nc, 2))), 0)
			kept, ok := f.get(replicaName(nc, 2))
			assert.Assert(t, ok, "the replica whose hooks cannot be read is kept")
			assert.Equal(t, kept.State, model.EventTypeRunning)
		})
	}
}

func TestDownKeepsOutcomesWhenHolderListFails(t *testing.T) {
	f := newFakeCmdman()
	rec := &commandPhaseReporter{}
	nc := stepCommand("web", 1, eventHook("mark", "", lifecycleEvents[:]...))
	putReplica(t, f, stepSpec(nc), nc, 1, model.EventTypeRunning)
	f.listErr = func(req cmdman.ListRequest) error {
		// Only the sweep for stranded releases lists every holder of the
		// project; the lookup of one holder names its resource key.
		if req.Labels[LabelIntermediate] == IntermediateHolder &&
			req.Labels[LabelResourceKey] == "" {
			return errors.New("database is locked")
		}
		return nil
	}

	res, err := f.service(rec).Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.DeepEqual(t, res.Stops, []StopOutcome{{Command: "web"}})
	assert.DeepEqual(t, res.Removes, []RemoveOutcome{{Command: "web"}})
	assert.Equal(t, len(res.Releases), 1)
	assert.Equal(t, res.Releases[0].Holder, "")
	assert.ErrorContains(t, res.Releases[0].Err, "list resource holders: database is locked")
	assert.Assert(t, rec.reached("proj", PhaseError))
	_, left := f.get(replicaName(nc, 1))
	assert.Assert(t, !left)
}

func TestForcedHooks(t *testing.T) {
	hooks := []LifecycleHook{{
		Name: "h",
		Events: map[LifecycleEvent]LifecycleExec{
			LifecycleStopPre:    {Args: []string{"a"}},
			LifecycleStopPost:   {Args: []string{"b"}, OnError: OnErrorFail},
			LifecycleRemovePre:  {Args: []string{"c"}, OnError: OnErrorContinue},
			LifecycleRemovePost: {Args: []string{"d"}, OnError: OnErrorIgnore},
		},
	}}

	got := forcedHooks(hooks)

	want := map[LifecycleEvent]OnError{
		LifecycleStopPre:    OnErrorContinue,
		LifecycleStopPost:   OnErrorContinue,
		LifecycleRemovePre:  OnErrorContinue,
		LifecycleRemovePost: OnErrorIgnore,
	}
	for ev, onError := range want {
		assert.Equal(t, got[0].Events[ev].OnError, onError, "%s", ev)
	}
	assert.Equal(t, hooks[0].Events[LifecycleStopPre].OnError, OnError(""),
		"the input is left as it is")
}

func TestDownRemovePostFailureKeepsHolderForTheNextDown(t *testing.T) {
	f := newFakeCmdman()
	nc := stepCommand("web", 1, scratchHook("", ""))
	spec := stepSpec(nc)
	putReplica(t, f, spec, nc, 1, model.EventTypeExited)
	s := f.service(nil)
	r := s.specHookReplica(spec, nc, 1)
	// Acquire as create does, so the holder records its release.
	f.run = func(string, cmdman.CreateRequest) fakeRun {
		return fakeRun{exit: new(0), stdout: []string{"/tmp/web"}}
	}
	_, err := s.runLifecycleEvent(t.Context(), r, nc.Hooks, LifecycleCreatePre)
	assert.NilError(t, err)
	release := ExecCommandName(r.Name, "scratch", LifecycleRemovePost)
	failExecNamed(f, release)

	res, err := s.Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.ErrorContains(t, removeOutcomes(res)["web"], `value "/tmp/web"`)
	_, execLeft := f.get(release)
	assert.Assert(t, !execLeft, "the exec commands of a removed replica go with it")
	assert.Equal(t, len(res.Releases), 0, "a release that failed in this down waits for the next")
	assert.Equal(t, execCreated(f, release), 1)
	_, replicaLeft := f.get(r.Name)
	assert.Assert(t, !replicaLeft)
	value, held := holderValue(t, f, r, "scratch")
	assert.Assert(t, held)
	assert.Equal(t, value, "/tmp/web")

	var releaseEnv []string
	f.run = func(name string, req cmdman.CreateRequest) fakeRun {
		if name == release {
			releaseEnv = req.Env
		}
		return fakeRun{exit: new(0)}
	}
	res, err = s.Down(t.Context(), storedSelection(), DownOption{})

	assert.NilError(t, err)
	assert.DeepEqual(t, res.Releases, []ReleaseOutcome{{Holder: HolderName(r.Name, "scratch")}})
	got, _ := envValue(releaseEnv, ENV_CMDMAN_COMPOSE_RESOURCE_VALUE)
	assert.Equal(t, got, "/tmp/web")
	_, held = holderValue(t, f, r, "scratch")
	assert.Assert(t, !held, "the retried release removed the holder")
}

// portHook acquires resource port at start_pre and releases it at stop_post
// under onError.
func portHook(onError OnError) LifecycleHook {
	return LifecycleHook{
		Name:     "port",
		Resource: "port",
		Events: map[LifecycleEvent]LifecycleExec{
			LifecycleStartPre: {Args: []string{"alloc"}},
			LifecycleStopPost: {Args: []string{"free"}, OnError: onError},
		},
	}
}

// acquirePort stores replica 1 of nc in state and acquires its port resource,
// valued port-1, as a start does. It returns the replica as its hooks see it.
func acquirePort(
	t *testing.T,
	f *fakeCmdman,
	s *Service,
	nc Command,
	state model.EventType,
) hookReplica {
	t.Helper()
	spec := stepSpec(nc)
	putReplica(t, f, spec, nc, 1, state)
	r := s.specHookReplica(spec, nc, 1)
	f.run = func(string, cmdman.CreateRequest) fakeRun {
		return fakeRun{exit: new(0), stdout: []string{"port-1"}}
	}
	_, err := s.runLifecycleEvent(t.Context(), r, nc.Hooks, LifecycleStartPre)
	assert.NilError(t, err)
	return r
}

func TestDownReleasesStopResourceOfReplicaItDidNotStop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		force     bool
		exit      int
		wantErr   string
		wantHeld  bool
		wantPhase Phase
	}{
		{name: "success", exit: 0, wantPhase: PhaseHookSucceeded},
		{
			name:      "fail keeps the holder",
			exit:      1,
			wantErr:   `value "port-1"`,
			wantHeld:  true,
			wantPhase: PhaseHookFailed,
		},
		{
			name:      "force turns fail into continue",
			force:     true,
			exit:      1,
			wantHeld:  true,
			wantPhase: PhaseHookWarning,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			s := f.service(rec)
			nc := stepCommand("web", 1,
				portHook(""), eventHook("mark", "", LifecycleRemovePre, LifecycleRemovePost))
			// The command exited on its own, so down does not stop it.
			r := acquirePort(t, f, s, nc, model.EventTypeExited)
			release := ExecCommandName(r.Name, "port", LifecycleStopPost)
			var releaseEnv []string
			f.run = func(name string, req cmdman.CreateRequest) fakeRun {
				if name == release {
					releaseEnv = req.Env
					return fakeRun{exit: new(tc.exit)}
				}
				return fakeRun{exit: new(0)}
			}

			res, err := s.Down(t.Context(), storedSelection(), DownOption{Force: tc.force})

			assert.NilError(t, err)
			assert.NilError(t, removeOutcomes(res)["web"])
			assert.Equal(t, len(res.Releases), 1)
			assert.Equal(t, res.Releases[0].Holder, HolderName(r.Name, "port"))
			if tc.wantErr == "" {
				assert.NilError(t, res.Releases[0].Err)
			} else {
				assert.ErrorContains(t, res.Releases[0].Err, tc.wantErr)
			}
			assert.DeepEqual(t, lifecycleTrace(f, r.Name), []string{
				"port.start_pre", "mark.remove_pre", "remove", "mark.remove_post",
				"port.stop_post",
			})
			assert.Equal(t, execCreated(f, release), 1)
			value, _ := envValue(releaseEnv, ENV_CMDMAN_COMPOSE_RESOURCE_VALUE)
			assert.Equal(t, value, "port-1")
			assert.Assert(t, rec.reached("web", tc.wantPhase))
			_, held := holderValue(t, f, r, "port")
			assert.Equal(t, held, tc.wantHeld)
		})
	}
}

func TestDownRunsTheReleaseOfAReplicaOnce(t *testing.T) {
	t.Run("a release the stop ran waits for the next down", func(t *testing.T) {
		f := newFakeCmdman()
		s := f.service(nil)
		nc := stepCommand("web", 1, portHook(OnErrorContinue))
		r := acquirePort(t, f, s, nc, model.EventTypeRunning)
		release := ExecCommandName(r.Name, "port", LifecycleStopPost)
		failExecNamed(f, release)

		res, err := s.Down(t.Context(), storedSelection(), DownOption{})

		assert.NilError(t, err)
		assert.NilError(t, removeOutcomes(res)["web"])
		assert.Equal(t, len(res.Releases), 0)
		assert.Equal(t, execCreated(f, release), 1)
		_, replicaLeft := f.get(r.Name)
		assert.Assert(t, !replicaLeft)
		_, held := holderValue(t, f, r, "port")
		assert.Assert(t, held)
	})

	t.Run("a replica down does not remove keeps its resource", func(t *testing.T) {
		f := newFakeCmdman()
		s := f.service(nil)
		nc := stepCommand("web", 1, portHook(""), eventHook("gate", "", LifecycleRemovePre))
		r := acquirePort(t, f, s, nc, model.EventTypeExited)
		failExecNamed(f, ExecCommandName(r.Name, "gate", LifecycleRemovePre))

		res, err := s.Down(t.Context(), storedSelection(), DownOption{})

		assert.NilError(t, err)
		assert.ErrorContains(t, removeOutcomes(res)["web"], `hook "gate" remove_pre`)
		assert.Equal(t, len(res.Releases), 0)
		assert.Equal(t, execCreated(f, ExecCommandName(r.Name, "port", LifecycleStopPost)), 0)
		_, held := holderValue(t, f, r, "port")
		assert.Assert(t, held)
	})
}

// putStrandedHolder stores the holder of resource scratch of replica 1 of web,
// acquired by hook slot, whose replica is gone. Its release runs at
// remove_post under onError.
func putStrandedHolder(f *fakeCmdman, onError OnError) resourceHolder {
	r := testHookReplica()
	h := resourceHolder{
		Ref:   r.resourceRef("scratch"),
		Owner: r.Name,
		Value: "/tmp/scratch",
		Release: &resourceRelease{
			Event:   LifecycleRemovePost,
			Args:    []string{"rm", "-rf"},
			OnError: onError,
		},
		Dir: "/wd/web",
		Env: []string{
			"REPLICA_VAR=replica",
			ENV_CMDMAN_COMPOSE_HOOK_NAME + "=slot",
			ENV_CMDMAN_COMPOSE_SCALE + "=2",
		},
	}
	req, err := h.createRequest()
	if err != nil {
		panic(err)
	}
	f.put(req, model.EventTypeCreated)
	return h
}

func TestDownRetriesStrandedRelease(t *testing.T) {
	for _, tc := range []struct {
		name       string
		onError    OnError
		force      bool
		exit       int
		wantErr    string
		wantHeld   bool
		wantPhase  Phase
		wantExecOK bool
	}{
		{name: "success", exit: 0, wantPhase: PhaseHookSucceeded},
		{
			name:       "fail keeps the holder",
			exit:       1,
			wantErr:    `value "/tmp/scratch"`,
			wantHeld:   true,
			wantPhase:  PhaseHookFailed,
			wantExecOK: true,
		},
		{
			name:       "continue keeps the holder with a warning",
			onError:    OnErrorContinue,
			exit:       1,
			wantHeld:   true,
			wantPhase:  PhaseHookWarning,
			wantExecOK: true,
		},
		{
			name:       "ignore drops the holder",
			onError:    OnErrorIgnore,
			exit:       1,
			wantPhase:  PhaseHookIgnored,
			wantExecOK: true,
		},
		{
			name:       "force turns fail into continue",
			force:      true,
			exit:       1,
			wantHeld:   true,
			wantPhase:  PhaseHookWarning,
			wantExecOK: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			rec := &commandPhaseReporter{}
			h := putStrandedHolder(f, tc.onError)
			exec := ExecCommandName(h.Owner, "slot", LifecycleRemovePost)
			var got cmdman.CreateRequest
			f.run = func(name string, req cmdman.CreateRequest) fakeRun {
				if name == exec {
					got = req
				}
				return fakeRun{exit: new(tc.exit)}
			}

			res, err := f.service(rec).Down(
				t.Context(), storedSelection(), DownOption{Force: tc.force})

			assert.NilError(t, err)
			assert.Equal(t, len(res.Releases), 1)
			assert.Equal(t, res.Releases[0].Holder, h.name())
			if tc.wantErr == "" {
				assert.NilError(t, res.Releases[0].Err)
			} else {
				assert.ErrorContains(t, res.Releases[0].Err, tc.wantErr)
			}
			assert.Equal(t, got.Dir, "/wd/web")
			assert.DeepEqual(t, got.Argv, []string{"rm", "-rf"})
			assert.DeepEqual(t, got.Env, slices.Concat(h.Env, []string{
				ENV_CMDMAN_COMPOSE_RESOURCE_KEY + "=scratch",
				ENV_CMDMAN_COMPOSE_RESOURCE_VALUE + "=/tmp/scratch",
			}))
			assert.Assert(t, rec.reached("web-1", tc.wantPhase),
				"the release is reported for the replica it was held for")
			_, held := f.get(h.name())
			assert.Equal(t, held, tc.wantHeld)
			_, execLeft := f.get(exec)
			assert.Equal(t, execLeft, tc.wantExecOK)
		})
	}
}

func TestDownLeavesHoldersAlone(t *testing.T) {
	t.Run("no release", func(t *testing.T) {
		f := newFakeCmdman()
		r := testHookReplica()
		putTestHolder(f, r, "scratch", "/tmp/scratch")

		res, err := f.service(nil).Down(t.Context(), storedSelection(), DownOption{})

		assert.NilError(t, err)
		assert.Equal(t, len(res.Releases), 0)
		_, held := holderValue(t, f, r, "scratch")
		assert.Assert(t, held)
		assert.Equal(t, len(f.callLog()), 0)
	})

	t.Run("named commands", func(t *testing.T) {
		f := newFakeCmdman()
		h := putStrandedHolder(f, "")
		db := stepCommand("db", 1)
		putReplica(t, f, stepSpec(db), db, 1, model.EventTypeExited)

		res, err := f.service(nil).Down(t.Context(), storedSelection(), DownOption{
			Targets: TargetsOf("db"),
		})

		assert.NilError(t, err)
		assert.NilError(t, removeOutcomes(res)["db"])
		assert.Equal(t, len(res.Releases), 0)
		_, held := f.get(h.name())
		assert.Assert(t, held, "a down of named commands retries no release")
	})

	t.Run("intermediates are not replicas", func(t *testing.T) {
		f := newFakeCmdman()
		r := testHookReplica()
		putTestHolder(f, r, "scratch", "/tmp/scratch")
		exec := ExecCommandName(r.Name, "mark", LifecycleStartPre)
		f.put(cmdman.CreateRequest{
			Name:   exec,
			Argv:   []string{"sleep", "300"},
			Labels: execLabels(r, "mark", LifecycleStartPre),
		}, model.EventTypeRunning)

		for _, selection := range []ProjectSelection{storedSelection(), {WorkDir: "/wd"}} {
			_, err := f.service(nil).Stop(t.Context(), selection, StopOption{})
			assert.NilError(t, err)
			_, err = f.service(nil).Down(t.Context(), selection, DownOption{})
			assert.NilError(t, err)
		}

		assert.Equal(t, len(f.callLog()), 0)
		running, _ := f.get(exec)
		assert.Equal(t, running.State, model.EventTypeRunning)
	})
}

func TestUpSkipsReplicaWhoseRecreateAborted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     model.EventType
		failing   string
		wantErr   string
		wantTrace []string
	}{
		{
			name:      "stop_pre leaves the old replica running",
			state:     model.EventTypeRunning,
			failing:   ".old.stop_pre",
			wantErr:   `hook "old" stop_pre`,
			wantTrace: []string{"old.stop_pre"},
		},
		{
			name:      "stop_post leaves the old replica stopped",
			state:     model.EventTypeRunning,
			failing:   ".old.stop_post",
			wantErr:   `hook "old" stop_post`,
			wantTrace: []string{"old.stop_pre", "stop", "old.stop_post"},
		},
		{
			name:      "remove_post leaves no replica",
			state:     model.EventTypeExited,
			failing:   ".old.remove_post",
			wantErr:   `hook "old" remove_post`,
			wantTrace: []string{"old.remove_pre", "remove", "old.remove_post"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			rec := &commandPhaseReporter{}
			old := stepCommand("web", 1, eventHook("old", "",
				LifecycleStopPre, LifecycleStopPost, LifecycleRemovePre, LifecycleRemovePost))
			putReplica(t, f, stepSpec(old), old, 1, tc.state)
			changed := old
			changed.Hooks = []LifecycleHook{
				eventHook("new", "", LifecycleStartPre, LifecycleStartPost),
			}
			dependent := stepCommand("app", 1)
			dependent.After = []AfterSpec{{Name: "web", Condition: ConditionRunning}}

			res, err := f.service(rec).Up(t.Context(), stepSpec(changed, dependent), UpOption{})

			assert.NilError(t, err)
			starts := map[string]error{}
			for _, s := range res.Starts {
				starts[s.Command] = s.Err
			}
			assert.ErrorContains(t, starts["web"], "not started")
			assert.ErrorContains(t, starts["web"], tc.wantErr)
			assert.ErrorContains(t, starts["app"], `dependency "web" failed`)
			assert.Assert(t, rec.reached("web", PhaseSkipped))
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(old, 1)), tc.wantTrace)
		})
	}
}
