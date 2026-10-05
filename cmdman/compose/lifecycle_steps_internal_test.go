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

// stepCommand is compose command name of project proj in /wd with scale
// replicas and hooks.
func stepCommand(name string, scale int, hooks ...LifecycleHook) Command {
	return Command{
		Name:          name,
		Args:          []string{"sleep", "300"},
		Scale:         scale,
		Hooks:         hooks,
		GeneratedName: GenerateName(workdirHash("/wd"), "proj", name),
	}
}

func stepSpec(cmds ...Command) ComposeSpec {
	return ComposeSpec{
		Project:     "proj",
		WorkDir:     "/wd",
		ComposeFile: "/wd/cmd-compose.yaml",
		Commands:    cmds,
	}
}

// eventHook is hook name running true at each of events, under onError.
func eventHook(name string, onError OnError, events ...LifecycleEvent) LifecycleHook {
	h := LifecycleHook{Name: name, Events: map[LifecycleEvent]LifecycleExec{}}
	for _, ev := range events {
		h.Events[ev] = LifecycleExec{Args: []string{"true"}, OnError: onError}
	}
	return h
}

// replicaName is the cmdman command name of replica idx of nc.
func replicaName(nc Command, idx int) string {
	return InstanceName(nc.GeneratedName, idx)
}

// putReplica stores replica idx of nc as compose creates it, left in state.
func putReplica(
	t *testing.T,
	f *fakeCmdman,
	spec ComposeSpec,
	nc Command,
	idx int,
	state model.EventType,
) {
	t.Helper()
	hash, err := Hash(nc)
	assert.NilError(t, err)
	f.put(buildCreateRequest(spec, nc, hash, replicaName(nc, idx), idx), state)
}

// lifecycleTrace reduces the call log to what happened to replica: "create",
// "start", "stop" and "remove" of the replica itself, and "<hook>.<event>" for
// every exec command created for it.
func lifecycleTrace(f *fakeCmdman, replica string) []string {
	var out []string
	for _, call := range f.callLog() {
		op, name, _ := strings.Cut(call, " ")
		switch {
		case name == replica:
			out = append(out, op)
		case op == "create" && strings.HasPrefix(name, replica+".hook."):
			out = append(out, strings.TrimPrefix(name, replica+".hook."))
		}
	}
	return out
}

// failExec makes every exec command whose name ends with suffix exit 1.
func failExec(f *fakeCmdman, suffix string) {
	f.run = func(name string, _ cmdman.CreateRequest) fakeRun {
		if strings.HasSuffix(name, suffix) {
			return fakeRun{exit: new(1), stdout: []string{"boom"}}
		}
		return fakeRun{exit: new(0)}
	}
}

func (r *commandPhaseReporter) hookPhaseSeen(phase Phase) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.events, func(ev Event) bool {
		return ev.Hook != "" && ev.Phase == phase
	})
}

func TestCreateRunsCreateHooksAroundEachReplica(t *testing.T) {
	f := newFakeCmdman()
	nc := stepCommand("web", 2, eventHook("mark", "", LifecycleCreatePre, LifecycleCreatePost))

	res, err := f.service(nil).Create(t.Context(), stepSpec(nc), CreateOption{})

	assert.NilError(t, err)
	for _, a := range res.Actions {
		assert.NilError(t, a.Err, a.Command)
	}
	for idx := 1; idx <= 2; idx++ {
		assert.DeepEqual(t, lifecycleTrace(f, replicaName(nc, idx)),
			[]string{"mark.create_pre", "create", "mark.create_post"})
	}
}

