package core

import (
	"encoding/json"
	"regexp"
	"sort"
)

// ActivityEvent is an allowlisted wire format, not an envelope for runtime payloads.
// Times are Unix milliseconds. A stop may arrive before its start.
type ActivityEvent struct {
	ID            string `json:"id"`
	Runtime       string `json:"runtime"`
	Session       string `json:"session"`
	Segment       string `json:"segment"`
	Kind          string `json:"kind"`
	At            int64  `json:"at"`
	Activity      string `json:"activity,omitempty"`
	Category      string `json:"category,omitempty"`
	Target        string `json:"target,omitempty"`
	Project       string `json:"project,omitempty"`
	Issue         int    `json:"issue,omitempty"`
	ParentSession string `json:"parent_session,omitempty"`
	ParentSegment string `json:"parent_segment,omitempty"`
	DispatchAt    *int64 `json:"dispatch_at,omitempty"`
	Resume        bool   `json:"resume,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type ActivitySegment struct {
	Runtime string `json:"runtime"`
	Session string `json:"session"`
	Segment string `json:"segment"`
	At      int64  `json:"at"`
	Until   *int64 `json:"until,omitempty"`
	IssueID *int64 `json:"issue_id"`
	// AccountingUntil clips intervals at the next native-session segment
	// without exposing a later issue's history in this issue's JSON.
	AccountingUntil *int64 `json:"-"`
}

type ObservationGap struct {
	Runtime  string `json:"runtime"`
	Session  string `json:"session"`
	Segment  string `json:"segment"`
	Activity string `json:"activity,omitempty"`
	From     int64  `json:"from"`
	To       *int64 `json:"to"`
	Reason   string `json:"reason"`
}

type ActivityReport struct {
	ActiveElapsedMS int64            `json:"active_elapsed_ms"`
	AgentMS         int64            `json:"agent_ms"`
	RetryElapsedMS  int64            `json:"retry_elapsed_ms"`
	RetryAgentMS    int64            `json:"retry_agent_ms"`
	Unknown         []ObservationGap `json:"unknown_gaps"`
	PerAgentMS      map[string]int64 `json:"per_agent_ms"`
	Basis           string           `json:"basis"`
}

var runtimeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$`)

func (e ActivityEvent) Validate() error {
	bad := func() error { return Fail("invalid_event", "invalid normalized activity event") }
	for _, v := range []string{e.ID, e.Runtime, e.Session, e.Segment} {
		if !runtimeID.MatchString(v) {
			return bad()
		}
	}
	if e.At < 0 || e.At > 4102444800000 {
		return bad()
	}
	if e.Kind == "bind" {
		if e.Activity != "" || e.Category != "" || e.Target != "" || e.Reason != "" {
			return bad()
		}
		if (e.ParentSession == "") != (e.ParentSegment == "") {
			return bad()
		}
		if e.ParentSession != "" {
			if !runtimeID.MatchString(e.ParentSession) || !runtimeID.MatchString(e.ParentSegment) || e.ParentSession == e.Session || e.Project != "" || e.Issue != 0 {
				return bad()
			}
			if e.DispatchAt != nil && (*e.DispatchAt < 0 || *e.DispatchAt > e.At) {
				return bad()
			}
		} else if (e.Project == "") != (e.Issue == 0) || (e.Project != "" && (!ValidName(e.Project) || !ValidPR(e.Issue))) {
			return bad()
		}
		if e.ParentSession == "" && e.DispatchAt != nil {
			return bad()
		}
		if e.Resume && (e.ParentSession != "" || e.Project != "" || e.Issue != 0) {
			return bad()
		}
		return nil
	}
	if e.Project != "" || e.Issue != 0 || e.ParentSession != "" || e.ParentSegment != "" || e.DispatchAt != nil || e.Resume {
		return bad()
	}
	if e.Kind == "gap" {
		if e.Activity != "" || e.Category != "" || e.Target != "" {
			return bad()
		}
		switch e.Reason {
		case "observer_restart", "event_loss", "uncertain_inference", "uncorrelated_child", "cancelled", "reconnected", "unsupported_lifecycle":
			return nil
		}
		return bad()
	}
	if (e.Kind != "start" && e.Kind != "stop") || !runtimeID.MatchString(e.Activity) || e.Reason != "" {
		return bad()
	}
	switch e.Category {
	case "tool", "inference", "retry", "permission", "question", "delegation":
	default:
		return bad()
	}
	if e.Target != "" && (!runtimeID.MatchString(e.Target) || (e.Category != "permission" && e.Category != "question")) {
		return bad()
	}
	return nil
}

