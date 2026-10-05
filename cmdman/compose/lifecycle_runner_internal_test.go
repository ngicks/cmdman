package compose

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
)

// testHookReplica is replica 1 of command web in project proj, named the way
// compose names it.
func testHookReplica() hookReplica {
	ref := resourceRef{Project: "proj", WorkDir: "/wd", Command: "web", ScaleIndex: 1}
	return hookReplica{
		Project:    "proj",
		WorkDir:    "/wd",
		Command:    "web",
		ScaleIndex: 1,
		Scale:      1,
		Name:       ref.replicaName(),
		Display:    "web",
		Dir:        "/wd/web",
		Env:        []string{"REPLICA_VAR=replica"},
	}
}

// scratchHook acquires resource scratch at create_pre and releases it at
// remove_post, each with on_error.
func scratchHook(acquireOnError, releaseOnError OnError) LifecycleHook {
	return LifecycleHook{
		Name:     "scratch",
		Resource: "scratch",
		Events: map[LifecycleEvent]LifecycleExec{
			LifecycleCreatePre:  {Args: []string{"mktemp", "-d"}, OnError: acquireOnError},
			LifecycleRemovePost: {Args: []string{"rm", "-rf"}, OnError: releaseOnError},
		},
	}
}

// putTestHolder stores a holder of resource key for r with value.
func putTestHolder(f *fakeCmdman, r hookReplica, key, value string) {
	req, err := resourceHolder{
		Ref:   r.resourceRef(key),
		Owner: r.Name,
		Value: value,
		Dir:   r.Dir,
		Env:   []string{"HOLDER_VAR=holder"},
	}.createRequest()
	if err != nil {
		panic(err)
	}
	f.put(req, model.EventTypeCreated)
}

func holderValue(t *testing.T, f *fakeCmdman, r hookReplica, key string) (string, bool) {
	t.Helper()
	e, ok := f.get(HolderName(r.Name, key))
	if !ok {
		return "", false
	}
	return e.ConfigJSON.Labels[LabelResourceValue], true
}

// hookPhases returns the phases of the hook events recorded for hook.
func hookPhases(rec *commandPhaseReporter, hook string) []Phase {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []Phase
	for _, ev := range rec.events {
		if ev.Hook == hook {
			out = append(out, ev.Phase)
		}
	}
	return out
}

func envValue(env []string, key string) (string, bool) {
	for _, kv := range slices.Backward(env) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v, true
		}
	}
	return "", false
}

func TestRunLifecycleEventAcquireStoresValue(t *testing.T) {
	f := newFakeCmdman()
	f.cfg.ConfigPath = "/etc/cmdman.json"
	f.run = func(string, cmdman.CreateRequest) fakeRun {
		return fakeRun{
			exit:   new(0),
			stdout: []string{"creating", "  /tmp/scratch.1  ", "", "   "},
			stderr: []string{"warning: noisy"},
		}
	}
	rec := &commandPhaseReporter{}
	s := f.service(rec)
	r := testHookReplica()
	exec := ExecCommandName(r.Name, "scratch", LifecycleCreatePre)

	warnings, err := s.runLifecycleEvent(
		t.Context(), r, []LifecycleHook{scratchHook("", "")}, LifecycleCreatePre)

	assert.NilError(t, err)
	assert.Equal(t, len(warnings), 0)

	value, ok := holderValue(t, f, r, "scratch")
	assert.Assert(t, ok, "holder should exist")
	assert.Equal(t, value, "/tmp/scratch.1")

	holder, _ := f.get(HolderName(r.Name, "scratch"))
	labels := holder.ConfigJSON.Labels
	assert.Equal(t, labels[LabelIntermediate], IntermediateHolder)
	assert.Equal(t, labels[LabelOwner], r.Name)
	assert.Equal(t, labels[LabelResourceCommand], "web")
	assert.Equal(t, labels[LabelResourceScaleIndex], "1")
	var release resourceRelease
	assert.NilError(t, json.Unmarshal([]byte(labels[LabelResourceRelease]), &release))
	assert.DeepEqual(t, release, resourceRelease{
		Event: LifecycleRemovePost, Args: []string{"rm", "-rf"}, OnError: OnErrorFail,
	})
	holderReq := f.request(HolderName(r.Name, "scratch"))
	assert.DeepEqual(t, holderReq.Argv, []string{"true"})
	assert.Equal(t, holderReq.LogDriver, logdriver.DriverNone)
	assert.Equal(t, holderReq.Dir, r.Dir)
	event, _ := envValue(holderReq.Env, ENV_CMDMAN_COMPOSE_HOOK_EVENT)
	assert.Equal(t, event, string(LifecycleRemovePost), "holder env is the release hook's")

	_, execLeft := f.get(exec)
	assert.Assert(t, !execLeft, "a successful exec command is removed")

	assert.DeepEqual(t, hookPhases(rec, "scratch"), []Phase{
		PhaseHookRunning, PhaseHookOutput, PhaseHookOutput, PhaseHookOutput,
		PhaseHookOutput, PhaseHookOutput, PhaseHookSucceeded,
	})
}

