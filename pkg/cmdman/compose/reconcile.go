package compose

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ngicks/cmdman/pkg/cmdman/model"
)

// commandSnapshot is the service-level equivalent of one `compose ps` row: the
// current stored state, exit code, and identity of a project-labeled command as
// observed before reconciliation begins. Reconcile decisions and user-visible
// status agree on this snapshot.
type commandSnapshot struct {
	// ID is the cmdman command ID.
	ID string
	// GenName is the deterministic cmdman command name (Command.GeneratedName as
	// stored). It is retained for diagnostics; the Start target is taken from the
	// spec's GeneratedName to preserve existing behavior.
	GenName string
	// State is the persisted state at snapshot time.
	State model.EventType
	// ExitCode is the last recorded exit code (nil when never exited).
	ExitCode *int
}

// resolveTargetCommands resolves the supplied command-name subset to the set of
// commands to operate on for a down walk (up/start), transitively pulling in
// every after-dependency so a targeted command can be created and started
// alongside what it needs. names == nil (or empty) → every command in the spec.
func resolveTargetCommands(spec ComposeSpec, names []string) map[string]struct{} {
	all := make(map[string]Command, len(spec.Commands))
	for _, nc := range spec.Commands {
		all[nc.Name] = nc
	}
	target := make(map[string]struct{})
	if len(names) == 0 {
		for n := range all {
			target[n] = struct{}{}
		}
		return target
	}
	var walk func(string)
	walk = func(n string) {
		if _, seen := target[n]; seen {
			return
		}
		nc, ok := all[n]
		if !ok {
			return
		}
		target[n] = struct{}{}
		for _, dep := range nc.After {
			walk(dep.Name)
		}
	}
	for _, n := range names {
		walk(n)
	}
	return target
}

// resolveStopTargetCommands resolves the supplied command-name subset to the set
// of commands to operate on for an up walk (stop/down), transitively pulling in
// every dependent so a dependency is never torn down before the commands that
// depend on it. names == nil (or empty) → every command in the spec.
func resolveStopTargetCommands(spec ComposeSpec, names []string) map[string]struct{} {
	all := make(map[string]struct{}, len(spec.Commands))
	for _, nc := range spec.Commands {
		all[nc.Name] = struct{}{}
	}
	target := make(map[string]struct{})
	if len(names) == 0 {
		for n := range all {
			target[n] = struct{}{}
		}
		return target
	}
	// dependents maps a command to the commands that declare it in their After.
	dependents := make(map[string][]string)
	for _, nc := range spec.Commands {
		for _, dep := range nc.After {
			dependents[dep.Name] = append(dependents[dep.Name], nc.Name)
		}
	}
	var walk func(string)
	walk = func(n string) {
		if _, seen := target[n]; seen {
			return
		}
		if _, ok := all[n]; !ok {
			return
		}
		target[n] = struct{}{}
		for _, d := range dependents[n] {
			walk(d)
		}
	}
	for _, n := range names {
		walk(n)
	}
	return target
}

// vertexID identifies a graph vertex. Command vertices use the compose command
// name; the two virtual vertices use sentinel values that cannot collide with a
// command name.
type vertexID string

const (
	beginVertex vertexID = "\x00begin"
	endVertex   vertexID = "\x00end"
)

// walkDirection selects which way a walk traverses the graph.
type walkDirection int

const (
	// walkFromBegin starts at begin and moves toward end. Used by up/start.
	walkFromBegin walkDirection = iota
	// walkFromEnd starts at end and moves toward begin. Used by stop/down stop
	// phases: dependents are handled before dependencies.
	walkFromEnd
)

// graphEdge is a directed dependency edge. For compose `after`, the natural
// direction is dependency -> dependent; Condition is the dependent's
// after.Condition on the dependency. Virtual edges (begin->cmd, cmd->end) carry
// no condition.
type graphEdge struct {
	From      vertexID
	To        vertexID
	Condition AfterCondition
}

