package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

type interval struct{ start, end int64 }

func unionLength(spans []interval) int64 {
	if len(spans) == 0 {
		return 0
	}
	copySpans := append([]interval{}, spans...)
	sort.Slice(copySpans, func(i, j int) bool { return copySpans[i].start < copySpans[j].start })
	current := copySpans[0]
	var total int64
	for _, s := range copySpans[1:] {
		if s.start > current.end {
			total += current.end - current.start
			current = s
		} else if s.end > current.end {
			current.end = s.end
		}
	}
	return total + current.end - current.start
}
func (f *fixture) children(parent string) ([]string, error) {
	parts, err := f.runtime.parts(parent)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, p := range parts {
		if p.Tool == "task" && nativeID(p.State.Metadata.SessionID) && p.State.Metadata.ParentID == parent && !seen[p.State.Metadata.SessionID] {
			ids = append(ids, p.State.Metadata.SessionID)
			seen[p.State.Metadata.SessionID] = true
		}
	}
	return ids, nil
}
func (f *fixture) childRunning(parent string) (string, error) {
	var id string
	err := wait(f.ctx, func() (bool, error) {
		ids, err := f.children(parent)
		if err != nil {
			return false, err
		}
		for _, child := range ids {
			parts, err := f.runtime.parts(child)
			if err != nil {
				return false, err
			}
			for _, p := range parts {
				if p.Tool == "bash" && p.State.Status == "running" {
					id = child
					return true, nil
				}
			}
		}
		return false, nil
	})
	return id, err
}

func parallelChildren(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	f.runtime.provider.add("left", []Call{sleepCall(3)})
	f.runtime.provider.add("right", []Call{sleepCall(3)})
	f.runtime.provider.add("parallel", []Call{taskCall("left", false, ""), taskCall("right", false, "")})
	start := time.Now()
	if err := f.runtime.prompt(id, "parallel"); err != nil {
		return err
	}
	if err := f.runtime.complete("parallel"); err != nil {
		return err
	}
	if err := f.runtime.provider.assertComplete("left"); err != nil {
		return err
	}
	if err := f.runtime.provider.assertComplete("right"); err != nil {
		return err
	}
	children, err := f.children(id)
	if err != nil {
		return err
	}
	if err := r.check("two_actual_children", len(children) == 2, int64(len(children)), 2); err != nil {
		return err
	}
	var spans []interval
	var sum int64
	for _, child := range children {
		parts, err := f.runtime.parts(child)
		if err != nil {
			return err
		}
		for _, p := range parts {
			if p.Tool == "bash" && p.State.Status == "completed" {
				x := interval{p.State.Time.Start, p.State.Time.End}
				spans = append(spans, x)
				sum += x.end - x.start
			}
		}
	}
	if len(spans) != 2 {
		return errors.New("two_child_tools_required")
	}
	union := unionLength(spans)
	if err := r.check("actual_child_overlap", sum-union >= 1500, sum-union, 1500); err != nil {
		return err
	}
	if err := r.check("parallel_monotonic_bound", time.Since(start) < 6*time.Second, time.Since(start).Milliseconds(), 6000); err != nil {
		return err
	}
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("elapsed_union", abs(s.Activity.ActiveElapsedMS-union) <= 750, s.Activity.ActiveElapsedMS, union); err != nil {
		return err
	}
	if err := r.check("summed_child_time", abs(s.Activity.AgentMS-sum) <= 750, s.Activity.AgentMS, sum); err != nil {
		return err
	}
	return f.parity(r, 1)
}

