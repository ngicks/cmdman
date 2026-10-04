package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
	"github.com/ngicks/cmdman/cmdman/store"
)

func TestResolveTargets(t *testing.T) {
	replicas := map[string]int{"a": 3, "b": 1, "none": 0}
	cases := []struct {
		name    string
		targets []Target
		want    targetSet
		wantErr []string
	}{
		{name: "no targets select the whole project"},
		{
			name:    "mixed replicas and whole commands",
			targets: []Target{{"a", 3}, {"a", 1}, {"b", 0}},
			want:    targetSet{"a": {1, 3}, "b": nil},
		},
		{
			name:    "every replica absorbs a single one",
			targets: []Target{{"a", 2}, {"a", 0}},
			want:    targetSet{"a": nil},
		},
		{
			name:    "duplicate replicas merge",
			targets: []Target{{"a", 3}, {"a", 1}, {"a", 3}},
			want:    targetSet{"a": {1, 3}},
		},
		{
			name:    "unknown commands",
			targets: []Target{{"a", 1}, {"y", 2}, {"x", 0}},
			wantErr: []string{"unknown compose command(s): [x y]"},
		},
		{
			name:    "index above the range",
			targets: []Target{{"a", 1}, {"a", 4}},
			wantErr: []string{`"a"`, "replica 4", "1..3"},
		},
		{
			name:    "negative index",
			targets: []Target{{"a", -1}},
			wantErr: []string{`"a"`, "1..3"},
		},
		{
			name:    "command without replicas",
			targets: []Target{{"none", 1}},
			wantErr: []string{`"none"`, "no replicas"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTargets(tc.targets, replicas)
			if len(tc.wantErr) > 0 {
				assert.Assert(t, err != nil)
				for _, want := range tc.wantErr {
					assert.ErrorContains(t, err, want)
				}
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestTargetsOf(t *testing.T) {
	assert.Assert(t, TargetsOf() == nil)
	assert.DeepEqual(t, TargetsOf("a", "b"), []Target{{Command: "a"}, {Command: "b"}})
}

func TestStoredReplicas(t *testing.T) {
	a, b := replicaCmd("a", 3), replicaCmd("b", 1)
	entries := []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeRunning),
		replicaEntry(t, a, 3, model.EventTypeRunning),
		replicaEntry(t, b, 1, model.EventTypeRunning),
	}

	// Without a spec the entries name the known commands.
	assert.DeepEqual(t, storedReplicas(nil, entries), map[string]int{"a": 3, "b": 1})

	// With a spec the spec names them: an undeclared command is unknown and a
	// declared one without replicas has none.
	spec := reconcileSpec(a, replicaCmd("c", 2))
	assert.DeepEqual(t, storedReplicas(&spec, entries), map[string]int{"a": 3, "c": 0})
}

func TestTargetSetFilter(t *testing.T) {
	a, b, c := replicaCmd("a", 3), replicaCmd("b", 1), replicaCmd("c", 1)
	entries := []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeRunning),
		replicaEntry(t, a, 2, model.EventTypeRunning),
		replicaEntry(t, a, 3, model.EventTypeRunning),
		replicaEntry(t, b, 1, model.EventTypeRunning),
		replicaEntry(t, c, 1, model.EventTypeRunning),
	}

	assert.Equal(t, len(targetSet(nil).filter(entries)), len(entries))

	got := targetSet{"a": {1, 3}, "b": nil}.filter(entries)
	assert.DeepEqual(t, entryIDs(got), []string{"id-a-1", "id-a-3", "id-b-1"})
}

func TestStartActsOnSelectedReplicas(t *testing.T) {
	a, b, c := replicaCmd("a", 3), replicaCmd("b", 1), replicaCmd("c", 1)
	rec := &replicaRecorder{entries: []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeCreated),
		replicaEntry(t, a, 2, model.EventTypeCreated),
		replicaEntry(t, a, 3, model.EventTypeCreated),
		replicaEntry(t, b, 1, model.EventTypeCreated),
		replicaEntry(t, c, 1, model.EventTypeCreated),
	}}
	reporter := &commandPhaseReporter{}
	spec := reconcileSpec(a, b, c)

	result, err := rec.service(reporter).Start(
		context.Background(),
		SelectionFromSpec(&spec),
		StartOption{Targets: []Target{{"a", 1}, {"a", 3}, {"b", 0}}},
	)
	assert.NilError(t, err)

	assert.DeepEqual(t, rec.sorted(&rec.started), []string{"gen-a-1", "gen-a-3", "gen-b-1"})
	assert.DeepEqual(t, startOutcomeCommands(result.Starts), []string{"a", "b"})
	for _, disp := range []string{"a-1", "a-3", "b"} {
		assert.Assert(t, reporter.reached(disp, PhaseRunning), "%s not reported running", disp)
	}
	assert.Assert(t, !reporter.seen("a-2"), "an unselected replica was reported")
}