func TestRunLifecycleEventExecRequest(t *testing.T) {
	f := newFakeCmdman()
	f.cfg.ConfigPath = "/etc/cmdman.json"
	var got cmdman.CreateRequest
	f.run = func(_ string, req cmdman.CreateRequest) fakeRun {
		got = req
		return fakeRun{exit: new(0)}
	}
	s := f.service(nil)
	r := testHookReplica()
	putTestHolder(f, r, "scratch", "/tmp/old")

	_, err := s.runLifecycleEvent(
		t.Context(), r, []LifecycleHook{scratchHook("", "")}, LifecycleRemovePost)
	assert.NilError(t, err)

	assert.Equal(t, got.Name, ExecCommandName(r.Name, "scratch", LifecycleRemovePost))
	assert.Equal(t, got.Dir, r.Dir)
	assert.DeepEqual(t, got.Argv, []string{"rm", "-rf"})
	assert.Equal(t, got.LogDriver, logdriver.DriverK8sFile)
	assert.Equal(t, got.RestartPolicy, model.RestartPolicyNo)
	assert.Assert(t, got.ImportHostEnv != nil && !*got.ImportHostEnv)
	assert.DeepEqual(t, got.Labels, map[string]string{
		LabelIntermediate: IntermediateExec,
		LabelOwner:        r.Name,
		LabelHooksProject: "proj",
		LabelHooksWorkdir: "/wd",
		LabelHook:         "scratch",
		LabelHookEvent:    string(LifecycleRemovePost),
	})
	for key, want := range map[string]string{
		"REPLICA_VAR":                     "replica",
		ENV_CMDMAN_COMPOSE_PROJECT:        "proj",
		ENV_CMDMAN_COMPOSE_WORK_DIR:       "/wd",
		ENV_CMDMAN_COMPOSE_WORK_DIR_HASH:  workdirHash("/wd"),
		ENV_CMDMAN_COMPOSE_COMMAND:        "web",
		ENV_CMDMAN_COMPOSE_SCALE_INDEX:    "1",
		ENV_CMDMAN_COMPOSE_SCALE:          "1",
		ENV_CMDMAN_COMPOSE_HOOK_NAME:      "scratch",
		ENV_CMDMAN_COMPOSE_HOOK_EVENT:     string(LifecycleRemovePost),
		ENV_CMDMAN_COMPOSE_RESOURCE_KEY:   "scratch",
		ENV_CMDMAN_COMPOSE_RESOURCE_VALUE: "/tmp/old",
		cmdman.ENV_CMDMAN_DATA_DIR:        "/data",
		cmdman.ENV_CMDMAN_RUNTIME_DIR:     "/run",
		cmdman.ENV_CMDMAN_CONF:            "/etc/cmdman.json",
	} {
		v, ok := envValue(got.Env, key)
		assert.Assert(t, ok, "env %s is missing", key)
		assert.Equal(t, v, want, "env %s", key)
	}
	_, hostImported := envValue(got.Env, "HOST_VAR")
	assert.Assert(t, !hostImported, "the replica env is the base, not the host env")

	_, held := holderValue(t, f, r, "scratch")
	assert.Assert(t, !held, "a successful release removes the holder")
}

