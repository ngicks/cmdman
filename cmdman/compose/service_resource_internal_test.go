package compose

import (
	"errors"
	"maps"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman"
	"github.com/ngicks/cmdman/cmdman/model"
)

// putTestReplica stores replica index of command web of project proj in /wd,
// labeled the way compose labels it.
func putTestReplica(f *fakeCmdman, index, scale int) {
	spec := ComposeSpec{Project: "proj", WorkDir: "/wd", ComposeFile: "/wd/cmd-compose.yaml"}
	nc := Command{
		Name:          "web",
		Scale:         scale,
		GeneratedName: GenerateName(workdirHash("/wd"), "proj", "web"),
	}
	f.put(cmdman.CreateRequest{
		Name:   InstanceName(nc.GeneratedName, index),
		Argv:   []string{"sleep", "300"},
		Labels: BuildLabels(spec, nc, "hash", index),
	}, model.EventTypeCreated)
}

func testResourceSelection() ProjectSelection {
	return ProjectSelection{Project: "proj", WorkDir: "/wd"}
}

func TestResourceSetGetUnset(t *testing.T) {
	f := newFakeCmdman()
	putTestReplica(f, 1, 1)
	s := f.service(nil)
	sel := testResourceSelection()
	opt := ResourceOption{Command: "web", Key: "scratch"}

	_, err := s.ResourceGet(t.Context(), sel, opt)
	assert.Assert(t, errors.Is(err, ErrNoResource), "got %v", err)

	assert.NilError(t, s.ResourceSet(t.Context(), sel, opt, "/tmp/a"))
	value, err := s.ResourceGet(t.Context(), sel, opt)
	assert.NilError(t, err)
	assert.Equal(t, value, "/tmp/a")

	replica := resourceRef{Project: "proj", WorkDir: "/wd", Command: "web", ScaleIndex: 1}
	holder, ok := f.get(HolderName(replica.replicaName(), "scratch"))
	assert.Assert(t, ok, "the holder is named after the replica")
	_, hasRelease := holder.ConfigJSON.Labels[LabelResourceRelease]
	assert.Assert(t, !hasRelease, "a value set by hand has no release")

	assert.NilError(t, s.ResourceSet(t.Context(), sel, opt, "/tmp/b"))
	value, err = s.ResourceGet(t.Context(), sel, opt)
	assert.NilError(t, err)
	assert.Equal(t, value, "/tmp/b")

	assert.NilError(t, s.ResourceUnset(t.Context(), sel, opt))
	_, err = s.ResourceGet(t.Context(), sel, opt)
	assert.Assert(t, errors.Is(err, ErrNoResource), "got %v", err)
	assert.NilError(t, s.ResourceUnset(t.Context(), sel, opt), "unset of nothing is no error")
}

func TestResourceSetKeepsRelease(t *testing.T) {
	f := newFakeCmdman()
	putTestReplica(f, 1, 1)
	s := f.service(nil)
	r := testHookReplica()
	req, err := resourceHolder{
		Ref:     r.resourceRef("scratch"),
		Owner:   r.Name,
		Value:   "/tmp/old",
		Release: &resourceRelease{Event: LifecycleRemovePost, Args: []string{"rm"}},
		Dir:     "/release/dir",
		Env:     []string{"RELEASE_VAR=1"},
	}.createRequest()
	assert.NilError(t, err)
	f.put(req, model.EventTypeCreated)

	err = s.ResourceSet(t.Context(), testResourceSelection(),
		ResourceOption{Command: "web", Key: "scratch"}, "/tmp/new")
	assert.NilError(t, err)

	holder, _ := f.get(HolderName(r.Name, "scratch"))
	decoded, err := decodeHolder(holder)
	assert.NilError(t, err)
	assert.Equal(t, decoded.Value, "/tmp/new")
	assert.DeepEqual(t, decoded.Release,
		&resourceRelease{Event: LifecycleRemovePost, Args: []string{"rm"}})
	assert.Equal(t, decoded.Dir, "/release/dir")
	assert.DeepEqual(t, decoded.Env, []string{"RELEASE_VAR=1"})
}

func TestResourceSetFailedReplacementKeepsOldValue(t *testing.T) {
	f := newFakeCmdman()
	putTestReplica(f, 1, 1)
	s := f.service(nil)
	sel := testResourceSelection()
	opt := ResourceOption{Command: "web", Key: "scratch"}
	assert.NilError(t, s.ResourceSet(t.Context(), sel, opt, "/tmp/old"))
	f.createErr = func(cmdman.CreateRequest) error { return errors.New("disk full") }

	err := s.ResourceSet(t.Context(), sel, opt, "/tmp/new")

	assert.ErrorContains(t, err, "disk full")
	value, err := s.ResourceGet(t.Context(), sel, opt)
	assert.NilError(t, err)
	assert.Equal(t, value, "/tmp/old")
}

func TestResourceScaledCommandNeedsScaleIndex(t *testing.T) {
	f := newFakeCmdman()
	putTestReplica(f, 1, 2)
	putTestReplica(f, 2, 2)
	s := f.service(nil)
	sel := testResourceSelection()

	_, err := s.ResourceGet(t.Context(), sel, ResourceOption{Command: "web", Key: "scratch"})
	assert.ErrorContains(t, err, "pick one with --scale 1..2")

	opt2 := ResourceOption{Command: "web", ScaleIndex: 2, Key: "scratch"}
	assert.NilError(t, s.ResourceSet(t.Context(), sel, opt2, "two"))
	value, err := s.ResourceGet(t.Context(), sel, opt2)
	assert.NilError(t, err)
	assert.Equal(t, value, "two")

	_, err = s.ResourceGet(t.Context(), sel,
		ResourceOption{Command: "web", ScaleIndex: 1, Key: "scratch"})
	assert.Assert(t, errors.Is(err, ErrNoResource), "got %v", err)
}