// graphVertex is one node of the reconcile graph plus its in-process scheduling
// and result state. begin/end have a nil Command.
type graphVertex struct {
	ID       vertexID
	Command  *Command // nil for begin/end
	Snapshot commandSnapshot

	// Parents maps each incoming-edge source to that edge (begin and real
	// dependencies). Children maps each outgoing-edge target to that edge (end
	// and real dependents).
	Parents  map[vertexID]graphEdge
	Children map[vertexID]graphEdge

	// InClosure marks a vertex as part of the operation's target set. Vertices
	// outside the closure are never acted on; their snapshot is still consulted
	// when they appear as a dependency edge.
	InClosure bool

	// Scheduling state, guarded by reconcileGraph.mu.
	Queued     bool
	InProgress bool
	Consumed   bool
	Blocked    bool
	WaitReason string

	// Reconcile result, guarded by reconcileGraph.mu. State/ExitCode start from
	// the snapshot and are overwritten by the action's observation.
	State    model.EventType
	ExitCode *int
	Err      error

	// Order is a monotonic consumption sequence assigned when the vertex is
	// finished (completed or finalized). It records the order the walk acted on
	// vertices so teardown reporting can reflect what actually happened (for a
	// stop walk that is reverse-dependency order). 0 means not yet consumed.
	Order int
}

// reconcileGraph is in-process reconciliation state. It is built from the spec
// and command snapshots, then mutated by workers after each service action so
// later vertices decide from current-run state rather than stale store reads.
type reconcileGraph struct {
	mu       sync.Mutex
	Vertices map[vertexID]*graphVertex
	// seq is the monotonic counter behind graphVertex.Order. Guarded by mu.
	seq int
}

// markConsumedLocked stamps a vertex with the next consumption sequence. Caller
// holds g.mu.
func (g *reconcileGraph) markConsumedLocked(v *graphVertex) {
	g.seq++
	v.Order = g.seq
}

// actionResult is what a graphAction reports about the vertex it acted on.
type actionResult struct {
	State    model.EventType
	ExitCode *int
	Err      error
}

// graphAction performs the operation for one vertex. It runs outside the graph
// lock and must not touch graph scheduling state directly.
type graphAction func(context.Context, *reconcileGraph, *graphVertex) actionResult

// buildReconcileGraph builds the full graph from the spec (every command plus
// the begin/end virtual vertices and their edges), seeds each vertex from the
// snapshot, and marks closure membership. Construction is uniform: begin->cmd
// and cmd->end edges are added for every command, so root/leaf detection is a
// property of the edges rather than special-cased.
func buildReconcileGraph(
	spec ComposeSpec,
	snaps map[string]commandSnapshot,
	closure map[string]struct{},
) *reconcileGraph {
	g := &reconcileGraph{Vertices: make(map[vertexID]*graphVertex, len(spec.Commands)+2)}

	begin := &graphVertex{
		ID:       beginVertex,
		Parents:  map[vertexID]graphEdge{},
		Children: map[vertexID]graphEdge{},
	}
	end := &graphVertex{
		ID:       endVertex,
		Parents:  map[vertexID]graphEdge{},
		Children: map[vertexID]graphEdge{},
	}
	g.Vertices[beginVertex] = begin
	g.Vertices[endVertex] = end

	for i := range spec.Commands {
		c := &spec.Commands[i]
		id := vertexID(c.Name)
		snap := snaps[c.Name]
		_, in := closure[c.Name]
		g.Vertices[id] = &graphVertex{
			ID:        id,
			Command:   c,
			Snapshot:  snap,
			State:     snap.State,
			ExitCode:  snap.ExitCode,
			InClosure: in,
			Parents:   map[vertexID]graphEdge{},
			Children:  map[vertexID]graphEdge{},
		}
	}

	// Virtual edges for every command.
	for i := range spec.Commands {
		id := vertexID(spec.Commands[i].Name)
		be := graphEdge{From: beginVertex, To: id}
		begin.Children[id] = be
		g.Vertices[id].Parents[beginVertex] = be

		ee := graphEdge{From: id, To: endVertex}
		end.Parents[id] = ee
		g.Vertices[id].Children[endVertex] = ee
	}

	// Real dependency edges: dependency -> dependent.
	for i := range spec.Commands {
		c := &spec.Commands[i]
		depID := vertexID(c.Name)
		for _, after := range c.After {
			parentID := vertexID(after.Name)
			edge := graphEdge{From: parentID, To: depID, Condition: after.Condition}
			if p := g.Vertices[parentID]; p != nil {
				p.Children[depID] = edge
			}
			g.Vertices[depID].Parents[parentID] = edge
		}
	}

	return g
}

