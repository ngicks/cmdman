package compose

import (
	"context"
	"strconv"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// putListReplica stores replica scaleIndex of command in project at workDir,
// labelled the way compose labels it.
func putListReplica(
	f *fakeCmdman,
	project, workDir, command string,
	scaleIndex, scale int,
) hookReplica {
	ref := resourceRef{Project: project, WorkDir: workDir, Command: command, ScaleIndex: scaleIndex}
	r := hookReplica{
		Project:    project,
		WorkDir:    workDir,
		Command:    command,
		ScaleIndex: scaleIndex,
		Scale:      scale,
		Name:       ref.replicaName(),
	}
	f.put(cmdman.CreateRequest{
		Name: r.Name,
		Argv: []string{"sleep", "300"},
		Labels: map[string]string{
			LabelProject:    project,
			LabelWorkdir:    workDir,
			LabelCommand:    command,
			LabelFile:       workDir + "/cmd-compose.yaml",
			LabelScaleIndex: strconv.Itoa(scaleIndex),
			LabelScale:      strconv.Itoa(scale),
			LabelVersion:    LabelVersionValue,
		},
	}, model.EventTypeRunning)
	return r
}

// putListExec stores the exec command of hook's ev for r, left exited as a
// failed hook run leaves it.
func putListExec(f *fakeCmdman, r hookReplica, hook string, ev LifecycleEvent) {
	f.put(cmdman.CreateRequest{
		Name:   ExecCommandName(r.Name, hook, ev),
		Argv:   []string{"false"},
		Labels: execLabels(r, hook, ev),
	}, model.EventTypeExited)
}

// listFixture is project proj in /wd with web scaled to 2 and db, one hook run
// and one holder of web, and a hook run whose replica is gone. Project other
// in /wd and proj in /elsewhere hold a resource each, with no replica left.
type listFixture struct {
	f                    *fakeCmdman
	web1, web2, db, gone hookReplica
	other, elsewhere     hookReplica
}

// goneReplica describes replica 1 of command in project at workDir, which is
// not stored.
func goneReplica(project, workDir, command string) hookReplica {
	ref := resourceRef{Project: project, WorkDir: workDir, Command: command, ScaleIndex: 1}
	return hookReplica{
		Project:    project,
		WorkDir:    workDir,
		Command:    command,
		ScaleIndex: 1,
		Scale:      1,
		Name:       ref.replicaName(),
	}
}

func newListFixture() listFixture {
	f := newFakeCmdman()
	fx := listFixture{f: f}
	fx.db = putListReplica(f, "proj", "/wd", "db", 1, 1)
	fx.web1 = putListReplica(f, "proj", "/wd", "web", 1, 2)
	fx.web2 = putListReplica(f, "proj", "/wd", "web", 2, 2)
	putListExec(f, fx.web1, "scratch", LifecycleCreatePre)
	putTestHolder(f, fx.web2, "scratch", "/tmp/web-2")
	fx.gone = goneReplica("proj", "/wd", "old")
	putListExec(f, fx.gone, "cleanup", LifecycleRemovePost)

	fx.other = goneReplica("other", "/wd", "web")
	putTestHolder(f, fx.other, "scratch", "/tmp/other")
	fx.elsewhere = goneReplica("proj", "/elsewhere", "web")
	putTestHolder(f, fx.elsewhere, "scratch", "/tmp/elsewhere")
	return fx
}

// psRow is the part of a CommandStatus that says which row it is.
type psRow struct {
	Command, Name, Intermediate, Owner string
}

func psRows(statuses []CommandStatus) []psRow {
	rows := make([]psRow, len(statuses))
	for i, s := range statuses {
		rows[i] = psRow{s.Command, s.Name, s.Intermediate, s.Owner}
	}
	return rows
}

func TestPsListsIntermediatesAfterTheirReplicas(t *testing.T) {
	fx := newListFixture()
	svc := fx.f.service(nil)
	selection := ProjectSelection{WorkDir: "/wd", Project: "proj"}

	execName := ExecCommandName(fx.web1.Name, "scratch", LifecycleCreatePre)
	holderName := HolderName(fx.web2.Name, "scratch")
	goneName := ExecCommandName(fx.gone.Name, "cleanup", LifecycleRemovePost)
	replica := func(r hookReplica) psRow { return psRow{Command: r.Command, Name: r.Name} }
	execRow := psRow{"web", execName, IntermediateExec, fx.web1.Name}
	holderRow := psRow{"web", holderName, IntermediateHolder, fx.web2.Name}

	cases := []struct {
		name    string
		targets []Target
		want    []psRow
	}{
		{
			name: "whole project",
			want: []psRow{
				replica(fx.db),
				replica(fx.web1),
				replica(fx.web2),
				execRow,
				holderRow,
				{"", goneName, IntermediateExec, fx.gone.Name},
			},
		},
		{
			name:    "every replica of a command",
			targets: []Target{{Command: "web"}},
			want:    []psRow{replica(fx.web1), replica(fx.web2), execRow, holderRow},
		},
		{
			name:    "one replica",
			targets: []Target{{Command: "web", ScaleIndex: 2}},
			want:    []psRow{replica(fx.web2), holderRow},
		},
		{
			name:    "a command without intermediates",
			targets: []Target{{Command: "db"}},
			want:    []psRow{replica(fx.db)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			statuses, err := svc.Ps(context.Background(), selection, PsOption{Targets: tc.targets})
			assert.NilError(t, err)
			assert.DeepEqual(t, psRows(statuses), tc.want)
		})
	}

	t.Run("intermediate rows carry their command's columns", func(t *testing.T) {
		statuses, err := svc.Ps(context.Background(), selection, PsOption{
			Targets: []Target{{Command: "web", ScaleIndex: 2}},
		})
		assert.NilError(t, err)
		holder := statuses[len(statuses)-1]
		assert.Equal(t, holder.State, model.EventTypeCreated)
		assert.DeepEqual(t, holder.Argv, []string{"true"})
	})
}

func TestPsWithoutProjectListsEveryIntermediateInTheWorkdir(t *testing.T) {
	fx := newListFixture()
	statuses, err := fx.f.service(nil).Ps(
		context.Background(), ProjectSelection{WorkDir: "/wd"}, PsOption{})
	assert.NilError(t, err)

	var holders []string
	for _, s := range statuses {
		if s.Intermediate == IntermediateHolder {
			holders = append(holders, s.Name)
		}
	}
	assert.DeepEqual(t, holders, []string{
		HolderName(fx.other.Name, "scratch"),
		HolderName(fx.web2.Name, "scratch"),
	})
}

func TestListProjectsCountsIntermediates(t *testing.T) {
	fx := newListFixture()
	summaries, err := fx.f.service(nil).ListProjects(context.Background())
	assert.NilError(t, err)
	assert.DeepEqual(t, summaries, []ProjectSummary{
		// Projects with no replica left are listed for the intermediates they
		// still have.
		{Project: "other", WorkDir: "/wd", Intermediates: 1},
		{Project: "proj", WorkDir: "/elsewhere", Intermediates: 1},
		{
			Project:       "proj",
			WorkDir:       "/wd",
			ComposeFile:   "/wd/cmd-compose.yaml",
			Commands:      3,
			Running:       3,
			Intermediates: 3,
		},
	})
}

func TestHooksProjectLabelsOmitsEmptyProject(t *testing.T) {
	assert.DeepEqual(t, hooksProjectLabels("/wd", ""),
		map[string]string{LabelHooksWorkdir: "/wd"})
	assert.DeepEqual(t, hooksProjectLabels("/wd", "proj"),
		map[string]string{LabelHooksWorkdir: "/wd", LabelHooksProject: "proj"})
}