func TestStartRejectsReplicaOutsideStoredRange(t *testing.T) {
	a := replicaCmd("a", 3)
	rec := &replicaRecorder{entries: []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeCreated),
		replicaEntry(t, a, 2, model.EventTypeCreated),
	}}
	spec := reconcileSpec(a)

	// The spec declares three replicas but only two are stored, so a start
	// cannot reach the third.
	_, err := rec.service(nil).Start(
		context.Background(),
		SelectionFromSpec(&spec),
		StartOption{Targets: []Target{{"a", 3}}},
	)
	assert.ErrorContains(t, err, `"a"`)
	assert.ErrorContains(t, err, "1..2")
	assert.Equal(t, len(rec.started), 0)
}

func TestStartCompletionWaitCoversSelectedReplicas(t *testing.T) {
	a := replicaCmd("a", 3)
	b := replicaCmd("b", 1, AfterSpec{Name: "a", Condition: ConditionCompletedSuccessfully})
	entries := []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeCreated),
		replicaEntry(t, a, 2, model.EventTypeCreated),
		replicaEntry(t, a, 3, model.EventTypeCreated),
		replicaEntry(t, b, 1, model.EventTypeCreated),
	}
	spec := reconcileSpec(a, b)

	t.Run("a targeted replica is the only one waited on", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		reporter := &commandPhaseReporter{}

		result, err := rec.service(reporter).Start(
			context.Background(),
			SelectionFromSpec(&spec),
			StartOption{Targets: []Target{{"a", 2}, {"b", 0}}},
		)
		assert.NilError(t, err)

		assert.DeepEqual(t, rec.waitedTargets(), [][]string{{"gen-a-2"}})
		assert.DeepEqual(t, rec.sorted(&rec.started), []string{"gen-a-2", "gen-b-1"})
		assert.Assert(t, reporter.reached("a-2", PhaseWaiting))
		assert.Assert(t, reporter.reached("a-2", PhaseExited))
		assert.Assert(t, !reporter.seen("a-1") && !reporter.seen("a-3"),
			"unselected replicas were reported")
		for _, o := range result.Starts {
			assert.NilError(t, o.Err, "outcome of %s", o.Command)
		}
	})

	t.Run("a pulled-in dependency covers every replica", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}

		_, err := rec.service(nil).Start(
			context.Background(),
			SelectionFromSpec(&spec),
			StartOption{Targets: TargetsOf("b")},
		)
		assert.NilError(t, err)

		assert.DeepEqual(t, rec.waitedTargets(), [][]string{{"gen-a-1", "gen-a-2", "gen-a-3"}})
		assert.DeepEqual(
			t,
			rec.sorted(&rec.started),
			[]string{"gen-a-1", "gen-a-2", "gen-a-3", "gen-b-1"},
		)
	})
}