func TestRunLifecycleEventNonZeroExitKeepsExec(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun {
		return fakeRun{exit: new(3), stdout: []string{"/tmp/new"}}
	}
	rec := &commandPhaseReporter{}
	s := f.service(rec)
	r := testHookReplica()
	putTestHolder(f, r, "scratch", "/tmp/old")
	exec := ExecCommandName(r.Name, "scratch", LifecycleCreatePre)

	_, err := s.runLifecycleEvent(
		t.Context(), r, []LifecycleHook{scratchHook("", "")}, LifecycleCreatePre)

	assert.ErrorContains(t, err, "exited with code 3")
	kept, ok := f.get(exec)
	assert.Assert(t, ok, "a failed exec command is kept for inspection")
	assert.Equal(t, kept.State, model.EventTypeExited)
	value, _ := holderValue(t, f, r, "scratch")
	assert.Equal(t, value, "/tmp/old", "a failed acquire leaves the holder alone")
	phases := hookPhases(rec, "scratch")
	assert.Equal(t, phases[len(phases)-1], PhaseHookFailed)
}

func TestRunLifecycleEventNilExitCodeFails(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun { return fakeRun{exit: nil} }
	s := f.service(nil)
	r := testHookReplica()

	_, err := s.runLifecycleEvent(
		t.Context(), r, []LifecycleHook{scratchHook("", "")}, LifecycleCreatePre)

	assert.ErrorContains(t, err, "ended without an exit code")
	_, held := holderValue(t, f, r, "scratch")
	assert.Assert(t, !held)
	_, kept := f.get(ExecCommandName(r.Name, "scratch", LifecycleCreatePre))
	assert.Assert(t, kept)
}

func TestRunLifecycleEventClearsStaleExec(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	r := testHookReplica()
	exec := ExecCommandName(r.Name, "scratch", LifecycleCreatePre)
	staleID := f.put(cmdman.CreateRequest{
		Name:   exec,
		Argv:   []string{"sleep", "300"},
		Labels: execLabels(r, "scratch", LifecycleCreatePre),
	}, model.EventTypeRunning)

	_, err := s.runLifecycleEvent(
		t.Context(), r, []LifecycleHook{scratchHook("", "")}, LifecycleCreatePre)
	assert.NilError(t, err)

	calls := f.callLog()
	stop := slices.Index(calls, "stop "+exec)
	remove := slices.Index(calls, "remove "+exec)
	create := slices.Index(calls, "create "+exec)
	assert.Assert(t, stop >= 0 && stop < remove && remove < create,
		"want stop, remove, then create of %s; calls %v", exec, calls)
	_, staleLeft := f.get(staleID)
	assert.Assert(t, !staleLeft)
}

func TestRunLifecycleEventClearsStaleExecThatNeverStarted(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	r := testHookReplica()
	exec := ExecCommandName(r.Name, "scratch", LifecycleCreatePre)
	staleID := f.put(cmdman.CreateRequest{
		Name:   exec,
		Argv:   []string{"mktemp", "-d"},
		Labels: execLabels(r, "scratch", LifecycleCreatePre),
	}, model.EventTypeCreated)

	_, err := s.runLifecycleEvent(
		t.Context(), r, []LifecycleHook{scratchHook("", "")}, LifecycleCreatePre)
	assert.NilError(t, err)

	calls := f.callLog()
	assert.Assert(t, !slices.Contains(calls, "stop "+exec),
		"a command that never started has no monitor to stop; calls %v", calls)
	remove := slices.Index(calls, "remove "+exec)
	create := slices.Index(calls, "create "+exec)
	start := slices.Index(calls, "start "+exec)
	assert.Assert(t, remove >= 0 && remove < create && create < start,
		"want remove, create, then start of %s; calls %v", exec, calls)
	_, staleLeft := f.get(staleID)
	assert.Assert(t, !staleLeft)
	_, held := holderValue(t, f, r, "scratch")
	assert.Assert(t, held, "the hook ran and acquired the resource")
}