func switchReuse(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	f.runtime.provider.add("child-a", []Call{sleepCall(1)})
	f.runtime.provider.add("parent-a", []Call{taskCall("child-a", false, "")})
	if err := f.runtime.prompt(id, "parent-a"); err != nil {
		return err
	}
	if err := f.runtime.complete("parent-a"); err != nil {
		return err
	}
	children, err := f.children(id)
	if err != nil || len(children) != 1 {
		return errors.New("reuse_child_metadata")
	}
	if err := f.runtime.provider.assertComplete("child-a"); err != nil {
		return err
	}
	read := func() (issueHistorySnapshot, error) {
		ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
		defer cancel()
		view := *f
		view.ctx = ctx
		return settleHistory(ctx, view.issueAHistory, func() (bool, error) { return f.runtime.quiescent(ctx, id, children[0]) })
	}
	err = reuseHistory(r, read, func() error { return f.assign(id, "switch-b", 2) }, func() error {
		f.runtime.provider.add("child-b", []Call{sleepCall(1)})
		f.runtime.provider.add("parent-b", []Call{taskCall("child-b", false, children[0])})
		if err := f.runtime.prompt(id, "parent-b"); err != nil {
			return err
		}
		if err := f.runtime.complete("parent-b"); err != nil {
			return err
		}
		return f.runtime.provider.assertComplete("child-b")
	})
	if err != nil {
		return err
	}
	_, segmentsA, err := f.events(1)
	if err != nil {
		return err
	}
	_, segmentsB, err := f.events(2)
	if err != nil {
		return err
	}
	var aChild, bChild bool
	for _, s := range segmentsA {
		if s.Session == children[0] {
			aChild = true
		}
	}
	for _, s := range segmentsB {
		if s.Session == children[0] {
			bChild = true
		}
	}
	if err := r.check("same_child_distinct_assignments", aChild && bChild, 0, 0); err != nil {
		return err
	}
	return f.parity(r, 2)
}

type issueRecords struct {
	Events   map[string]json.RawMessage `json:"activity_events"`
	Bindings map[string]json.RawMessage `json:"bindings"`
}
type issueHistorySnapshot struct {
	Summary json.RawMessage
	Records issueRecords
}

func (f *fixture) issueAHistory() (issueHistorySnapshot, error) {
	var snapshot issueHistorySnapshot
	a, err := f.summary(1)
	if err != nil {
		return snapshot, err
	}
	snapshot.Summary, err = json.Marshal(a)
	if err != nil {
		return snapshot, err
	}
	events, segments, err := f.events(1)
	if err != nil {
		return snapshot, err
	}
	snapshot.Records = issueRecords{Events: map[string]json.RawMessage{}, Bindings: map[string]json.RawMessage{}}
	for _, event := range events {
		b, err := json.Marshal(event)
		if err != nil {
			return snapshot, err
		}
		snapshot.Records.Events[event.Runtime+"\x00"+event.ID] = b
	}
	for _, segment := range segments {
		// Only persisted binding fields participate. LEAD-derived accounting
		// boundaries may legitimately change when the parent switches issues.
		segment.Until = nil
		segment.AccountingUntil = nil
		b, err := json.Marshal(segment)
		if err != nil {
			return snapshot, err
		}
		snapshot.Records.Bindings[segment.Runtime+"\x00"+segment.Session+"\x00"+segment.Segment] = b
	}
	return snapshot, nil
}

// Two equal readbacks while both native turns are quiescent establish a bounded
// bookmark. This is observation polling, not a delay or retry-until-PASS policy.
func settleHistory(ctx context.Context, read func() (issueHistorySnapshot, error), quiet func() (bool, error)) (issueHistorySnapshot, error) {
	var result issueHistorySnapshot
	var previous []byte
	err := wait(ctx, func() (bool, error) {
		ok, err := quiet()
		if err != nil {
			return false, err
		}
		if !ok {
			previous = nil
			return false, nil
		}
		current, err := read()
		if err != nil {
			return false, err
		}
		encoded, err := json.Marshal(current)
		if err != nil {
			return false, err
		}
		stable := previous != nil && bytes.Equal(previous, encoded)
		previous = encoded
		result = current
		return stable, nil
	})
	if err != nil && ctx.Err() != nil {
		return result, errors.New("reuse_history_not_settled")
	}
	return result, err
}

func originalRecords(current, original issueRecords) issueRecords {
	subset := func(all, keys map[string]json.RawMessage) map[string]json.RawMessage {
		if keys == nil {
			return nil
		}
		out := map[string]json.RawMessage{}
		for k := range keys {
			if v, ok := all[k]; ok {
				out[k] = v
			}
		}
		return out
	}
	return issueRecords{Events: subset(current.Events, original.Events), Bindings: subset(current.Bindings, original.Bindings)}
}

