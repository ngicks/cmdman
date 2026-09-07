package commands

import (
	"errors"
	"iter"
	"strings"
	"testing"
)

type targetOutcome struct {
	id  string
	err error
}

func outcomeSeq(outcomes ...targetOutcome) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for _, o := range outcomes {
			if !yield(o.id, o.err) {
				return
			}
		}
	}
}

func TestReportTargetErrors(t *testing.T) {
	t.Run("failures print one line each and fail the command", func(t *testing.T) {
		var out strings.Builder
		err := reportTargetErrors(&out, "stop", false, outcomeSeq(
			targetOutcome{"aaa", errors.New("no such command")},
			targetOutcome{"bbb", nil},
			targetOutcome{"ccc", errors.New("monitor unreachable")},
		))
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if got, want := err.Error(), "one or more stop operations failed"; got != want {
			t.Errorf("error = %q, want %q", got, want)
		}
		want := "stop aaa: no such command\nstop ccc: monitor unreachable\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("ignore keeps the lines but drops the failure", func(t *testing.T) {
		var out strings.Builder
		err := reportTargetErrors(&out, "rm", true, outcomeSeq(
			targetOutcome{"aaa", errors.New("command is running")},
		))
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if want := "rm aaa: command is running\n"; out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})

	t.Run("no failures is silent and succeeds", func(t *testing.T) {
		var out strings.Builder
		err := reportTargetErrors(&out, "signal", false, outcomeSeq(
			targetOutcome{"aaa", nil},
			targetOutcome{"bbb", nil},
		))
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
		if out.String() != "" {
			t.Errorf("output = %q, want empty", out.String())
		}
	})
}