func TestCreatePreAcquireHoldsForTheReplicaCreatedNext(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(_ string, req cmdman.CreateRequest) fakeRun {
		idx, _ := envValue(req.Env, ENV_CMDMAN_COMPOSE_SCALE_INDEX)
		return fakeRun{exit: new(0), stdout: []string{"/tmp/web-" + idx}}
	}
	nc := stepCommand("web", 2, scratchHook("", ""))

	_, err := f.service(nil).Create(t.Context(), stepSpec(nc), CreateOption{})
	assert.NilError(t, err)

	for idx, value := range map[int]string{1: "/tmp/web-1", 2: "/tmp/web-2"} {
		replica, ok := f.get(replicaName(nc, idx))
		assert.Assert(t, ok, "replica %d was not created", idx)
		holder, ok := f.get(HolderName(replica.Name, "scratch"))
		assert.Assert(t, ok, "replica %d has no holder", idx)
		assert.Equal(t, holder.ConfigJSON.Labels[LabelOwner], replica.Name)
		assert.Equal(t, holder.ConfigJSON.Labels[LabelResourceValue], value)
	}
}

func TestCreateFailureKeepsTheCreatePreHolder(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun {
		return fakeRun{exit: new(0), stdout: []string{"/tmp/web"}}
	}
	nc := stepCommand("web", 1, scratchHook("", ""))
	replica := replicaName(nc, 1)
	f.createErr = func(req cmdman.CreateRequest) error {
		if req.Name == replica {
			return errors.New("disk full")
		}
		return nil
	}
	s := f.service(nil)

	res, err := s.Create(t.Context(), stepSpec(nc), CreateOption{})

	assert.NilError(t, err)
	assert.ErrorContains(t, res.Actions[0].Err, "disk full")
	value, err := s.ResourceGet(t.Context(),
		ProjectSelection{WorkDir: "/wd", Project: "proj"},
		ResourceOption{Command: "web", Key: "scratch"})
	assert.NilError(t, err)
	assert.Equal(t, value, "/tmp/web")
}

func TestCreateHookFailure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		onError     OnError
		failing     string
		wantErr     string
		wantTrace   []string
		wantReplica bool
	}{
		{
			name:      "failing create_pre skips the create",
			failing:   ".create_pre",
			wantErr:   `hook "mark" create_pre`,
			wantTrace: []string{"mark.create_pre"},
		},
		{
			name:        "failing create_post keeps the replica",
			failing:     ".create_post",
			wantErr:     `hook "mark" create_post`,
			wantTrace:   []string{"mark.create_pre", "create", "mark.create_post"},
			wantReplica: true,
		},
		{
			name:        "continue only warns",
			onError:     OnErrorContinue,
			failing:     ".create_pre",
			wantTrace:   []string{"mark.create_pre", "create", "mark.create_post"},
			wantReplica: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			rec := &commandPhaseReporter{}
			nc := stepCommand("web", 1,
				eventHook("mark", tc.onError, LifecycleCreatePre, LifecycleCreatePost))
			replica := replicaName(nc, 1)

			res, err := f.service(rec).Create(t.Context(), stepSpec(nc), CreateOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(res.Actions), 1)
			if tc.wantErr == "" {
				assert.NilError(t, res.Actions[0].Err)
				assert.Assert(t, rec.hookPhaseSeen(PhaseHookWarning))
			} else {
				assert.ErrorContains(t, res.Actions[0].Err, tc.wantErr)
				_, kept := f.get(replica + ".hook.mark" + tc.failing)
				assert.Assert(t, kept, "the failed exec command is kept")
			}
			assert.DeepEqual(t, lifecycleTrace(f, replica), tc.wantTrace)
			_, created := f.get(replica)
			assert.Equal(t, created, tc.wantReplica)
		})
	}
}