func reuseHistory(r *Result, read func() (issueHistorySnapshot, error), switchParent, reuseChild func() error) error {
	prior, err := read()
	if err != nil {
		return err
	}
	if err := switchParent(); err != nil {
		return err
	}
	// The switch itself can add A work before its B binding. Freeze the complete
	// A summary only after that turn settles, and before dispatching B's child.
	before, err := read()
	if err != nil {
		return err
	}
	old, _ := json.Marshal(prior.Records)
	preserved, _ := json.Marshal(originalRecords(before.Records, prior.Records))
	if err := compareHistory(r, "issue_a_records_preserved_during_switch", old, preserved); err != nil {
		return err
	}
	if err := reuseChild(); err != nil {
		return err
	}
	after, err := read()
	if err != nil {
		return err
	}
	if err := compareHistory(r, "issue_a_history_stable", before.Summary, after.Summary); err != nil {
		return err
	}
	beforeRecords, _ := json.Marshal(before.Records)
	afterRecords, _ := json.Marshal(after.Records)
	return compareHistory(r, "issue_a_records_stable", beforeRecords, afterRecords)
}

func backgroundScenario(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	f.runtime.provider.add("background-child", []Call{sleepCall(4)})
	f.runtime.provider.add("launch-background", []Call{taskCall("background-child", true, "")})
	if err := f.runtime.prompt(id, "launch-background"); err != nil {
		return err
	}
	child, err := f.childRunning(id)
	if err != nil {
		return err
	}
	if err := f.runtime.complete("launch-background"); err != nil {
		return err
	}
	if err := f.assign(id, "background-switch", 2); err != nil {
		return err
	}
	// The child must still be running after the issue switch, not merely finish
	// before B. This is independent of the observer's inherited payload fields.
	parts, err := f.runtime.parts(child)
	if err != nil {
		return err
	}
	running := false
	for _, p := range parts {
		running = running || p.Tool == "bash" && p.State.Status == "running"
	}
	if err := r.check("background_crosses_switch", running, 0, 0); err != nil {
		return err
	}
	if err := wait(f.ctx, func() (bool, error) {
		if e := f.runtime.provider.failure(); e != "" {
			return false, errors.New(e)
		}
		return f.runtime.provider.completed("background-child"), nil
	}); err != nil {
		return err
	}
	parts, err = f.runtime.parts(id)
	if err != nil {
		return err
	}
	job := false
	for _, p := range parts {
		if p.Tool == "task" && p.State.Metadata.SessionID == child && p.State.Metadata.JobID == child && p.State.Metadata.Background {
			job = true
		}
	}
	if err := r.check("actual_background_job", job, 0, 0); err != nil {
		return err
	}
	_, a, err := f.events(1)
	if err != nil {
		return err
	}
	_, b, err := f.events(2)
	if err != nil {
		return err
	}
	inA, inB := false, false
	for _, s := range a {
		inA = inA || s.Session == child
	}
	for _, s := range b {
		inB = inB || s.Session == child
	}
	if err := r.check("dispatch_snapshot_keeps_a", inA && !inB, 0, 0); err != nil {
		return err
	}
	// A second quiescent invocation proves background child reuse as well.
	if err := f.runtime.reuseBackground(f.ctx, id, child); err != nil {
		return err
	}
	if err := f.runtime.complete("reuse-parent"); err != nil {
		return err
	}
	_, b, err = f.events(2)
	if err != nil {
		return err
	}
	inB = false
	for _, s := range b {
		inB = inB || s.Session == child
	}
	if err := r.check("background_reuse_b", inB, 0, 0); err != nil {
		return err
	}
	return f.parity(r, 2)
}

func (r *Runtime) reuseBackground(ctx context.Context, parent, child string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := wait(ctx, func() (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if e := r.provider.failure(); e != "" {
			return false, errors.New(e)
		}
		return r.quiescent(ctx, parent, child)
	}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.provider.add("background-reuse", []Call{sleepCall(1)})
	r.provider.add("reuse-parent", []Call{taskCall("background-reuse", false, child)})
	view := &Runtime{ctx: ctx, root: r.root, origin: r.origin, client: r.client}
	return view.prompt(parent, "reuse-parent")
}