func TestResourceScaleFromSpec(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	sel := testResourceSelection()
	sel.Spec = &ComposeSpec{Project: "proj", WorkDir: "/wd", Commands: []Command{
		{Name: "web", Scale: 3},
	}}

	err := s.ResourceSet(t.Context(), sel, ResourceOption{Command: "web", Key: "k"}, "v")

	assert.ErrorContains(t, err, "runs 3 replicas")
}

func TestResourceHolderOutlivesReplica(t *testing.T) {
	f := newFakeCmdman()
	s := f.service(nil)
	sel := testResourceSelection()
	replica2 := testHookReplica()
	replica2.ScaleIndex = 2
	replica2.Name = replica2.resourceRef("").replicaName()
	putTestHolder(f, replica2, "scratch", "/tmp/two")

	value, err := s.ResourceGet(t.Context(), sel, ResourceOption{Command: "web", Key: "scratch"})
	assert.NilError(t, err)
	assert.Equal(t, value, "/tmp/two", "the only holder names the replica")

	putTestHolder(f, testHookReplica(), "scratch", "/tmp/one")
	_, err = s.ResourceGet(t.Context(), sel, ResourceOption{Command: "web", Key: "scratch"})
	assert.ErrorContains(t, err, "held for replicas [1 2]")

	value, err = s.ResourceGet(t.Context(), sel,
		ResourceOption{Command: "web", ScaleIndex: 1, Key: "scratch"})
	assert.NilError(t, err)
	assert.Equal(t, value, "/tmp/one")
}

func TestResourceRejectsBadInput(t *testing.T) {
	s := newFakeCmdman().service(nil)
	cases := map[string]struct {
		sel  ProjectSelection
		opt  ResourceOption
		want string
	}{
		"no project": {
			sel:  ProjectSelection{WorkDir: "/wd"},
			opt:  ResourceOption{Command: "web", Key: "k"},
			want: "no project selected",
		},
		"bad key": {
			sel:  testResourceSelection(),
			opt:  ResourceOption{Command: "web", Key: "a/b"},
			want: "resource name",
		},
		"negative scale": {
			sel:  testResourceSelection(),
			opt:  ResourceOption{Command: "web", Key: "k", ScaleIndex: -1},
			want: "1 or greater",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := s.ResourceGet(t.Context(), tc.sel, tc.opt)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestResourceScaleIndex(t *testing.T) {
	cases := []struct {
		name    string
		opt     ResourceOption
		count   int
		heldBy  []int
		want    int
		wantErr string
	}{
		{name: "explicit", opt: ResourceOption{ScaleIndex: 3}, count: 2, want: 3},
		{name: "sole replica", count: 1, heldBy: []int{2}, want: 1},
		{name: "scaled", count: 2, wantErr: "--scale 1..2"},
		{name: "nothing known", want: 1},
		{name: "one holder", heldBy: []int{4}, want: 4},
		{name: "several holders", heldBy: []int{1, 4}, wantErr: "held for replicas [1 4]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resourceScaleIndex(tc.opt, tc.count, tc.heldBy)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func lookupMap(env map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestResolveContextSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	env := lookupMap(map[string]string{
		ENV_CMDMAN_COMPOSE_WORK_DIR: "/wd/",
		ENV_CMDMAN_COMPOSE_PROJECT:  "proj",
	})

	sel, err := ResolveContextSelection(NormalizeOpts{}, env)
	assert.NilError(t, err)
	assert.DeepEqual(t, sel, ProjectSelection{WorkDir: "/wd", Project: "proj"})

	// Explicit selection flags win over the environment.
	sel, err = ResolveContextSelection(
		NormalizeOpts{ProjectName: "other", WorkDir: "/elsewhere"}, env)
	assert.NilError(t, err)
	assert.DeepEqual(t, sel, ProjectSelection{WorkDir: "/elsewhere", Project: "other"})

	sel, err = ResolveContextSelection(NormalizeOpts{}, lookupMap(map[string]string{
		ENV_CMDMAN_COMPOSE_WORK_DIR: "/wd",
	}))
	assert.NilError(t, err)
	assert.Equal(t, sel.Project, "", "half a context in the environment is ignored")
}

func TestContextScaleIndex(t *testing.T) {
	sel := testResourceSelection()
	full := map[string]string{
		ENV_CMDMAN_COMPOSE_COMMAND:     "web",
		ENV_CMDMAN_COMPOSE_PROJECT:     "proj",
		ENV_CMDMAN_COMPOSE_WORK_DIR:    "/wd",
		ENV_CMDMAN_COMPOSE_SCALE_INDEX: "2",
	}
	with := func(k, v string) map[string]string {
		env := maps.Clone(full)
		env[k] = v
		return env
	}
	cases := map[string]struct {
		env     map[string]string
		command string
		want    int
	}{
		"own replica":            {full, "web", 2},
		"other command":          {full, "db", 0},
		"other project":          {with(ENV_CMDMAN_COMPOSE_PROJECT, "x"), "web", 0},
		"other work dir":         {with(ENV_CMDMAN_COMPOSE_WORK_DIR, "/x"), "web", 0},
		"unparsable index":       {with(ENV_CMDMAN_COMPOSE_SCALE_INDEX, "two"), "web", 0},
		"outside any replica":    {map[string]string{}, "web", 0},
		"work dir needs no tidy": {with(ENV_CMDMAN_COMPOSE_WORK_DIR, "/wd/"), "web", 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, ContextScaleIndex(sel, tc.command, lookupMap(tc.env)), tc.want)
		})
	}
}