func TestRecreateRunsStoredHooksThenSpecHooks(t *testing.T) {
	for _, tc := range []struct {
		state     model.EventType
		wantTrace []string
	}{
		{
			state: model.EventTypeRunning,
			wantTrace: []string{
				"old.stop_pre", "stop", "old.stop_post",
				"old.remove_pre", "remove", "old.remove_post", "scratch.remove_post",
				"new.create_pre", "scratch.create_pre", "create", "new.create_post",
			},
		},
		{
			state: model.EventTypeExited,
			wantTrace: []string{
				"old.remove_pre", "remove", "old.remove_post", "scratch.remove_post",
				"new.create_pre", "scratch.create_pre", "create", "new.create_post",
			},
		},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			f := newFakeCmdman()
			var releaseEnv []string
			f.run = func(name string, req cmdman.CreateRequest) fakeRun {
				switch {
				case strings.HasSuffix(name, ".scratch.remove_post"):
					releaseEnv = req.Env
				case strings.HasSuffix(name, ".scratch.create_pre"):
					return fakeRun{exit: new(0), stdout: []string{"/tmp/new"}}
				}
				return fakeRun{exit: new(0)}
			}
			// The stored replica sets create_pre and the spec sets stop_pre, so a
			// trace that runs either names the wrong source.
			old := stepCommand("web", 1,
				eventHook("old", "", LifecycleCreatePre, LifecycleStopPre,
					LifecycleStopPost, LifecycleRemovePre, LifecycleRemovePost),
				scratchHook("", ""),
			)
			spec := stepSpec(old)
			putReplica(t, f, spec, old, 1, tc.state)
			s := f.service(nil)
			putTestHolder(f, s.specHookReplica(spec, old, 1), "scratch", "/tmp/old")
			changed := old
			changed.Hooks = []LifecycleHook{
				eventHook("new", "", LifecycleCreatePre, LifecycleCreatePost, LifecycleStopPre),
				scratchHook("", ""),
			}

			res, err := s.Create(t.Context(), stepSpec(changed), CreateOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(res.Actions), 1)
			assert.Equal(t, res.Actions[0].Action, "recreate")
			assert.NilError(t, res.Actions[0].Err)
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(old, 1)), tc.wantTrace)
			value, _ := envValue(releaseEnv, ENV_CMDMAN_COMPOSE_RESOURCE_VALUE)
			assert.Equal(t, value, "/tmp/old", "remove_post reads the value of the removed replica")
			holder, ok := f.get(HolderName(replicaName(old, 1), "scratch"))
			assert.Assert(t, ok)
			assert.Equal(t, holder.ConfigJSON.Labels[LabelResourceValue], "/tmp/new")
		})
	}
}

func TestRecreateHookFailureEndsTheRecreate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		state     model.EventType
		failing   string
		wantErr   string
		wantTrace []string
		wantState model.EventType
	}{
		{
			name:      "stop_pre keeps the replica running",
			state:     model.EventTypeRunning,
			failing:   ".stop_pre",
			wantErr:   `hook "old" stop_pre`,
			wantTrace: []string{"old.stop_pre"},
			wantState: model.EventTypeRunning,
		},
		{
			name:      "stop_post keeps the stopped replica",
			state:     model.EventTypeRunning,
			failing:   ".stop_post",
			wantErr:   `hook "old" stop_post`,
			wantTrace: []string{"old.stop_pre", "stop", "old.stop_post"},
			wantState: model.EventTypeExited,
		},
		{
			name:      "remove_pre keeps the replica",
			state:     model.EventTypeExited,
			failing:   ".remove_pre",
			wantErr:   `hook "old" remove_pre`,
			wantTrace: []string{"old.remove_pre"},
			wantState: model.EventTypeExited,
		},
		{
			name:      "remove_post creates no new replica",
			state:     model.EventTypeExited,
			failing:   ".remove_post",
			wantErr:   `hook "old" remove_post`,
			wantTrace: []string{"old.remove_pre", "remove", "old.remove_post"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			old := stepCommand("web", 1, eventHook("old", "",
				LifecycleStopPre, LifecycleStopPost, LifecycleRemovePre, LifecycleRemovePost))
			spec := stepSpec(old)
			putReplica(t, f, spec, old, 1, tc.state)
			changed := old
			changed.Hooks = []LifecycleHook{eventHook("new", "", LifecycleCreatePre)}

			res, err := f.service(nil).Create(t.Context(), stepSpec(changed), CreateOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(res.Actions), 1)
			assert.ErrorContains(t, res.Actions[0].Err, tc.wantErr)
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(old, 1)), tc.wantTrace)
			replica, ok := f.get(replicaName(old, 1))
			if tc.wantState == "" {
				assert.Assert(t, !ok, "no replica should be left")
				return
			}
			assert.Assert(t, ok, "the replica should be kept")
			assert.Equal(t, replica.State, tc.wantState)
		})
	}
}