// commandCount returns the number of real (non-virtual) vertices.
func (g *reconcileGraph) commandCount() int {
	n := 0
	for _, v := range g.Vertices {
		if v.Command != nil {
			n++
		}
	}
	return n
}

// stepStatus is the outcome of claiming a vertex from the work queue.
type stepStatus int

const (
	stepReady stepStatus = iota
	stepPending
	stepBlocked
	stepSkip
)

// edgeStatus is the result of evaluating a single dependency edge.
type edgeStatus int

const (
	edgeSatisfied edgeStatus = iota
	edgePending
	edgeBlocked
)

// walk traverses the graph in dir, running action on each ready vertex, until
// every in-closure command vertex is consumed or blocked. Bounded parallelism
// comes from errgroup.SetLimit; the queue is an unclosed buffered channel and
// completion is derived from graph state (no pending-work counter). When the
// walk drains, any still-unconsumed target vertex is marked blocked.
func (g *reconcileGraph) walk(
	ctx context.Context,
	dir walkDirection,
	limit int,
	action graphAction,
) {
	if limit < 1 {
		limit = 1
	}

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	workCh := make(chan vertexID, max(g.commandCount(), 1))

	for _, id := range g.seed(dir) {
		workCh <- id // buffered; distinct seeds <= capacity, never blocks
	}
	if g.done() {
		g.finalize()
		return
	}

	var eg errgroup.Group
	eg.SetLimit(limit)
	for range limit {
		eg.Go(func() error {
			for {
				select {
				case <-workCtx.Done():
					return nil
				case id := <-workCh:
					for _, n := range g.step(workCtx, id, dir, action) {
						select {
						case workCh <- n:
						case <-workCtx.Done():
							return nil
						}
					}
					if g.done() {
						g.finalize()
						cancel()
						return nil
					}
				}
			}
		})
	}
	_ = eg.Wait()
}

// step claims id and, when ready, runs action and returns the next vertices to
// enqueue. Skipped/pending/blocked claims enqueue nothing; pending vertices are
// re-enqueued by a later parent/child completion, and blocked vertices are
// resolved by finalize when the walk drains.
func (g *reconcileGraph) step(
	ctx context.Context,
	id vertexID,
	dir walkDirection,
	action graphAction,
) []vertexID {
	v, status := g.claim(id, dir)
	if status != stepReady {
		return nil
	}
	res := action(ctx, g, v)
	return g.complete(v.ID, res, dir)
}

// claim decides whether the task may act on id. It marks the vertex in-progress
// when ready, records the wait reason when pending, and records a dependency
// error when blocked — all atomically so termination detection cannot observe a
// half-updated frontier.
func (g *reconcileGraph) claim(id vertexID, dir walkDirection) (*graphVertex, stepStatus) {
	g.mu.Lock()
	defer g.mu.Unlock()

	v := g.Vertices[id]
	if v == nil || v.Command == nil || !v.InClosure || v.Consumed || v.InProgress {
		return v, stepSkip
	}

	st, reason := g.readinessLocked(v, dir)
	switch st {
	case edgeSatisfied:
		v.Queued = false
		v.InProgress = true
		return v, stepReady
	case edgePending:
		v.Queued = false
		v.WaitReason = reason
		return v, stepPending
	default: // edgeBlocked
		v.Queued = false
		v.Consumed = true
		v.Blocked = true
		v.WaitReason = reason
		if v.Err == nil {
			v.Err = errors.New(reason)
		}
		return v, stepBlocked
	}
}

// complete records an action's result and returns the next frontier to enqueue.
// Marking the vertex finished and queuing its frontier happen under one lock so
// done() never sees a vertex that is neither in-progress nor about to enqueue.
func (g *reconcileGraph) complete(id vertexID, res actionResult, dir walkDirection) []vertexID {
	g.mu.Lock()
	defer g.mu.Unlock()

	v := g.Vertices[id]
	v.InProgress = false
	v.Consumed = true
	g.markConsumedLocked(v)
	v.State = res.State
	v.ExitCode = res.ExitCode
	v.Err = res.Err

	var next []vertexID
	frontier, skip := v.Children, endVertex
	if dir == walkFromEnd {
		frontier, skip = v.Parents, beginVertex
	}
	for nid := range frontier {
		if nid == skip {
			continue
		}
		n := g.Vertices[nid]
		if n == nil || !n.InClosure || n.Consumed || n.InProgress || n.Queued {
			continue
		}
		n.Queued = true
		next = append(next, nid)
	}
	return next
}