func TestStopActsOnSelectedReplicas(t *testing.T) {
	a, b, c := replicaCmd("a", 3), replicaCmd("b", 1), replicaCmd("c", 1)
	entries := []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeRunning),
		replicaEntry(t, a, 2, model.EventTypeRunning),
		replicaEntry(t, a, 3, model.EventTypeRunning),
		replicaEntry(t, b, 1, model.EventTypeRunning),
		replicaEntry(t, c, 1, model.EventTypeRunning),
	}
	selection := ProjectSelection{WorkDir: "/wd", Project: "proj"}

	rec := &replicaRecorder{entries: entries}
	reporter := &commandPhaseReporter{}
	_, err := rec.service(reporter).Stop(context.Background(), selection, StopOption{
		Targets: []Target{{"a", 1}, {"a", 3}, {"b", 0}},
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, rec.sorted(&rec.stopped), []string{"id-a-1", "id-a-3", "id-b-1"})
	assert.Assert(t, !reporter.seen("a-2"), "an unselected replica was reported")

	rec = &replicaRecorder{entries: entries}
	_, err = rec.service(nil).Stop(context.Background(), selection, StopOption{
		Targets: []Target{{"a", 4}},
	})
	assert.ErrorContains(t, err, `"a"`)
	assert.ErrorContains(t, err, "1..3")
	assert.Equal(t, len(rec.stopped), 0)
}

func TestRestartActsOnSelectedReplicas(t *testing.T) {
	a, b, c := replicaCmd("a", 3), replicaCmd("b", 1), replicaCmd("c", 1)
	rec := &replicaRecorder{entries: []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeRunning),
		replicaEntry(t, a, 2, model.EventTypeRunning),
		replicaEntry(t, a, 3, model.EventTypeRunning),
		replicaEntry(t, b, 1, model.EventTypeRunning),
		replicaEntry(t, c, 1, model.EventTypeRunning),
	}}

	_, err := rec.service(nil).Restart(
		context.Background(),
		ProjectSelection{WorkDir: "/wd", Project: "proj"},
		RestartOption{Targets: []Target{{"a", 1}, {"a", 3}, {"b", 0}}},
	)
	assert.NilError(t, err)
	assert.DeepEqual(t, rec.sorted(&rec.stopped), []string{"id-a-1", "id-a-3", "id-b-1"})
	assert.DeepEqual(t, rec.sorted(&rec.started), []string{"gen-a-1", "gen-a-3", "gen-b-1"})
}

func TestUpActsOnSelectedReplicas(t *testing.T) {
	a, b, c := replicaCmd("a", 3), replicaCmd("b", 1), replicaCmd("c", 1)
	spec := reconcileSpec(a, b, c)
	// Replica 4 is the surplus of an earlier, larger scale.
	entries := []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeExited),
		replicaEntry(t, a, 4, model.EventTypeExited),
	}

	t.Run("replica targets create and start only those replicas", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}

		result, err := rec.service(nil).Up(context.Background(), spec, UpOption{
			CreateOption: CreateOption{Targets: []Target{{"a", 2}, {"a", 3}, {"b", 0}}},
		})
		assert.NilError(t, err)

		assert.DeepEqual(t, rec.sorted(&rec.created), []string{"gen-a-2", "gen-a-3", "gen-b-1"})
		assert.DeepEqual(t, rec.sorted(&rec.started), []string{"gen-a-2", "gen-a-3", "gen-b-1"})
		assert.Equal(t, len(rec.removed), 0, "a replica target must leave the surplus alone")
		assert.DeepEqual(t, actionCommands(result.Actions), []string{"a-2", "a-3", "b"})
	})

	t.Run("a whole-command target removes the surplus", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}

		_, err := rec.service(nil).Up(context.Background(), spec, UpOption{
			CreateOption: CreateOption{Targets: TargetsOf("a")},
		})
		assert.NilError(t, err)

		assert.DeepEqual(t, rec.sorted(&rec.removed), []string{"id-a-4"})
		assert.DeepEqual(t, rec.sorted(&rec.created), []string{"gen-a-2", "gen-a-3"})
		assert.DeepEqual(t, rec.sorted(&rec.started), []string{"gen-a-1", "gen-a-2", "gen-a-3"})
	})

	t.Run("the range is the declared scale", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}

		// Replica 4 is stored, yet the spec declares three.
		_, err := rec.service(nil).Up(context.Background(), spec, UpOption{
			CreateOption: CreateOption{Targets: []Target{{"a", 4}}},
		})
		assert.ErrorContains(t, err, `"a"`)
		assert.ErrorContains(t, err, "1..3")
		assert.Equal(t, len(rec.created), 0)
	})
}