func TestExcessReplicaRunsStoredStopAndRemoveHooks(t *testing.T) {
	f := newFakeCmdman()
	nc := stepCommand("web", 2, eventHook("mark", "", lifecycleEvents[:]...))
	spec := stepSpec(nc)
	putReplica(t, f, spec, nc, 1, model.EventTypeRunning)
	putReplica(t, f, spec, nc, 2, model.EventTypeRunning)
	scaled := nc
	scaled.Scale = 1

	res, err := f.service(nil).Create(t.Context(), stepSpec(scaled), CreateOption{})

	assert.NilError(t, err)
	for _, a := range res.Actions {
		assert.NilError(t, a.Err, a.Command)
	}
	assert.DeepEqual(t, lifecycleTrace(f, replicaName(nc, 2)), []string{
		"mark.stop_pre", "stop", "mark.stop_post", "mark.remove_pre", "remove", "mark.remove_post",
	})
	assert.Equal(t, len(lifecycleTrace(f, replicaName(nc, 1))), 0)
	_, left := f.get(replicaName(nc, 2))
	assert.Assert(t, !left)
}

func TestExcessReplicaPreHookFailureKeepsIt(t *testing.T) {
	for _, tc := range []struct {
		state     model.EventType
		failing   string
		wantTrace []string
	}{
		{model.EventTypeRunning, ".stop_pre", []string{"mark.stop_pre"}},
		{model.EventTypeExited, ".remove_pre", []string{"mark.remove_pre"}},
	} {
		t.Run(tc.failing, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			nc := stepCommand("web", 2, eventHook("mark", "", lifecycleEvents[:]...))
			spec := stepSpec(nc)
			putReplica(t, f, spec, nc, 1, model.EventTypeExited)
			putReplica(t, f, spec, nc, 2, tc.state)
			scaled := nc
			scaled.Scale = 1

			res, err := f.service(nil).Create(t.Context(), stepSpec(scaled), CreateOption{})

			assert.NilError(t, err)
			i := slices.IndexFunc(res.Actions, func(a ActionOutcome) bool {
				return a.Action == "remove-excess"
			})
			assert.Assert(t, i >= 0, "no remove-excess outcome in %v", res.Actions)
			assert.ErrorContains(t, res.Actions[i].Err, `hook "mark"`)
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(nc, 2)), tc.wantTrace)
			replica, ok := f.get(replicaName(nc, 2))
			assert.Assert(t, ok, "the replica should be kept")
			assert.Equal(t, replica.State, tc.state)
		})
	}
}

func TestRemoveOrphanRunsStoredRemoveHooks(t *testing.T) {
	f := newFakeCmdman()
	keep := stepCommand("keep", 1)
	gone := stepCommand("gone", 1, eventHook("mark", "", lifecycleEvents[:]...))
	live := stepCommand("live", 1, eventHook("mark", "", lifecycleEvents[:]...))
	spec := stepSpec(keep, gone, live)
	putReplica(t, f, spec, keep, 1, model.EventTypeExited)
	putReplica(t, f, spec, gone, 1, model.EventTypeExited)
	putReplica(t, f, spec, live, 1, model.EventTypeRunning)

	res, err := f.service(nil).Create(
		t.Context(), stepSpec(keep), CreateOption{RemoveOrphan: true})

	assert.NilError(t, err)
	outcomes := map[string]ActionOutcome{}
	for _, a := range res.Actions {
		outcomes[a.Command] = a
	}
	assert.Equal(t, outcomes["gone"].Action, "remove-orphan")
	assert.NilError(t, outcomes["gone"].Err)
	assert.Equal(t, outcomes["live"].Action, "skipped")
	assert.DeepEqual(t, lifecycleTrace(f, replicaName(gone, 1)),
		[]string{"mark.remove_pre", "remove", "mark.remove_post"})
	assert.Equal(t, len(lifecycleTrace(f, replicaName(live, 1))), 0)
}

