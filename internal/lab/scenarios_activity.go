package lab

import (
	"context"
	"errors"
	"time"
)

func (f *fixture) assign(session, turn string, issue int) error {
	f.runtime.provider.add(turn, assignment(issue))
	if err := f.runtime.prompt(session, turn); err != nil {
		return err
	}
	return f.runtime.complete(turn)
}
func (f *fixture) assigned() (string, error) {
	if err := f.start(false); err != nil {
		return "", err
	}
	id, err := f.runtime.session()
	if err != nil {
		return "", err
	}
	return id, f.assign(id, "assign", 1)
}

type permissionRequest struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionID"`
	Permission string `json:"permission"`
}

func (f *fixture) pending(session string) (permissionRequest, error) {
	var request permissionRequest
	err := wait(f.ctx, func() (bool, error) {
		var pending []permissionRequest
		if err := f.runtime.request(f.ctx, "GET", "/permission", nil, &pending); err != nil {
			return false, err
		}
		if len(pending) > 8 {
			return false, errors.New("permission_bound")
		}
		for _, p := range pending {
			if p.SessionID == session && p.Permission == "tools_programmer_assign" && nativeID(p.ID) {
				request = p
				return true, nil
			}
		}
		if e := f.runtime.provider.failure(); e != "" {
			return false, errors.New(e)
		}
		return false, nil
	})
	return request, err
}
func (f *fixture) reply(p permissionRequest, allow bool) error {
	reply := "reject"
	if allow {
		reply = "once"
	}
	return f.runtime.request(f.ctx, "POST", "/permission/"+p.ID+"/reply", map[string]string{"reply": reply}, nil)
}

func (f *fixture) humanReply(p permissionRequest, r *Result, decide func(context.Context) (bool, error)) error {
	if !f.interactive {
		r.Status = "blocked"
		return errors.New("human_decision_required")
	}
	allow, err := decide(f.ctx)
	// Recheck after the decision: cancellation can race with an available answer.
	// Do not reclassify transport failures after a reply has been attempted.
	if contextErr := f.ctx.Err(); contextErr != nil {
		err = contextErr
	}
	if errors.Is(err, context.DeadlineExceeded) {
		r.Status = "blocked"
		return errors.New("human_decision_timeout")
	}
	if errors.Is(err, context.Canceled) {
		r.Status = "blocked"
		return errors.New("human_decision_cancelled")
	}
	if err != nil {
		return err
	}
	if err := f.reply(p, allow); err != nil {
		return err
	}
	if !allow {
		r.Status = "blocked"
		return errors.New("human_declined")
	}
	return nil
}