func TestScaleTargetsScaledCommands(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "cmd-compose.yaml")
	assert.NilError(t, os.WriteFile(file, []byte(`name: proj
commands:
  a:
    args: [sleep, "1"]
  b:
    args: [sleep, "1"]
`), 0o644))

	cases := []struct {
		scales map[string]int
		want   []string
	}{
		{scales: map[string]int{"a": 2}, want: []string{"a-1", "a-2"}},
		{scales: map[string]int{"a": 1, "b": 2}, want: []string{"a-1", "b-1", "b-2"}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.scales), func(t *testing.T) {
			rec := &replicaRecorder{}
			_, err := rec.service(nil).Scale(context.Background(), ScaleOption{
				File:    file,
				WorkDir: dir,
				Scales:  tc.scales,
			})
			assert.NilError(t, err)
			assert.DeepEqual(t, rec.sorted(&rec.createdReplicas), tc.want)
		})
	}
}

func TestDownRejectsReplicaTarget(t *testing.T) {
	rec := &replicaRecorder{}
	_, err := rec.service(nil).Down(
		context.Background(),
		ProjectSelection{WorkDir: "/wd", Project: "proj"},
		DownOption{Targets: []Target{{"a", 2}}},
	)
	assert.ErrorContains(t, err, "scale the command down")
	assert.Equal(t, len(rec.removed), 0)
}

func TestFlatVerbsActOnSelectedReplicas(t *testing.T) {
	a, b, c := replicaCmd("a", 3), replicaCmd("b", 1), replicaCmd("c", 1)
	entries := []store.CommandEntry{
		replicaEntry(t, a, 1, model.EventTypeRunning),
		replicaEntry(t, a, 2, model.EventTypeRunning),
		replicaEntry(t, a, 3, model.EventTypeRunning),
		replicaEntry(t, b, 1, model.EventTypeRunning),
		replicaEntry(t, c, 1, model.EventTypeRunning),
	}
	selection := ProjectSelection{WorkDir: "/wd", Project: "proj"}
	targets := []Target{{"a", 1}, {"a", 3}, {"b", 0}}
	want := []string{"id-a-1", "id-a-3", "id-b-1"}
	ctx := context.Background()

	t.Run("send-keys", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		_, err := rec.service(nil).SendKeys(ctx, selection, SendKeysOption{
			Targets: targets,
			Keys:    []string{"Enter"},
		})
		assert.NilError(t, err)
		assert.DeepEqual(t, rec.sorted(&rec.sentKeys), want)
	})

	t.Run("wait", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		_, err := rec.service(nil).Wait(ctx, selection, WaitOption{Targets: targets})
		assert.NilError(t, err)
		waited := rec.waitedTargets()
		assert.Equal(t, len(waited), 1)
		assert.DeepEqual(t, slices.Sorted(slices.Values(waited[0])), want)
	})

	t.Run("events", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		_, err := rec.service(nil).Events(ctx, selection, EventsOption{Targets: targets})
		assert.NilError(t, err)
		assert.DeepEqual(t, rec.sorted(&rec.eventIDs), want)
	})

	t.Run("inspect", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		out, err := rec.service(nil).Inspect(ctx, selection, InspectOption{Targets: targets})
		assert.NilError(t, err)
		var ids []string
		for _, o := range out {
			ids = append(ids, o.ID)
		}
		assert.DeepEqual(t, slices.Sorted(slices.Values(ids)), want)
	})

	t.Run("ps", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		statuses, err := rec.service(nil).Ps(ctx, selection, PsOption{Targets: targets})
		assert.NilError(t, err)
		var ids []string
		for _, s := range statuses {
			ids = append(ids, s.ID)
		}
		assert.DeepEqual(t, ids, want)
	})

	t.Run("status", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		states, err := rec.service(nil).Status(ctx, selection, StatusOption{Targets: targets})
		assert.NilError(t, err)
		var ids []string
		for _, s := range states {
			ids = append(ids, s.ID)
		}
		assert.DeepEqual(t, ids, want)
	})

	t.Run("out of range", func(t *testing.T) {
		rec := &replicaRecorder{entries: entries}
		_, err := rec.service(nil).Ps(ctx, selection, PsOption{Targets: []Target{{"b", 2}}})
		assert.ErrorContains(t, err, `"b"`)
		assert.ErrorContains(t, err, "1..1")
	})
}