func TestRunLifecycleEventCancelledStartStopsNoUnstartedExec(t *testing.T) {
	f := newFakeCmdman()
	ctx, cancel := context.WithCancel(t.Context())
	f.startErr = func(string) error {
		cancel()
		return context.Canceled
	}
	s := f.service(nil)
	r := testHookReplica()
	exec := ExecCommandName(r.Name, "scratch", LifecycleCreatePre)

	_, err := s.runLifecycleEvent(
		ctx, r, []LifecycleHook{scratchHook("", "")}, LifecycleCreatePre)

	assert.Assert(t, errors.Is(err, context.Canceled), "got %v", err)
	assert.Assert(t, !slices.Contains(f.callLog(), "stop "+exec),
		"calls %v", f.callLog())
	left, ok := f.get(exec)
	assert.Assert(t, ok)
	assert.Equal(t, left.State, model.EventTypeCreated)
}

func TestRunLifecycleEventCancellationStopsExec(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun { return fakeRun{block: true} }
	ctx, cancel := context.WithCancel(t.Context())
	f.started = func(string) { cancel() }
	rec := &commandPhaseReporter{}
	s := f.service(rec)
	r := testHookReplica()
	exec := ExecCommandName(r.Name, "scratch", LifecycleCreatePre)

	// ignore must not swallow a cancellation.
	_, err := s.runLifecycleEvent(
		ctx, r, []LifecycleHook{scratchHook(OnErrorIgnore, "")}, LifecycleCreatePre)

	assert.Assert(t, errors.Is(err, context.Canceled), "got %v", err)
	assert.Assert(t, slices.Contains(f.callLog(), "stop "+exec))
	stopped, ok := f.get(exec)
	assert.Assert(t, ok)
	assert.Assert(t, stopped.State != model.EventTypeRunning)
	phases := hookPhases(rec, "scratch")
	assert.Equal(t, phases[len(phases)-1], PhaseHookFailed)
}

func TestRunLifecycleEventOnErrorContinue(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun { return fakeRun{exit: new(1)} }
	rec := &commandPhaseReporter{}
	s := f.service(rec)
	r := testHookReplica()
	putTestHolder(f, r, "scratch", "/tmp/old")

	warnings, err := s.runLifecycleEvent(
		t.Context(), r,
		[]LifecycleHook{scratchHook("", OnErrorContinue)},
		LifecycleRemovePost,
	)

	assert.NilError(t, err)
	assert.Equal(t, len(warnings), 1)
	assert.ErrorContains(t, warnings[0], "exited with code 1")
	value, ok := holderValue(t, f, r, "scratch")
	assert.Assert(t, ok, "a failed release under continue keeps the holder")
	assert.Equal(t, value, "/tmp/old")
	phases := hookPhases(rec, "scratch")
	assert.Equal(t, phases[len(phases)-1], PhaseHookWarning)
}