type interval struct{ start, end int64 }

func union(in []interval) []interval {
	sort.Slice(in, func(i, j int) bool {
		if in[i].start == in[j].start {
			return in[i].end < in[j].end
		}
		return in[i].start < in[j].start
	})
	out := []interval{}
	for _, x := range in {
		if x.end <= x.start {
			continue
		}
		if len(out) == 0 || x.start > out[len(out)-1].end {
			out = append(out, x)
		} else if x.end > out[len(out)-1].end {
			out[len(out)-1].end = x.end
		}
	}
	return out
}

func subtract(in []interval, masks []interval) []interval {
	for _, m := range union(masks) {
		var next []interval
		for _, x := range in {
			if m.end <= x.start || m.start >= x.end {
				next = append(next, x)
				continue
			}
			if m.start > x.start {
				next = append(next, interval{x.start, m.start})
			}
			if m.end < x.end {
				next = append(next, interval{m.end, x.end})
			}
		}
		in = next
	}
	return in
}

func length(in []interval) int64 {
	var n int64
	for _, x := range union(in) {
		n += x.end - x.start
	}
	return n
}

// CalculateActivity never extends a dangling activity to wall-clock now. Runtime
// sessions (not tool calls) are the agents. Missing boundaries remain unknown.
func CalculateActivity(events []ActivityEvent, segments []ActivitySegment) ActivityReport {
	r := ActivityReport{Unknown: []ObservationGap{}, PerAgentMS: map[string]int64{}, Basis: "closed observed intervals only; unknown gaps excluded; runtime integration experimental"}
	type pair struct {
		e           ActivityEvent
		start, stop *int64
	}
	key := func(runtime, session, segment string) string { return runtime + "\x00" + session + "\x00" + segment }
	bounds := map[string]ActivitySegment{}
	for _, s := range segments {
		bounds[key(s.Runtime, s.Session, s.Segment)] = s
	}
	pairs := map[string]*pair{}
	for _, e := range events {
		if e.Kind == "gap" {
			s := bounds[key(e.Runtime, e.Session, e.Segment)]
			r.Unknown = append(r.Unknown, ObservationGap{Runtime: e.Runtime, Session: e.Session, Segment: e.Segment, From: e.At, To: s.Until, Reason: e.Reason})
			continue
		}
		if e.Kind != "start" && e.Kind != "stop" {
			continue
		}
		k := key(e.Runtime, e.Session, e.Segment) + "\x00" + e.Activity
		p := pairs[k]
		if p == nil {
			p = &pair{e: e}
			pairs[k] = p
		}
		t := e.At
		if e.Kind == "start" {
			p.start = &t
		} else {
			p.stop = &t
		}
	}
	type span struct {
		e ActivityEvent
		x interval
	}
	var spans []span
	for _, p := range pairs {
		if p.start == nil || p.stop == nil || *p.stop < *p.start {
			at := p.e.At
			var until *int64
			s, known := bounds[key(p.e.Runtime, p.e.Session, p.e.Segment)]
			if p.start != nil {
				at = *p.start
				until = s.Until
			} else {
				if known {
					at = s.At
				}
				until = p.stop
			}
			r.Unknown = append(r.Unknown, ObservationGap{Runtime: p.e.Runtime, Session: p.e.Session, Segment: p.e.Segment, Activity: p.e.Activity, From: at, To: until, Reason: "missing_activity_boundary"})
			continue
		}
		x := interval{*p.start, *p.stop}
		if s, ok := bounds[key(p.e.Runtime, p.e.Session, p.e.Segment)]; ok {
			if x.start < s.At {
				x.start = s.At
			}
			until := s.AccountingUntil
			if until == nil {
				until = s.Until
			}
			if until != nil && x.end > *until {
				x.end = *until
			}
		}
		spans = append(spans, span{p.e, x})
	}
	active, retry := map[string][]interval{}, map[string][]interval{}
	for _, s := range spans {
		agentJSON, _ := json.Marshal([2]string{s.e.Runtime, s.e.Session})
		agent := string(agentJSON)
		if s.e.Category == "retry" {
			var masks []interval
			for _, e := range events {
				if e.Kind == "gap" && e.Reason != "uncertain_inference" && key(e.Runtime, e.Session, e.Segment) == key(s.e.Runtime, s.e.Session, s.e.Segment) && e.At > s.x.start && e.At < s.x.end {
					masks = append(masks, interval{e.At, s.x.end})
				}
			}
			retry[agent] = append(retry[agent], subtract([]interval{s.x}, masks)...)
			continue
		}
		if s.e.Category != "tool" && s.e.Category != "inference" {
			continue
		}
		var masks []interval
		for _, e := range events {
			if e.Kind == "gap" && e.Reason != "uncertain_inference" && key(e.Runtime, e.Session, e.Segment) == key(s.e.Runtime, s.e.Session, s.e.Segment) && e.At > s.x.start && e.At < s.x.end {
				masks = append(masks, interval{e.At, s.x.end})
			}
			if e.Kind == "gap" && e.Reason == "uncertain_inference" && s.e.Category == "inference" && key(e.Runtime, e.Session, e.Segment) == key(s.e.Runtime, s.e.Session, s.e.Segment) && e.At > s.x.start && e.At < s.x.end {
				masks = append(masks, interval{e.At, s.x.end})
			}
		}
		for _, w := range spans {
			if key(w.e.Runtime, w.e.Session, w.e.Segment) != key(s.e.Runtime, s.e.Session, s.e.Segment) {
				continue
			}
			wait := w.e.Category == "permission" || w.e.Category == "question"
			if s.e.Category == "inference" && (wait || w.e.Category == "retry" || w.e.Category == "delegation") || s.e.Category == "tool" && wait && w.e.Target == s.e.Activity {
				masks = append(masks, w.x)
			}
		}
		// An unfinished wait cannot prove that blocked activity resumed. Mask
		// from its observed start through this candidate span, not through now.
		for _, p := range pairs {
			if p.start != nil && p.stop != nil || key(p.e.Runtime, p.e.Session, p.e.Segment) != key(s.e.Runtime, s.e.Session, s.e.Segment) {
				continue
			}
			wait := p.e.Category == "permission" || p.e.Category == "question"
			if s.e.Category == "inference" && (wait || p.e.Category == "retry" || p.e.Category == "delegation") || s.e.Category == "tool" && wait && p.e.Target == s.e.Activity {
				start, end := s.x.start, s.x.end
				if p.start != nil {
					start = *p.start
				}
				if p.stop != nil {
					end = *p.stop
				}
				masks = append(masks, interval{start, end})
			}
		}
		active[agent] = append(active[agent], subtract([]interval{s.x}, masks)...)
	}
	var all, retries []interval
	for agent, spans := range active {
		n := length(spans)
		r.PerAgentMS[agent] = n
		r.AgentMS += n
		all = append(all, spans...)
	}
	for _, spans := range retry {
		r.RetryAgentMS += length(spans)
		retries = append(retries, spans...)
	}
	r.ActiveElapsedMS = length(all)
	r.RetryElapsedMS = length(retries)
	sort.Slice(r.Unknown, func(i, j int) bool {
		a, b := r.Unknown[i], r.Unknown[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return key(a.Runtime, a.Session, a.Segment)+a.Activity+a.Reason < key(b.Runtime, b.Session, b.Segment)+b.Activity+b.Reason
	})
	return r
}
