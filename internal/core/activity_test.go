package core

import "testing"

func TestActivityUnionWaitsAndUnknown(t *testing.T) {
	var events []ActivityEvent
	span := func(session, activity, category, target string, start, end int64) {
		e := ActivityEvent{Runtime: "r", Session: session, Segment: "s", Activity: activity, Category: category, Target: target, Kind: "start", At: start}
		events = append(events, e)
		e.Kind, e.At = "stop", end
		events = append(events, e)
	}
	span("parent", "dispatch", "delegation", "", 0, 600000)
	span("child1", "t1", "tool", "", 0, 600000)
	span("child1", "t2", "tool", "", 0, 600000)
	span("child2", "t1", "tool", "", 0, 600000)
	r := CalculateActivity(events, nil)
	if r.ActiveElapsedMS != 600000 || r.AgentMS != 1200000 {
		t.Fatal(r)
	}
	span("parent", "independent", "tool", "", 0, 100000)
	span("parent", "inference", "inference", "", 0, 600000)
	span("parent", "wait", "permission", "", 0, 300000)
	span("parent", "backoff", "retry", "", 300000, 600000)
	r = CalculateActivity(events, nil)
	if r.AgentMS != 1300000 || r.RetryElapsedMS != 300000 {
		t.Fatal(r)
	}
	events = append(events, ActivityEvent{Runtime: "r", Session: "child1", Segment: "s", Activity: "orphan", Kind: "start", Category: "tool", At: 700000})
	r = CalculateActivity(events, nil)
	if len(r.Unknown) != 1 || r.AgentMS != 1300000 {
		t.Fatal(r)
	}
}

func TestActivityMasksAndGaps(t *testing.T) {
	for _, tt := range []struct {
		name             string
		category, target string
		closed           bool
		want             int64
	}{
		{"permission blocks one tool", "permission", "blocked", true, 150},
		{"question blocks one tool", "question", "blocked", true, 150},
		{"open permission remains blocked", "permission", "blocked", false, 100},
		{"retry never erases independent tools", "retry", "", true, 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			events := []ActivityEvent{
				{Runtime: "r", Session: "s", Segment: "x", Kind: "start", Activity: "blocked", Category: "tool", At: 0},
				{Runtime: "r", Session: "s", Segment: "x", Kind: "stop", Activity: "blocked", Category: "tool", At: 200},
				{Runtime: "r", Session: "s", Segment: "x", Kind: "start", Activity: "independent", Category: "tool", At: 0},
				{Runtime: "r", Session: "s", Segment: "x", Kind: "stop", Activity: "independent", Category: "tool", At: 100},
				{Runtime: "r", Session: "s", Segment: "x", Kind: "start", Activity: "wait", Category: tt.category, Target: tt.target, At: 50},
			}
			if tt.closed {
				e := events[4]
				e.Kind = "stop"
				e.At = 150
				events = append(events, e)
			}
			r := CalculateActivity(events, nil)
			if r.AgentMS != tt.want {
				t.Fatal(r)
			}
		})
	}
	events := []ActivityEvent{
		{Runtime: "r", Session: "s", Segment: "x", Kind: "start", Activity: "tool", Category: "tool", At: 0},
		{Runtime: "r", Session: "s", Segment: "x", Kind: "stop", Activity: "tool", Category: "tool", At: 1000},
		{Runtime: "r", Session: "s", Segment: "x", Kind: "gap", Reason: "event_loss", At: 100},
	}
	r := CalculateActivity(events, nil)
	if r.AgentMS != 100 || len(r.Unknown) != 1 {
		t.Fatal(r)
	}
	until := int64(50)
	r = CalculateActivity(events[:2], []ActivitySegment{{Runtime: "r", Session: "s", Segment: "x", At: 0, Until: &until}})
	if r.AgentMS != 50 {
		t.Fatal("switch rewrote assignment", r)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	if reordered := CalculateActivity(events, nil); reordered.AgentMS != 100 {
		t.Fatal(reordered)
	}
}