// readinessLocked reports whether v may be acted on now. Caller holds g.mu.
//
// Down walk: every real parent must satisfy its edge condition. Up walk: every
// in-closure real child must already be consumed (dependents before deps).
func (g *reconcileGraph) readinessLocked(v *graphVertex, dir walkDirection) (edgeStatus, string) {
	if dir == walkFromEnd {
		for cid := range v.Children {
			if cid == endVertex {
				continue
			}
			if c := g.Vertices[cid]; c != nil && c.InClosure && !c.Consumed {
				return edgePending, fmt.Sprintf("waiting for %q to stop", cid)
			}
		}
		return edgeSatisfied, ""
	}

	worst := edgeSatisfied
	reason := ""
	for pid, edge := range v.Parents {
		if pid == beginVertex {
			continue
		}
		st, r := g.evalDownEdgeLocked(g.Vertices[pid], edge.Condition)
		switch st {
		case edgeBlocked:
			return edgeBlocked, r
		case edgePending:
			if worst == edgeSatisfied {
				worst, reason = edgePending, r
			}
		}
	}
	return worst, reason
}

// evalDownEdgeLocked evaluates one dependency edge for a down walk. Caller holds
// g.mu. An in-closure parent is judged by its current-run result (it must be
// consumed first); an out-of-closure parent is judged by its pre-run snapshot,
// since it will not run during this reconciliation.
func (g *reconcileGraph) evalDownEdgeLocked(
	p *graphVertex,
	cond AfterCondition,
) (edgeStatus, string) {
	if cond == "" {
		cond = ConditionCompleted
	}
	if p == nil {
		return edgeBlocked, "dependency missing from graph"
	}

	if p.InClosure {
		if p.Blocked {
			return edgeBlocked, fmt.Sprintf("dependency %q is blocked", p.ID)
		}
		if !p.Consumed {
			return edgePending, fmt.Sprintf("waiting for %q (%s)", p.ID, cond)
		}
		if p.Err != nil {
			return edgeBlocked, fmt.Sprintf("dependency %q failed: %v", p.ID, p.Err)
		}
		switch cond {
		case ConditionRunning:
			return edgeSatisfied, ""
		case ConditionCompleted:
			if isTerminalState(p.State) {
				return edgeSatisfied, ""
			}
			return edgePending, fmt.Sprintf("waiting for %q to complete", p.ID)
		default: // ConditionCompletedSuccessfully
			if p.State == model.EventTypeExited && p.ExitCode != nil && *p.ExitCode == 0 {
				return edgeSatisfied, ""
			}
			if isTerminalState(p.State) {
				return edgeBlocked, fmt.Sprintf(
					"dependency %q did not complete successfully (%s)",
					p.ID,
					exitDesc(p.State, p.ExitCode),
				)
			}
			return edgePending, fmt.Sprintf("waiting for %q to complete successfully", p.ID)
		}
	}

	// Out-of-closure dependency: judge by the pre-run snapshot only.
	snap := p.Snapshot
	switch cond {
	case ConditionRunning:
		if snap.State == model.EventTypeRunning || snap.State == model.EventTypeStarting {
			return edgeSatisfied, ""
		}
		return edgeBlocked, fmt.Sprintf(
			"dependency %q is not running and is outside the reconciliation set", p.ID)
	case ConditionCompleted:
		if isTerminalState(snap.State) {
			return edgeSatisfied, ""
		}
		return edgeBlocked, fmt.Sprintf(
			"dependency %q has not completed and is outside the reconciliation set", p.ID)
	default: // ConditionCompletedSuccessfully
		if snap.State == model.EventTypeExited && snap.ExitCode != nil && *snap.ExitCode == 0 {
			return edgeSatisfied, ""
		}
		return edgeBlocked, fmt.Sprintf(
			"dependency %q did not complete successfully (%s)",
			p.ID,
			exitDesc(snap.State, snap.ExitCode),
		)
	}
}

// anyDependentNeedsCompletion reports whether any in-closure dependent of id
// requires the command to terminate (condition completed or
// completed_successfully). The action uses this to decide whether to Wait.
func (g *reconcileGraph) anyDependentNeedsCompletion(id vertexID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	v := g.Vertices[id]
	if v == nil {
		return false
	}
	for cid, edge := range v.Children {
		if cid == endVertex {
			continue
		}
		c := g.Vertices[cid]
		if c == nil || !c.InClosure {
			continue
		}
		if edge.Condition == ConditionCompleted ||
			edge.Condition == ConditionCompletedSuccessfully {
			return true
		}
	}
	return false
}