func TestRunLifecycleEventOnErrorIgnore(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun { return fakeRun{exit: new(1)} }
	rec := &commandPhaseReporter{}
	s := f.service(rec)
	r := testHookReplica()
	putTestHolder(f, r, "scratch", "/tmp/old")

	warnings, err := s.runLifecycleEvent(
		t.Context(), r,
		[]LifecycleHook{scratchHook("", OnErrorIgnore)},
		LifecycleRemovePost,
	)

	assert.NilError(t, err)
	assert.Equal(t, len(warnings), 0)
	_, held := holderValue(t, f, r, "scratch")
	assert.Assert(t, !held, "a failed release under ignore still drops the holder")
	assert.DeepEqual(t, hookPhases(rec, "scratch"),
		[]Phase{PhaseHookRunning, PhaseHookIgnored})
	rec.mu.Lock()
	ignored := rec.events[len(rec.events)-1]
	rec.mu.Unlock()
	assert.ErrorContains(t, ignored.Err, "exited with code 1")
	assert.Assert(t, ignored.ExitCode != nil && *ignored.ExitCode == 1)
}

func TestPhaseHookIgnoredEndsTheRunWithoutFailing(t *testing.T) {
	assert.Assert(t, PhaseHookIgnored.Terminal())
	assert.Assert(t, !PhaseHookIgnored.Failed())
}

func TestRunLifecycleEventFailStopsTheWalk(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(name string, _ cmdman.CreateRequest) fakeRun {
		if strings.Contains(name, ".first.") {
			return fakeRun{exit: new(1)}
		}
		return fakeRun{exit: new(0)}
	}
	s := f.service(nil)
	r := testHookReplica()
	hooks := []LifecycleHook{
		{Name: "first", Events: map[LifecycleEvent]LifecycleExec{
			LifecycleStartPost: {Args: []string{"false"}},
		}},
		{Name: "second", Events: map[LifecycleEvent]LifecycleExec{
			LifecycleStartPost: {Args: []string{"true"}},
		}},
	}

	_, err := s.runLifecycleEvent(t.Context(), r, hooks, LifecycleStartPost)

	assert.ErrorContains(t, err, `hook "first" start_post of web`)
	second := ExecCommandName(r.Name, "second", LifecycleStartPost)
	assert.Assert(t, !slices.Contains(f.callLog(), "create "+second))
}

func TestRunLifecycleEventFailedHolderReplacementKeepsOldValue(t *testing.T) {
	f := newFakeCmdman()
	f.run = func(string, cmdman.CreateRequest) fakeRun {
		return fakeRun{exit: new(0), stdout: []string{"/tmp/new"}}
	}
	r := testHookReplica()
	holder := HolderName(r.Name, "scratch")
	f.createErr = func(req cmdman.CreateRequest) error {
		if req.Name == holder {
			return errors.New("disk full")
		}
		return nil
	}
	s := f.service(nil)
	putTestHolder(f, r, "scratch", "/tmp/old")

	_, err := s.runLifecycleEvent(
		t.Context(), r, []LifecycleHook{scratchHook("", "")}, LifecycleCreatePre)

	assert.ErrorContains(t, err, "disk full")
	value, _ := holderValue(t, f, r, "scratch")
	assert.Equal(t, value, "/tmp/old")
	_, kept := f.get(ExecCommandName(r.Name, "scratch", LifecycleCreatePre))
	assert.Assert(t, kept, "the exec command keeps the value that could not be stored")
}

func TestRunLifecycleEventSkipsHooksWithoutTheEvent(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)

	warnings, err := s.runLifecycleEvent(
		t.Context(), testHookReplica(), []LifecycleHook{scratchHook("", "")}, LifecycleStartPre)

	assert.NilError(t, err)
	assert.Equal(t, len(warnings), 0)
	assert.Equal(t, len(f.callLog()), 0)
}

func TestSpecHookReplica(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	spec := ComposeSpec{Project: "proj", WorkDir: "/wd"}
	nc := Command{
		Name:          "web",
		Env:           []string{"A=1"},
		ImportHostEnv: true,
		Scale:         2,
		GeneratedName: GenerateName(workdirHash("/wd"), "proj", "web"),
	}

	r := s.specHookReplica(spec, nc, 2)

	assert.Equal(t, r.Name, nc.GeneratedName+"-2")
	assert.Equal(t, r.Display, "web-2")
	assert.Equal(t, r.Dir, "/default-wd")
	assert.DeepEqual(t, r.Env, slices.Concat(
		[]string{"HOST_VAR=host", "A=1"},
		composeContextEnv("proj", "/wd", "web", 2, 2),
	))

	nc.ImportHostEnv = false
	nc.Dir = "/wd/web"
	r = s.specHookReplica(spec, nc, 1)
	assert.Equal(t, r.Dir, "/wd/web")
	assert.Assert(t, !slices.Contains(r.Env, "HOST_VAR=host"))
}