func freshHuman(f *fixture, r *Result) error {
	if err := f.start(true); err != nil {
		return err
	}
	id, err := f.runtime.session()
	if err != nil {
		return err
	}
	f.runtime.provider.add("human", assignment(1), []Call{sleepCall(3)})
	if err := f.runtime.prompt(id, "human"); err != nil {
		return err
	}
	p, err := f.pending(id)
	if err != nil {
		return err
	}
	if err := r.check("actual_permission_requested", p.ID != "", 1, 1); err != nil {
		return err
	}
	var start time.Time
	if err := f.humanReply(p, r, func(ctx context.Context) (bool, error) {
		allow, err := humanDecision(ctx)
		start = time.Now()
		return allow, err
	}); err != nil {
		return err
	}
	if err := f.runtime.complete("human"); err != nil {
		return err
	}
	if err := f.sleepEvidence(r, id, 3, time.Since(start).Milliseconds()); err != nil {
		return err
	}
	parts, err := f.runtime.parts(id)
	if err != nil {
		return err
	}
	assigns := 0
	for _, p := range parts {
		if p.Tool == "tools_programmer_assign" && p.State.Status == "completed" {
			assigns++
		}
	}
	if err := r.check("assignment_exactly_once", assigns == 1, int64(assigns), 1); err != nil {
		return err
	}
	return f.parity(r, 1)
}
func permissionDenial(f *fixture, r *Result) error {
	if err := f.start(true); err != nil {
		return err
	}
	id, err := f.runtime.session()
	if err != nil {
		return err
	}
	f.runtime.provider.add("denied", assignment(1), []Call{sleepCall(3)})
	if err := f.runtime.prompt(id, "denied"); err != nil {
		return err
	}
	p, err := f.pending(id)
	if err != nil {
		return err
	}
	if err := f.reply(p, false); err != nil {
		return err
	}
	if err := wait(f.ctx, func() (bool, error) {
		parts, err := f.runtime.parts(id)
		if err != nil {
			return false, err
		}
		for _, p := range parts {
			if p.Tool == "tools_programmer_assign" && p.State.Status == "error" {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	parts, err := f.runtime.parts(id)
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.Tool == "bash" {
			return errors.New("denied_assignment_advanced")
		}
	}
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	return r.check("denial_no_assigned_work", len(s.Segments) == 0 && s.Activity.ActiveElapsedMS == 0, s.Activity.ActiveElapsedMS, 0)
}

func (f *fixture) sleepEvidence(r *Result, session string, seconds int, monotonic int64) error {
	parts, err := f.runtime.parts(session)
	if err != nil {
		return err
	}
	count := 0
	var runtimeMS int64
	for _, p := range parts {
		if p.Tool == "bash" && p.State.Status == "completed" {
			count++
			runtimeMS += p.State.Time.End - p.State.Time.Start
		}
	}
	if err := r.check("sleep_exactly_once", count == 1, int64(count), 1); err != nil {
		return err
	}
	expected := int64(seconds * 1000)
	if err := r.check("monotonic_sleep", monotonic >= expected && monotonic <= expected+1500, monotonic, expected); err != nil {
		return err
	}
	events, _, err := f.events(1)
	if err != nil {
		return err
	}
	starts := map[string]int64{}
	var toolMS int64
	for _, e := range events {
		if e.Session != session || e.Category != "tool" {
			continue
		}
		if e.Kind == "start" {
			starts[e.Activity] = e.At
		}
		if e.Kind == "stop" {
			if start, ok := starts[e.Activity]; ok {
				toolMS += e.At - start
			}
		}
	}
	if err := r.check("automatic_tool_duration", toolMS >= expected && abs(toolMS-runtimeMS) <= 500, toolMS, runtimeMS); err != nil {
		return err
	}
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	return r.check("assigned_sleep_accounted", s.Activity.ActiveElapsedMS >= expected && s.Activity.ActiveElapsedMS <= monotonic+500, s.Activity.ActiveElapsedMS, expected)
}
func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
func silent(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	f.runtime.provider.add("silent", []Call{sleepCall(3)})
	start := time.Now()
	if err := f.runtime.prompt(id, "silent"); err != nil {
		return err
	}
	if err := f.runtime.complete("silent"); err != nil {
		return err
	}
	if err := f.sleepEvidence(r, id, 3, time.Since(start).Milliseconds()); err != nil {
		return err
	}
	return f.parity(r, 1)
}

func waitOverlap(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	// A genuine pending question overlaps an independent silent tool. Rejecting
	// the fixture question tests denial, not an invented human answer.
	question := Call{"question", map[string]any{"questions": []any{map[string]any{"question": "Continue the fixture?", "header": "Fixture", "options": []any{map[string]string{"label": "Continue", "description": "Continue this fixture"}}}}}}
	f.runtime.provider.add("overlap", []Call{question, sleepCall(4)})
	if err := f.runtime.prompt(id, "overlap"); err != nil {
		return err
	}
	var qid string
	start := time.Now()
	if err := wait(f.ctx, func() (bool, error) {
		var q []struct{ ID, SessionID string }
		if err := f.runtime.request(f.ctx, "GET", "/question", nil, &q); err != nil {
			return false, err
		}
		for _, item := range q {
			if item.SessionID == id && nativeID(item.ID) {
				qid = item.ID
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	// Wait for the independent tool's actual terminal event while the question is
	// still pending, rather than assuming its full question window is overlap.
	if err := wait(f.ctx, func() (bool, error) {
		parts, err := f.runtime.parts(id)
		if err != nil {
			return false, err
		}
		for _, p := range parts {
			if p.Tool == "bash" && p.State.Status == "completed" {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("independent_work_during_wait", s.Activity.ActiveElapsedMS >= 3500 && s.Activity.ActiveElapsedMS <= time.Since(start).Milliseconds()+500, s.Activity.ActiveElapsedMS, 4000); err != nil {
		return err
	}
	events, _, err := f.events(1)
	if err != nil {
		return err
	}
	var waitAt, toolStart, toolEnd int64
	for _, e := range events {
		if e.Session != id {
			continue
		}
		if e.Category == "question" && e.Kind == "start" {
			waitAt = e.At
		}
		if e.Category == "tool" && e.Kind == "start" && e.At > toolStart {
			toolStart = e.At
		}
		if e.Category == "tool" && e.Kind == "stop" && e.At > toolEnd {
			toolEnd = e.At
		}
	}
	overlap := toolEnd - max(waitAt, toolStart)
	if err := r.check("positive_observed_overlap", waitAt > 0 && overlap > 0, overlap, 1); err != nil {
		return err
	}
	if err := f.runtime.request(f.ctx, "POST", "/question/"+qid+"/reject", map[string]any{}, nil); err != nil {
		return err
	}
	return nil
}

func retryScenario(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	f.runtime.provider.add("retry", []Call{sleepCall(1)})
	f.runtime.provider.mu.Lock()
	f.runtime.provider.turns["retry"].Retry = true
	f.runtime.provider.mu.Unlock()
	if err := f.runtime.prompt(id, "retry"); err != nil {
		return err
	}
	if err := f.runtime.complete("retry"); err != nil {
		return err
	}
	_, retryMS := f.runtime.provider.timing("retry")
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("raw_provider_backoff", retryMS >= 1500, retryMS, 2000); err != nil {
		return err
	}
	events, _, err := f.events(1)
	if err != nil {
		return err
	}
	var start, end int64
	for _, e := range events {
		if e.Session == id && e.Category == "retry" {
			if e.Kind == "start" {
				start = e.At
			}
			if e.Kind == "stop" {
				end = e.At
			}
		}
	}
	if err := r.check("retry_separate_interval", start > 0 && end > start && abs(end-start-retryMS) <= 750, end-start, retryMS); err != nil {
		return err
	}
	return r.check("retry_not_active", s.Activity.ActiveElapsedMS >= 1000 && s.Activity.ActiveElapsedMS < 2000, s.Activity.ActiveElapsedMS, 1000)
}

func boundedPause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return errors.New("deadline")
	}
}

func permissionOverlap(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	// Session-scoped ask overrides only B. The existing A assignment is fixture
	// setup; the second request is genuinely pending and is never autoapproved.
	policy := map[string]any{"permission": []any{map[string]string{"permission": "tools_programmer_assign", "pattern": "lab#2", "action": "ask"}}}
	if err := f.runtime.request(f.ctx, "PATCH", "/session/"+id, policy, nil); err != nil {
		return err
	}
	f.runtime.provider.add("permission-overlap", []Call{assignment(2)[0], sleepCall(4)})
	if err := f.runtime.prompt(id, "permission-overlap"); err != nil {
		return err
	}
	pending, err := f.pending(id)
	if err != nil {
		return err
	}
	if err := wait(f.ctx, func() (bool, error) {
		parts, err := f.runtime.parts(id)
		if err != nil {
			return false, err
		}
		for _, p := range parts {
			if p.Tool == "bash" && p.State.Status == "completed" {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		return err
	}
	events, _, err := f.events(1)
	if err != nil {
		return err
	}
	var waitAt, toolStart, toolEnd int64
	for _, e := range events {
		if e.Session != id {
			continue
		}
		if e.Category == "permission" && e.Kind == "start" {
			waitAt = e.At
		}
		if e.Category == "tool" && e.Kind == "start" && e.At > toolStart {
			toolStart = e.At
		}
		if e.Category == "tool" && e.Kind == "stop" && e.At > toolEnd {
			toolEnd = e.At
		}
	}
	overlap := toolEnd - max(toolStart, waitAt)
	if err := r.check("permission_positive_overlap", waitAt > 0 && overlap > 0, overlap, 1); err != nil {
		return err
	}
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("permission_independent_work_counted", s.Activity.ActiveElapsedMS >= 3500, s.Activity.ActiveElapsedMS, 4000); err != nil {
		return err
	}
	if err := f.reply(pending, false); err != nil {
		return err
	}
	s, err = f.summary(2)
	if err != nil {
		return err
	}
	return r.check("permission_denial_keeps_a", len(s.Segments) == 0, int64(len(s.Segments)), 0)
}