func TestRemoveOrphanPreHookFailureKeepsIt(t *testing.T) {
	f := newFakeCmdman()
	failExec(f, ".remove_pre")
	keep := stepCommand("keep", 1)
	gone := stepCommand("gone", 1, eventHook("mark", "", LifecycleRemovePre))
	spec := stepSpec(keep, gone)
	putReplica(t, f, spec, keep, 1, model.EventTypeExited)
	putReplica(t, f, spec, gone, 1, model.EventTypeExited)

	res, err := f.service(nil).Create(
		t.Context(), stepSpec(keep), CreateOption{RemoveOrphan: true})

	assert.NilError(t, err)
	i := slices.IndexFunc(res.Actions, func(a ActionOutcome) bool { return a.Command == "gone" })
	assert.Assert(t, i >= 0)
	assert.ErrorContains(t, res.Actions[i].Err, `hook "mark" remove_pre`)
	_, kept := f.get(replicaName(gone, 1))
	assert.Assert(t, kept)
}

// putExec stores the exec command that runs ev of hook mark for r, left in
// state, and returns its name.
func putExec(f *fakeCmdman, r hookReplica, ev LifecycleEvent, state model.EventType) string {
	name := ExecCommandName(r.Name, "mark", ev)
	f.put(cmdman.CreateRequest{
		Name:   name,
		Argv:   []string{"true"},
		Labels: execLabels(r, "mark", ev),
	}, state)
	return name
}