// seed returns the initial frontier and marks it queued. Down: in-closure
// commands with no in-closure real dependency. Up: in-closure commands with no
// in-closure real dependent.
func (g *reconcileGraph) seed(dir walkDirection) []vertexID {
	g.mu.Lock()
	defer g.mu.Unlock()

	var seeds []vertexID
	for id, v := range g.Vertices {
		if v.Command == nil || !v.InClosure {
			continue
		}
		if g.hasInClosureRealNeighborLocked(v, dir) {
			continue
		}
		v.Queued = true
		seeds = append(seeds, id)
	}
	slices.Sort(seeds)
	return seeds
}

// hasInClosureRealNeighborLocked reports whether v has an in-closure real
// parent (down) or real child (up). Caller holds g.mu.
func (g *reconcileGraph) hasInClosureRealNeighborLocked(v *graphVertex, dir walkDirection) bool {
	neighbors, skip := v.Parents, beginVertex
	if dir == walkFromEnd {
		neighbors, skip = v.Children, endVertex
	}
	for nid := range neighbors {
		if nid == skip {
			continue
		}
		if n := g.Vertices[nid]; n != nil && n.InClosure {
			return true
		}
	}
	return false
}

// done reports whether no in-closure command vertex is queued or in progress,
// which means no worker can produce further work.
func (g *reconcileGraph) done() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, v := range g.Vertices {
		if v.Command == nil || !v.InClosure {
			continue
		}
		if v.Queued || v.InProgress {
			return false
		}
	}
	return true
}

// finalize marks every in-closure command vertex that the walk never consumed
// as blocked, attaching the last recorded wait reason. Idempotent.
func (g *reconcileGraph) finalize() {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, v := range g.Vertices {
		if v.Command == nil || !v.InClosure || v.Consumed {
			continue
		}
		v.Consumed = true
		g.markConsumedLocked(v)
		v.Blocked = true
		if v.Err == nil {
			reason := v.WaitReason
			if reason == "" {
				reason = "blocked by unsatisfied dependency conditions"
			}
			v.Err = errors.New(reason)
		}
	}
}

// startOutcomes extracts per-command StartOutcome in deterministic spec order
// for the in-closure commands.
func (g *reconcileGraph) startOutcomes(spec ComposeSpec) []StartOutcome {
	g.mu.Lock()
	defer g.mu.Unlock()

	var out []StartOutcome
	for i := range spec.Commands {
		name := spec.Commands[i].Name
		v := g.Vertices[vertexID(name)]
		if v == nil || !v.InClosure {
			continue
		}
		out = append(out, StartOutcome{Command: name, Err: v.Err})
	}
	return out
}

// stopOutcomes extracts per-command StopOutcome for the in-closure commands,
// ordered by the sequence in which the walk consumed them. For an up walk that
// is teardown (reverse-dependency) order: dependents before dependencies.
func (g *reconcileGraph) stopOutcomes(spec ComposeSpec) []StopOutcome {
	g.mu.Lock()
	defer g.mu.Unlock()

	type item struct {
		name  string
		order int
		err   error
	}
	var items []item
	for i := range spec.Commands {
		name := spec.Commands[i].Name
		v := g.Vertices[vertexID(name)]
		if v == nil || !v.InClosure {
			continue
		}
		items = append(items, item{name: name, order: v.Order, err: v.Err})
	}
	slices.SortStableFunc(items, func(a, b item) int { return a.order - b.order })

	out := make([]StopOutcome, 0, len(items))
	for _, it := range items {
		out = append(out, StopOutcome{Command: it.name, Err: it.err})
	}
	return out
}

// isTerminalState reports whether a state is one of the terminal states.
func isTerminalState(s model.EventType) bool {
	return s == model.EventTypeExited || s == model.EventTypeFailed
}

// exitDesc renders a terminal state plus exit code for diagnostics.
func exitDesc(state model.EventType, exit *int) string {
	if exit != nil {
		return fmt.Sprintf("state %s, exit code %d", state, *exit)
	}
	return fmt.Sprintf("state %s, exit code absent", state)
}
