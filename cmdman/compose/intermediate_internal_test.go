package compose

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/ngicks/cmdman/cmdman/logdriver"
	"github.com/ngicks/cmdman/cmdman/model"
)

// replicaLabels are the labels by which compose verbs select and read the
// commands of a project. No intermediate may carry any of them.
var replicaLabels = []string{
	LabelProject, LabelWorkdir, LabelCommand, LabelScaleIndex, LabelScale, LabelFile,
	LabelConfigHash, LabelHooks,
}

func TestExecLabels(t *testing.T) {
	r := testHookReplica()

	labels := execLabels(r, "notify", LifecycleStartPost)

	assert.DeepEqual(t, labels, map[string]string{
		LabelIntermediate: IntermediateExec,
		LabelOwner:        r.Name,
		LabelHooksProject: "proj",
		LabelHooksWorkdir: "/wd",
		LabelHook:         "notify",
		LabelHookEvent:    "start_post",
	})
	for _, k := range replicaLabels {
		_, ok := labels[k]
		assert.Assert(t, !ok, "exec labels carry %s", k)
	}
}

func TestResourceHolderRoundTrip(t *testing.T) {
	ref := resourceRef{Project: "proj", WorkDir: "/wd", Command: "web", ScaleIndex: 2, Key: "net"}
	h := resourceHolder{
		Ref:   ref,
		Owner: ref.replicaName(),
		Value: "net-123",
		Release: &resourceRelease{
			Event: LifecycleStopPost, Args: []string{"podman", "network", "rm"},
			OnError: OnErrorIgnore,
		},
		Dir: "/wd/web",
		Env: []string{"A=1"},
	}

	req, err := h.createRequest()
	assert.NilError(t, err)

	assert.Equal(t, req.Name, HolderName(ref.replicaName(), "net"))
	assert.Assert(t, req.Replace)
	assert.DeepEqual(t, req.Argv, []string{"true"})
	assert.Equal(t, req.LogDriver, logdriver.DriverNone)
	assert.Equal(t, req.RestartPolicy, model.RestartPolicyNo)
	assert.Assert(t, req.ImportHostEnv != nil && !*req.ImportHostEnv)
	assert.Assert(t, req.InjectEnv != nil && !*req.InjectEnv)
	assert.DeepEqual(t, req.Labels, map[string]string{
		LabelIntermediate:       IntermediateHolder,
		LabelOwner:              ref.replicaName(),
		LabelHooksProject:       "proj",
		LabelHooksWorkdir:       "/wd",
		LabelResourceCommand:    "web",
		LabelResourceScaleIndex: "2",
		LabelResourceKey:        "net",
		LabelResourceValue:      "net-123",
		LabelResourceRelease: `{"event":"stop_post","args":["podman","network","rm"],` +
			`"on_error":"ignore"}`,
	})
	for _, k := range replicaLabels {
		_, ok := req.Labels[k]
		assert.Assert(t, !ok, "holder labels carry %s", k)
	}

	decoded, err := decodeHolder(cmdmanEntry{
		Name: req.Name,
		ConfigJSON: &model.CommandConfig{
			Dir: req.Dir, Env: req.Env, Labels: req.Labels,
		},
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, decoded, h)
}

func TestResourceHolderWithoutRelease(t *testing.T) {
	ref := resourceRef{Project: "proj", WorkDir: "/wd", Command: "web", ScaleIndex: 1, Key: "k"}
	req, err := resourceHolder{Ref: ref, Owner: ref.replicaName(), Value: "v"}.createRequest()
	assert.NilError(t, err)
	_, ok := req.Labels[LabelResourceRelease]
	assert.Assert(t, !ok)
}

func TestResourceRefReplicaName(t *testing.T) {
	ref := resourceRef{Project: "my-proj", WorkDir: "/wd", Command: "web", ScaleIndex: 3}
	assert.Equal(t, ref.replicaName(),
		InstanceName(GenerateName(workdirHash("/wd"), "my-proj", "web"), 3))
}