// execsOf returns the names of the exec commands owned by the replica named
// replica.
func execsOf(t *testing.T, f *fakeCmdman, replica string) []string {
	t.Helper()
	entries, err := f.list(t.Context(), cmdman.ListRequest{Labels: map[string]string{
		LabelIntermediate: IntermediateExec,
		LabelOwner:        replica,
	}})
	assert.NilError(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func actionErrs(actions []ActionOutcome) error {
	var errs []error
	for _, a := range actions {
		errs = append(errs, a.Err)
	}
	return errors.Join(errs...)
}

func TestRemovedReplicaTakesTheExecsItsFailedHooksLeft(t *testing.T) {
	hooks := []LifecycleHook{
		eventHook("mark", "", LifecycleRemovePre, LifecycleRemovePost),
		scratchHook("", ""),
	}
	web := stepCommand("web", 2, hooks...)
	keep := stepCommand("keep", 1)
	for _, tc := range []struct {
		name string
		// idx is the replica of web the operation removes.
		idx int
		// remove runs the operation and returns the failures it reports.
		remove func(t *testing.T, s *Service) error
	}{
		{
			name: "recreate",
			idx:  1,
			remove: func(t *testing.T, s *Service) error {
				changed := web
				changed.Hooks = append(
					[]LifecycleHook{eventHook("new", "", LifecycleCreatePre)}, hooks...)
				res, err := s.Create(t.Context(), stepSpec(changed), CreateOption{})
				assert.NilError(t, err)
				return actionErrs(res.Actions)
			},
		},
		{
			name: "scale-down surplus",
			idx:  2,
			remove: func(t *testing.T, s *Service) error {
				scaled := web
				scaled.Scale = 1
				res, err := s.Create(t.Context(), stepSpec(scaled), CreateOption{})
				assert.NilError(t, err)
				return actionErrs(res.Actions)
			},
		},
		{
			name: "remove orphan",
			idx:  1,
			remove: func(t *testing.T, s *Service) error {
				res, err := s.Create(
					t.Context(), stepSpec(keep), CreateOption{RemoveOrphan: true})
				assert.NilError(t, err)
				return actionErrs(res.Actions)
			},
		},
		{
			name: "down",
			idx:  1,
			remove: func(t *testing.T, s *Service) error {
				res, err := s.Down(t.Context(), storedSelection(), DownOption{})
				assert.NilError(t, err)
				var errs []error
				for _, o := range res.Removes {
					errs = append(errs, o.Err)
				}
				return errors.Join(errs...)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, ".mark.remove_post")
			spec := stepSpec(web, keep)
			for idx := 1; idx <= 2; idx++ {
				putReplica(t, f, spec, web, idx, model.EventTypeExited)
			}
			putReplica(t, f, spec, keep, 1, model.EventTypeExited)
			s := f.service(nil)
			r := s.specHookReplica(spec, web, tc.idx)
			putExec(f, r, LifecycleStartPre, model.EventTypeExited)
			running := putExec(f, r, LifecycleStartPost, model.EventTypeRunning)
			putTestHolder(f, r, "scratch", "/tmp/scratch")

			err := tc.remove(t, s)

			assert.ErrorContains(t, err, `hook "mark" remove_post`)
			_, left := f.get(r.Name)
			assert.Assert(t, !left, "the replica should be removed")
			assert.DeepEqual(t, execsOf(t, f, r.Name), []string{running})
			value, held := holderValue(t, f, r, "scratch")
			assert.Assert(t, held, "the holder outlives its replica")
			assert.Equal(t, value, "/tmp/scratch")
		})
	}
}

func TestUpRunsStartHooksAroundEachStartedReplica(t *testing.T) {
	f := newFakeCmdman()
	nc := stepCommand("web", 2, eventHook("mark", "", lifecycleEvents[:]...))
	spec := stepSpec(nc)
	// Replica 1 is already up to date and running, so nothing runs for it.
	putReplica(t, f, spec, nc, 1, model.EventTypeRunning)

	res, err := f.service(nil).Up(t.Context(), spec, UpOption{})

	assert.NilError(t, err)
	for _, s := range res.Starts {
		assert.NilError(t, s.Err, s.Command)
	}
	assert.Equal(t, len(lifecycleTrace(f, replicaName(nc, 1))), 0)
	assert.DeepEqual(t, lifecycleTrace(f, replicaName(nc, 2)), []string{
		"mark.create_pre", "create", "mark.create_post",
		"mark.start_pre", "start", "mark.start_post",
	})
}

func TestUpStartHookFailureFailsTheStart(t *testing.T) {
	for _, tc := range []struct {
		failing   string
		onError   OnError
		wantErr   string
		wantTrace []string
	}{
		{
			failing:   ".start_pre",
			wantErr:   `hook "mark" start_pre`,
			wantTrace: []string{"create", "mark.start_pre"},
		},
		{
			failing:   ".start_post",
			wantErr:   `hook "mark" start_post`,
			wantTrace: []string{"create", "mark.start_pre", "start", "mark.start_post"},
		},
		{
			failing:   ".start_pre",
			onError:   OnErrorIgnore,
			wantTrace: []string{"create", "mark.start_pre", "start", "mark.start_post"},
		},
	} {
		t.Run(tc.failing+"/"+string(tc.onError), func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			a := stepCommand("a", 1,
				eventHook("mark", tc.onError, LifecycleStartPre, LifecycleStartPost))
			b := stepCommand("b", 1)
			b.After = []AfterSpec{{Name: "a", Condition: ConditionRunning}}

			res, err := f.service(nil).Up(t.Context(), stepSpec(a, b), UpOption{})

			assert.NilError(t, err)
			starts := map[string]error{}
			for _, s := range res.Starts {
				starts[s.Command] = s.Err
			}
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(a, 1)), tc.wantTrace)
			if tc.wantErr == "" {
				assert.NilError(t, starts["a"])
				assert.NilError(t, starts["b"])
				return
			}
			assert.ErrorContains(t, starts["a"], tc.wantErr)
			assert.ErrorContains(t, starts["b"], `dependency "a" failed`)
			assert.Assert(t, !slices.Contains(lifecycleTrace(f, replicaName(b, 1)), "start"),
				"a dependent of a failed start must not start")
			_, kept := f.get(replicaName(a, 1) + ".hook.mark" + tc.failing)
			assert.Assert(t, kept, "the failed exec command is kept")
		})
	}
}

