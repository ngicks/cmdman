package cmdman

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestParseReportedStatus(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"working", ReportedStatusWorking},
		{"waiting", ReportedStatusWaiting},
		{"done", ReportedStatusDone},
		{"  Done ", ReportedStatusDone},
		{"WAITING", ReportedStatusWaiting},
	} {
		got, err := ParseReportedStatus(tc.in)
		assert.NilError(t, err, "input %q", tc.in)
		assert.Equal(t, got, tc.want, "input %q", tc.in)
	}

	for _, in := range []string{"", "busy", "wait", "done!", "0"} {
		_, err := ParseReportedStatus(in)
		assert.ErrorContains(t, err, "unknown status", "input %q", in)
	}
}

func TestServiceReportedStatusVerbs(t *testing.T) {
	appCfg, id, _ := startTestMonitor(t, false, "/bin/sh", "-c", "sleep 30")
	svc := NewService(appCfg)
	defer svc.Close()
	ctx := t.Context()

	got, err := svc.GetReportedStatus(ctx, id)
	assert.NilError(t, err)
	assert.DeepEqual(t, got, ReportedStatusInfo{})

	assert.NilError(t, svc.SetReportedStatus(ctx, id, "Waiting", "needs input"))
	got, err = svc.GetReportedStatus(ctx, id)
	assert.NilError(t, err)
	assert.DeepEqual(t, got, ReportedStatusInfo{
		Status: ReportedStatusWaiting,
		Detail: "needs input",
	})

	// Setting again without a detail replaces the previous one rather than
	// keeping it: the report is the whole state, not a patch.
	assert.NilError(t, svc.SetReportedStatus(ctx, id, ReportedStatusWorking, ""))
	got, err = svc.GetReportedStatus(ctx, id)
	assert.NilError(t, err)
	assert.DeepEqual(t, got, ReportedStatusInfo{Status: ReportedStatusWorking})

	assert.NilError(t, svc.DeleteReportedStatus(ctx, id))
	got, err = svc.GetReportedStatus(ctx, id)
	assert.NilError(t, err)
	assert.DeepEqual(t, got, ReportedStatusInfo{})

	assert.ErrorContains(t, svc.SetReportedStatus(ctx, id, "busy", ""), "unknown status")
	assert.ErrorContains(t, svc.SetReportedStatus(ctx, "no-such-command", "done", ""),
		"resolve command")
	assert.ErrorContains(t, svc.DeleteReportedStatus(ctx, "no-such-command"), "resolve command")

	_, err = svc.GetReportedStatus(ctx, "no-such-command")
	assert.ErrorContains(t, err, "resolve command")
}
