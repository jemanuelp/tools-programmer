package lab

import (
	"encoding/json"
	"errors"
	"time"
)

func (f *fixture) startLong() (string, error) {
	id, err := f.assigned()
	if err != nil {
		return "", err
	}
	f.runtime.provider.add("long", []Call{sleepCall(20)})
	if err := f.runtime.prompt(id, "long"); err != nil {
		return "", err
	}
	err = wait(f.ctx, func() (bool, error) {
		parts, err := f.runtime.parts(id)
		if err != nil {
			return false, err
		}
		for _, p := range parts {
			if p.Tool == "bash" && p.State.Status == "running" {
				return true, nil
			}
		}
		return false, nil
	})
	return id, err
}

func cancelScenario(f *fixture, r *Result) error {
	id, err := f.startLong()
	if err != nil {
		return err
	}
	if err := boundedPause(f.ctx, 500*time.Millisecond); err != nil {
		return err
	}
	if err := f.runtime.request(f.ctx, "POST", "/session/"+id+"/abort", nil, nil); err != nil {
		return err
	}
	if err := wait(f.ctx, func() (bool, error) {
		var status map[string]struct{ Type string }
		if err := f.runtime.request(f.ctx, "GET", "/session/status", nil, &status); err != nil {
			return false, err
		}
		s, ok := status[id]
		return !ok || s.Type == "idle", nil
	}); err != nil {
		return err
	}
	if err := boundedPause(f.ctx, 300*time.Millisecond); err != nil {
		return err
	}
	a, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("cancel_not_full_sleep", a.Activity.ActiveElapsedMS < 5000, a.Activity.ActiveElapsedMS, 5000); err != nil {
		return err
	}
	if err := boundedPause(f.ctx, 500*time.Millisecond); err != nil {
		return err
	}
	b, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("cancel_no_now_extension", a.Activity.ActiveElapsedMS == b.Activity.ActiveElapsedMS, b.Activity.ActiveElapsedMS, a.Activity.ActiveElapsedMS); err != nil {
		return err
	}
	parts, err := f.runtime.parts(id)
	if err != nil {
		return err
	}
	terminal := false
	for _, p := range parts {
		if p.Tool == "bash" && p.State.Status == "error" {
			terminal = true
		}
	}
	return r.check("cancel_terminal_or_unknown", terminal || len(b.Activity.Unknown) > 0, 0, 0)
}

func restartScenario(f *fixture, r *Result) error {
	id, err := f.startLong()
	if err != nil {
		return err
	}
	if err := boundedPause(f.ctx, 500*time.Millisecond); err != nil {
		return err
	}
	// Kill only our process group, including its sleep. Persisted running parts
	// must not be treated as a process surviving this server restart.
	origin := f.runtime.origin
	f.runtime.process.stop()
	a, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("missing_terminal_unknown", len(a.Activity.Unknown) > 0, int64(len(a.Activity.Unknown)), 1); err != nil {
		return err
	}
	if err := boundedPause(f.ctx, time.Second); err != nil {
		return err
	}
	b, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("offline_gap_not_active", a.Activity.ActiveElapsedMS == b.Activity.ActiveElapsedMS, b.Activity.ActiveElapsedMS, a.Activity.ActiveElapsedMS); err != nil {
		return err
	}
	// The same port, fixture repo, persisted runtime state, database and session
	// preserve the product namespace; no reassignment request is sent.
	if err := f.runtime.start(); err != nil {
		return err
	}
	if err := r.check("restart_same_origin", f.runtime.origin == origin, 0, 0); err != nil {
		return err
	}
	f.runtime.provider.add("after-restart", []Call{sleepCall(1)})
	if err := f.runtime.prompt(id, "after-restart"); err != nil {
		return err
	}
	if err := f.runtime.complete("after-restart"); err != nil {
		return err
	}
	b, err = f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("restart_preserves_unknown", len(b.Activity.Unknown) >= len(a.Activity.Unknown), int64(len(b.Activity.Unknown)), int64(len(a.Activity.Unknown))); err != nil {
		return err
	}
	if err := r.check("restart_counts_only_new_work", b.Activity.ActiveElapsedMS >= 1000 && b.Activity.ActiveElapsedMS < 4000, b.Activity.ActiveElapsedMS, 1000); err != nil {
		return err
	}
	_, segments, err := f.events(1)
	if err != nil {
		return err
	}
	same := 0
	for _, s := range segments {
		if s.Session == id {
			same++
		}
	}
	if err := r.check("restart_fresh_segment", same >= 2, int64(same), 2); err != nil {
		return err
	}
	return f.parity(r, 1)
}

func reconnectScenario(f *fixture, r *Result) error {
	id, err := f.assigned()
	if err != nil {
		return err
	}
	// The plugin subscribes to the server event bridge, not this external SSE
	// connection. A UI transport reconnect alone must not invent a recovery gap.
	res, err := f.runtime.client.Get(f.runtime.origin + "/event")
	if err != nil {
		return errors.New("sse_connect")
	}
	res.Body.Close()
	a, err := f.summary(1)
	if err != nil {
		return err
	}
	before, _ := json.Marshal(a)
	res, err = f.runtime.client.Get(f.runtime.origin + "/event")
	if err != nil {
		return errors.New("sse_reconnect")
	}
	res.Body.Close()
	b, err := f.summary(1)
	if err != nil {
		return err
	}
	after, _ := json.Marshal(b)
	if err := r.check("ui_reconnect_no_fabricated_gap", string(before) == string(after), 0, 0); err != nil {
		return err
	}
	f.runtime.provider.add("after-reconnect", []Call{sleepCall(1)})
	if err := f.runtime.prompt(id, "after-reconnect"); err != nil {
		return err
	}
	if err := f.runtime.complete("after-reconnect"); err != nil {
		return err
	}
	b, err = f.summary(1)
	if err != nil {
		return err
	}
	return r.check("observer_survives_ui_reconnect", b.Activity.ActiveElapsedMS >= 1000, b.Activity.ActiveElapsedMS, 1000)
}