func TestUpHoldsNewReplicaWhoseCreateFailed(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failing     string
		onError     OnError
		wantErr     string
		wantTrace   []string
		wantReplica bool
	}{
		{
			name:        "create_post leaves the replica created",
			failing:     ".create_post",
			wantErr:     `hook "mark" create_post`,
			wantTrace:   []string{"mark.create_pre", "create", "mark.create_post"},
			wantReplica: true,
		},
		{
			name:      "create_pre leaves no replica",
			failing:   ".create_pre",
			wantErr:   `hook "mark" create_pre`,
			wantTrace: []string{"mark.create_pre"},
		},
		{
			name:        "continue starts the replica",
			failing:     ".create_post",
			onError:     OnErrorContinue,
			wantTrace:   []string{"mark.create_pre", "create", "mark.create_post", "start"},
			wantReplica: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			rec := &commandPhaseReporter{}
			web := stepCommand("web", 1,
				eventHook("mark", tc.onError, LifecycleCreatePre, LifecycleCreatePost))
			app := stepCommand("app", 1)
			app.After = []AfterSpec{{Name: "web", Condition: ConditionRunning}}

			res, err := f.service(rec).Up(t.Context(), stepSpec(web, app), UpOption{})

			assert.NilError(t, err)
			starts := map[string]error{}
			for _, s := range res.Starts {
				starts[s.Command] = s.Err
			}
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(web, 1)), tc.wantTrace)
			replica, exists := f.get(replicaName(web, 1))
			assert.Equal(t, exists, tc.wantReplica)
			if tc.wantErr == "" {
				assert.NilError(t, starts["web"])
				assert.NilError(t, starts["app"])
				return
			}
			assert.ErrorContains(t, starts["web"], "not started")
			assert.ErrorContains(t, starts["web"], tc.wantErr)
			assert.ErrorContains(t, starts["app"], `dependency "web" failed`)
			assert.Assert(t, rec.reached("web", PhaseSkipped))
			assert.Assert(t, !slices.Contains(lifecycleTrace(f, replicaName(app, 1)), "start"),
				"a dependent of a held replica must not start")
			if tc.wantReplica {
				assert.Equal(t, replica.State, model.EventTypeCreated)
			}
		})
	}
}

func TestStartTakesHooksFromSpecOrStoredReplica(t *testing.T) {
	// Each replica stores hooks of its own, so a start that decodes the label
	// once per command runs one replica's hooks for the other.
	one := stepCommand("web", 2, eventHook("one", "", LifecycleStartPre, LifecycleStartPost))
	two := stepCommand("web", 2, eventHook("two", "", LifecycleStartPre, LifecycleStartPost))
	fromFile := stepCommand("web", 2,
		eventHook("file", "", LifecycleStartPre, LifecycleStartPost))
	seed := func(t *testing.T) *fakeCmdman {
		f := newFakeCmdman()
		putReplica(t, f, stepSpec(one), one, 1, model.EventTypeExited)
		putReplica(t, f, stepSpec(two), two, 2, model.EventTypeExited)
		return f
	}

	t.Run("stored", func(t *testing.T) {
		f := seed(t)
		res, err := f.service(nil).Start(t.Context(),
			ProjectSelection{WorkDir: "/wd", Project: "proj"}, StartOption{})
		assert.NilError(t, err)
		for _, s := range res.Starts {
			assert.NilError(t, s.Err, s.Command)
		}
		assert.DeepEqual(t, lifecycleTrace(f, replicaName(one, 1)),
			[]string{"one.start_pre", "start", "one.start_post"})
		assert.DeepEqual(t, lifecycleTrace(f, replicaName(two, 2)),
			[]string{"two.start_pre", "start", "two.start_post"})
	})

	t.Run("spec", func(t *testing.T) {
		f := seed(t)
		spec := stepSpec(fromFile)
		res, err := f.service(nil).Start(t.Context(), SelectionFromSpec(&spec), StartOption{})
		assert.NilError(t, err)
		for _, s := range res.Starts {
			assert.NilError(t, s.Err, s.Command)
		}
		for idx := 1; idx <= 2; idx++ {
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(fromFile, idx)),
				[]string{"file.start_pre", "start", "file.start_post"})
		}
	})
}