// replicaRecorder is a cmdmanSvc fake over a fixed set of stored entries that
// records which replicas each call reached.
type replicaRecorder struct {
	entries []store.CommandEntry

	mu              sync.Mutex
	created         []string // generated names
	createdReplicas []string // "<command>-<scale index>"
	started         []string // generated names
	stopped         []string // ids
	removed         []string // ids
	sentKeys        []string // ids
	eventIDs        []string
	waited          [][]string
}

func (r *replicaRecorder) record(dst *[]string, values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	*dst = append(*dst, values...)
}

func (r *replicaRecorder) sorted(src *[]string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Sorted(slices.Values(*src))
}

func (r *replicaRecorder) waitedTargets() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.waited)
}

func (r *replicaRecorder) service(reporter Reporter) *Service {
	return &Service{reporter: reporter, svc: testCmdmanSvc{
		list: func(context.Context, cmdman.ListRequest) ([]store.CommandEntry, error) {
			return r.entries, nil
		},
		create: func(_ context.Context, req cmdman.CreateRequest) (*cmdman.CreateResult, error) {
			r.record(&r.created, req.Name)
			r.record(&r.createdReplicas,
				req.Labels[LabelCommand]+"-"+req.Labels[LabelScaleIndex])
			return nil, nil
		},
		start: func(_ context.Context, genName string) error {
			r.record(&r.started, genName)
			return nil
		},
		stop: func(_ context.Context, req cmdman.StopRequest) ([]cmdman.StopResult, error) {
			r.record(&r.stopped, req.Targets...)
			return nil, nil
		},
		remove: func(
			_ context.Context,
			req cmdman.RemoveRequest,
		) ([]cmdman.RemoveResult, error) {
			r.record(&r.removed, req.Targets...)
			return nil, nil
		},
		wait: func(_ context.Context, req cmdman.WaitRequest) ([]cmdman.WaitResult, error) {
			r.mu.Lock()
			r.waited = append(r.waited, slices.Clone(req.Targets))
			r.mu.Unlock()
			results := make([]cmdman.WaitResult, len(req.Targets))
			for i, target := range req.Targets {
				zero := 0
				results[i] = cmdman.WaitResult{ID: target, ExitCode: &zero}
			}
			return results, nil
		},
		sendKeys: func(_ context.Context, id string, _ cmdman.SendKeysRequest) error {
			r.record(&r.sentKeys, id)
			return nil
		},
		events: func(
			_ context.Context,
			req cmdman.EventsRequest,
		) (*cmdman.EventsSubscription, error) {
			r.record(&r.eventIDs, req.IDFilter...)
			return nil, nil
		},
		inspect: func(_ context.Context, id string) (*cmdman.InspectOutput, error) {
			return &cmdman.InspectOutput{ID: id}, nil
		},
	}}
}

func replicaCmd(name string, scale int, after ...AfterSpec) Command {
	return Command{Name: name, GeneratedName: "gen-" + name, Scale: scale, After: after}
}

// replicaEntry is the stored form of replica idx of cmd, labeled the way a
// create of reconcileSpec(cmd) labels it.
func replicaEntry(t *testing.T, cmd Command, idx int, state model.EventType) store.CommandEntry {
	t.Helper()
	hash, err := Hash(cmd)
	assert.NilError(t, err)
	return store.CommandEntry{
		ID:    fmt.Sprintf("id-%s-%d", cmd.Name, idx),
		Name:  InstanceName(cmd.GeneratedName, idx),
		State: state,
		ConfigJSON: &model.CommandConfig{
			Labels: BuildLabels(reconcileSpec(cmd), cmd, hash, idx),
		},
	}
}

func entryIDs(entries []store.CommandEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}

func startOutcomeCommands(outcomes []StartOutcome) []string {
	var out []string
	for _, o := range outcomes {
		out = append(out, o.Command)
	}
	return out
}

func actionCommands(actions []ActionOutcome) []string {
	var out []string
	for _, a := range actions {
		out = append(out, a.Command)
	}
	return slices.Sorted(slices.Values(out))
}
