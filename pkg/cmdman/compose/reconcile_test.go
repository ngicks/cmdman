package compose

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// fullClosure marks every command in the spec as in-closure.
func fullClosure(spec ComposeSpec) map[string]struct{} {
	closure := make(map[string]struct{}, len(spec.Commands))
	for _, c := range spec.Commands {
		closure[c.Name] = struct{}{}
	}
	return closure
}

// recordOrder returns an action that appends each visited command name to a
// shared slice and reports a clean terminal state.
func recordOrder(mu *sync.Mutex, order *[]string) graphAction {
	return func(_ context.Context, _ *reconcileGraph, v *graphVertex) actionResult {
		mu.Lock()
		*order = append(*order, string(v.ID))
		mu.Unlock()
		zero := 0
		return actionResult{State: model.EventTypeExited, ExitCode: &zero}
	}
}

// before reports whether a appears before b in order.
func before(order []string, a, b string) bool {
	ia := slices.Index(order, a)
	ib := slices.Index(order, b)
	return ia >= 0 && ib >= 0 && ia < ib
}

func TestBuildReconcileGraphVirtualEdges(t *testing.T) {
	spec := reconcileSpec(
		reconcileCmd("api"),
		reconcileCmd("worker", AfterSpec{Name: "api", Condition: ConditionRunning}),
	)
	g := buildReconcileGraph(spec, nil, fullClosure(spec))

	begin := g.Vertices[beginVertex]
	end := g.Vertices[endVertex]
	if len(begin.Children) != 2 || len(end.Parents) != 2 {
		t.Fatalf("begin/end must connect every command: begin=%d end=%d",
			len(begin.Children), len(end.Parents))
	}

	worker := g.Vertices["worker"]
	// worker's parents: begin + api.
	if _, ok := worker.Parents[beginVertex]; !ok {
		t.Fatal("worker should have begin as a parent")
	}
	if edge, ok := worker.Parents["api"]; !ok || edge.Condition != ConditionRunning {
		t.Fatalf("worker should depend on api with running condition, got %#v", worker.Parents)
	}
	// api's children: end + worker.
	if _, ok := g.Vertices["api"].Children["worker"]; !ok {
		t.Fatal("api should have worker as a dependent child")
	}
}

// TestWalkUpVisitsDependentsBeforeDependencies exercises the up-walk direction:
// dependents are reconciled before the dependencies they sit on. This validates
// the reusable graph for stop/down stop phases.
func TestWalkUpVisitsDependentsBeforeDependencies(t *testing.T) {
	// Linear chain a -> b -> c (c depends on b depends on a) plus an independent d.
	spec := reconcileSpec(
		reconcileCmd("a"),
		reconcileCmd("b", AfterSpec{Name: "a", Condition: ConditionRunning}),
		reconcileCmd("c", AfterSpec{Name: "b", Condition: ConditionRunning}),
		reconcileCmd("d"),
	)
	g := buildReconcileGraph(spec, nil, fullClosure(spec))

	var (
		mu    sync.Mutex
		order []string
	)
	g.walk(context.Background(), walkFromEnd, 4, recordOrder(&mu, &order))

	if len(order) != 4 {
		t.Fatalf("expected all four commands visited, got %v", order)
	}
	if !before(order, "c", "b") || !before(order, "b", "a") {
		t.Fatalf("up walk must visit dependents before dependencies, got %v", order)
	}
}

// TestWalkDownVisitsDependenciesBeforeDependents is the mirror: dependencies are
// reconciled before dependents during a down walk.
func TestWalkDownVisitsDependenciesBeforeDependents(t *testing.T) {
	spec := reconcileSpec(
		reconcileCmd("a"),
		reconcileCmd("b", AfterSpec{Name: "a", Condition: ConditionRunning}),
		reconcileCmd("c", AfterSpec{Name: "b", Condition: ConditionRunning}),
	)
	g := buildReconcileGraph(spec, nil, fullClosure(spec))

	var (
		mu    sync.Mutex
		order []string
	)
	g.walk(context.Background(), walkFromBegin, 4, recordOrder(&mu, &order))

	if !before(order, "a", "b") || !before(order, "b", "c") {
		t.Fatalf("down walk must visit dependencies before dependents, got %v", order)
	}
}

// TestWalkDownClosureExcludesUntargetedCommands verifies vertices outside the
// closure are never acted on, while their snapshot can still satisfy an edge.
func TestWalkDownClosureExcludesUntargetedCommands(t *testing.T) {
	spec := reconcileSpec(
		reconcileCmd("api"),
		reconcileCmd("worker", AfterSpec{Name: "api", Condition: ConditionRunning}),
	)
	// Only worker is targeted; api is out of closure but already running.
	closure := map[string]struct{}{"worker": {}}
	snaps := map[string]commandSnapshot{
		"api": {State: model.EventTypeRunning},
	}
	g := buildReconcileGraph(spec, snaps, closure)

	var (
		mu    sync.Mutex
		order []string
	)
	g.walk(context.Background(), walkFromBegin, 4, recordOrder(&mu, &order))

	if !slices.Equal(order, []string{"worker"}) {
		t.Fatalf("only the in-closure worker should be acted on, got %v", order)
	}
	if g.Vertices["worker"].Err != nil {
		t.Fatalf("worker should be satisfied by api's running snapshot, got %v",
			g.Vertices["worker"].Err)
	}
}
