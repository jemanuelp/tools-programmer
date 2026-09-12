package lab

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (r *Runtime) reconnect(ctx context.Context, between func() error) error {
	for i := 0; i < 2; i++ {
		if err := r.connectedEvent(ctx); err != nil {
			return err
		}
		if i == 0 {
			if err := between(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runtime) connectedEvent(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", r.origin+"/event?directory="+url.QueryEscape(r.root+"/repo"), nil)
	if err != nil {
		return errors.New("sse_request")
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := r.client.Do(req)
	if err != nil {
		return errors.New("sse_transport")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("sse_status")
	}
	media, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if err != nil || media != "text/event-stream" {
		return errors.New("sse_content_type")
	}
	// Bound the entire handshake, including comments and ignored fields, to
	// 64 KiB and 32 frames. The existing client/case deadlines bound reads.
	reader := bufio.NewReader(io.LimitReader(res.Body, 64<<10))
	data := ""
	for frames := 0; frames < 32; {
		line, err := reader.ReadString('\n')
		if err != nil {
			return errors.New("sse_incomplete_or_bound")
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line != "" {
			field, value, _ := strings.Cut(line, ":")
			if field == "data" {
				data += strings.TrimPrefix(value, " ") + "\n"
			}
			continue
		}
		frames++
		if data == "" {
			continue
		}
		// Pinned SDK EventServerConnected: type plus object properties.
		// Require a complete native connection event, not merely SSE headers.
		var event struct {
			Type       string         `json:"type"`
			Properties map[string]any `json:"properties"`
		}
		if json.Unmarshal([]byte(data), &event) != nil || event.Type == "" || event.Properties == nil {
			return errors.New("sse_event")
		}
		if event.Type == "server.connected" {
			return ctx.Err()
		}
		data = ""
	}
	return errors.New("sse_event_bound")
}

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
	var before []byte
	if err := f.runtime.reconnect(f.ctx, func() error {
		a, err := f.summary(1)
		if err == nil {
			before, err = json.Marshal(a)
		}
		return err
	}); err != nil {
		return err
	}
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