func TestStoredHookReplica(t *testing.T) {
	hooks := []LifecycleHook{scratchHook("", "")}
	spec := ComposeSpec{Project: "proj", WorkDir: "/wd", ComposeFile: "/wd/cmd-compose.yaml"}
	nc := Command{Name: "web", Scale: 3, Hooks: hooks}
	e := cmdmanEntry{
		ID:   "id",
		Name: "abc-proj-web-3",
		ConfigJSON: &model.CommandConfig{
			Dir:    "/wd/web",
			Env:    []string{"A=1"},
			Labels: BuildLabels(spec, nc, "hash", 3),
		},
	}

	r, got, err := storedHookReplica(e)

	assert.NilError(t, err)
	assert.DeepEqual(t, got, hooks)
	assert.DeepEqual(t, r, hookReplica{
		Project:    "proj",
		WorkDir:    "/wd",
		Command:    "web",
		ScaleIndex: 3,
		Scale:      3,
		Name:       "abc-proj-web-3",
		Display:    "web-3",
		Dir:        "/wd/web",
		Env:        []string{"A=1"},
	})
}

func TestResourceValue(t *testing.T) {
	cases := map[string]struct {
		stdout []string
		want   string
	}{
		"empty":                {nil, ""},
		"all blank":            {[]string{"", "  ", "\t"}, ""},
		"last line":            {[]string{"a", "b"}, "b"},
		"trailing blank lines": {[]string{"a", "  b \r", "", " "}, "b"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, resourceValue(tc.stdout), tc.want)
		})
	}
}

func TestLineAssemblerJoinsPartialRecords(t *testing.T) {
	var a lineAssembler
	_, ok := a.add(
		logdriver.LogLine{Stream: logdriver.StreamStdout, Partial: true, Line: []byte("ab")},
	)
	assert.Assert(t, !ok)
	_, ok = a.add(
		logdriver.LogLine{Stream: logdriver.StreamStderr, Partial: true, Line: []byte("e")},
	)
	assert.Assert(t, !ok)
	line, ok := a.add(logdriver.LogLine{Stream: logdriver.StreamStdout, Line: []byte("c\r\n")})
	assert.Assert(t, ok)
	assert.Equal(t, line, "abc")
	_, ok = a.add(
		logdriver.LogLine{Stream: logdriver.StreamStdout, Partial: true, Line: []byte("d")},
	)
	assert.Assert(t, !ok)

	assert.DeepEqual(t, a.rest(), []logdriver.LogLine{
		{Stream: logdriver.StreamStderr, Line: []byte("e")},
		{Stream: logdriver.StreamStdout, Line: []byte("d")},
	})
	assert.Equal(t, len(a.rest()), 0)
}

func TestHookExit(t *testing.T) {
	_, err := hookExit("x", []cmdman.WaitResult{{ExitCode: new(0)}})
	assert.NilError(t, err)
	_, err = hookExit("x", []cmdman.WaitResult{{ExitCode: new(2)}})
	assert.ErrorContains(t, err, "exited with code 2")
	_, err = hookExit("x", []cmdman.WaitResult{{}})
	assert.ErrorContains(t, err, "ended without an exit code")
	_, err = hookExit("x", []cmdman.WaitResult{{Err: errors.New("boom")}})
	assert.ErrorContains(t, err, "boom")
	_, err = hookExit("x", nil)
	assert.Assert(t, cmp.ErrorContains(err, "got 0 results"))
}