func TestRestartRunsStoredStopHooksThenStartHooks(t *testing.T) {
	stored := stepCommand("web", 1, eventHook("stored", "",
		LifecycleStopPre, LifecycleStopPost, LifecycleStartPre, LifecycleStartPost))
	fromFile := stepCommand("web", 1, eventHook("file", "",
		LifecycleStopPre, LifecycleStopPost, LifecycleStartPre, LifecycleStartPost))
	fileSpec := stepSpec(fromFile)
	for _, tc := range []struct {
		name      string
		selection ProjectSelection
		state     model.EventType
		wantTrace []string
	}{
		{
			name:      "spec",
			selection: SelectionFromSpec(&fileSpec),
			state:     model.EventTypeRunning,
			wantTrace: []string{
				"stored.stop_pre", "stop", "stored.stop_post",
				"file.start_pre", "start", "file.start_post",
			},
		},
		{
			name:      "stored",
			selection: ProjectSelection{WorkDir: "/wd", Project: "proj"},
			state:     model.EventTypeRunning,
			wantTrace: []string{
				"stored.stop_pre", "stop", "stored.stop_post",
				"stored.start_pre", "start", "stored.start_post",
			},
		},
		{
			name:      "not running",
			selection: ProjectSelection{WorkDir: "/wd", Project: "proj"},
			state:     model.EventTypeExited,
			wantTrace: []string{"stop", "stored.start_pre", "start", "stored.start_post"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCmdman()
			putReplica(t, f, stepSpec(stored), stored, 1, tc.state)

			res, err := f.service(nil).Restart(t.Context(), tc.selection, RestartOption{})

			assert.NilError(t, err)
			for _, r := range res.Restarts {
				assert.NilError(t, r.StopErr, r.Command)
				assert.NilError(t, r.StartErr, r.Command)
			}
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(stored, 1)), tc.wantTrace)
		})
	}
}

func TestRestartHookFailure(t *testing.T) {
	for _, tc := range []struct {
		failing      string
		wantStopErr  string
		wantStartErr string
		wantTrace    []string
	}{
		{
			failing:     ".stop_pre",
			wantStopErr: `hook "mark" stop_pre`,
			wantTrace:   []string{"mark.stop_pre"},
		},
		{
			failing:     ".stop_post",
			wantStopErr: `hook "mark" stop_post`,
			wantTrace:   []string{"mark.stop_pre", "stop", "mark.stop_post"},
		},
		{
			failing:      ".start_pre",
			wantStartErr: `hook "mark" start_pre`,
			wantTrace:    []string{"mark.stop_pre", "stop", "mark.stop_post", "mark.start_pre"},
		},
	} {
		t.Run(tc.failing, func(t *testing.T) {
			f := newFakeCmdman()
			failExec(f, tc.failing)
			nc := stepCommand("web", 1, eventHook("mark", "", lifecycleEvents[:]...))
			spec := stepSpec(nc)
			putReplica(t, f, spec, nc, 1, model.EventTypeRunning)

			res, err := f.service(nil).Restart(
				t.Context(), SelectionFromSpec(&spec), RestartOption{})

			assert.NilError(t, err)
			assert.Equal(t, len(res.Restarts), 1)
			o := res.Restarts[0]
			if tc.wantStopErr != "" {
				assert.ErrorContains(t, o.StopErr, tc.wantStopErr)
			} else {
				assert.NilError(t, o.StopErr)
			}
			if tc.wantStartErr != "" {
				assert.ErrorContains(t, o.StartErr, tc.wantStartErr)
			} else {
				assert.NilError(t, o.StartErr)
			}
			assert.DeepEqual(t, lifecycleTrace(f, replicaName(nc, 1)), tc.wantTrace)
		})
	}
}
