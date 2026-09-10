package store

import (
	"bytes"
	"os"
	"testing"

	"tools-programmer/internal/core"
)

func TestDurableDispatchReadOnlySurvivesParentSwitch(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	s.AddIssue("one", 1, "")
	s.AddIssue("one", 2, "")
	record := func(e core.ActivityEvent) {
		t.Helper()
		if err := s.RecordActivity(e); err != nil {
			t.Fatal(err)
		}
	}
	record(core.ActivityEvent{ID: "p", Runtime: "r", Session: "parent", Segment: "a", Kind: "bind", At: 100, Project: "one", Issue: 1})
	marker, err := core.DispatchMarker("r", "parent", "call")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"start", "stop"} {
		record(core.ActivityEvent{ID: marker + "-" + kind, Runtime: "r", Session: "parent", Segment: "a", Kind: kind, At: 110, Activity: marker, Category: "delegation"})
	}
	if err := s.RecordActivity(core.ActivityEvent{ID: "tie", Runtime: "r", Session: "parent", Segment: "tie", Kind: "bind", At: 110, Project: "one", Issue: 2}); err == nil {
		t.Fatal("same-timestamp switch could invalidate durable dispatch")
	}
	record(core.ActivityEvent{ID: "switch", Runtime: "r", Session: "parent", Segment: "b", Kind: "bind", At: 120, Project: "one", Issue: 2})
	s.Close()
	before, _ := os.ReadFile(path)
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ro.Dispatch("r", "parent", "call")
	if err != nil || d == nil || d.Segment != "a" || d.At != 110 {
		t.Fatalf("%+v %v", d, err)
	}
	if missing, err := ro.Dispatch("r", "parent", "absent"); err != nil || missing != nil {
		t.Fatal(missing, err)
	}
	if _, err := ro.Dispatch("r", "bad id", "call"); err == nil {
		t.Fatal("invalid lookup accepted")
	}
	ro.Close()
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("read-only lookup mutated state")
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	record(core.ActivityEvent{ID: "child", Runtime: "r", Session: "child", Segment: "new", Kind: "bind", At: 130, ParentSession: d.Session, ParentSegment: d.Segment, DispatchAt: &d.At})
	_, segments, err := s.IssueActivity("one", 1)
	if err != nil || len(segments) != 2 {
		t.Fatal(segments, err)
	}
	record(core.ActivityEvent{ID: "b-start", Runtime: "r", Session: "parent", Segment: "b", Kind: "start", At: 140, Activity: "independent", Category: "tool"})
	record(core.ActivityEvent{ID: "b-stop", Runtime: "r", Session: "parent", Segment: "b", Kind: "stop", At: 160, Activity: "independent", Category: "tool"})
	events, bound, err := s.IssueActivity("one", 2)
	if err != nil || core.CalculateActivity(events, bound).AgentMS != 20 {
		t.Fatal(events, err)
	}
}

func TestDispatchConflictingBoundaryFailsClosed(t *testing.T) {
	s, _ := openTest(t)
	for _, e := range []core.ActivityEvent{
		{ID: "a", Runtime: "r", Session: "p", Segment: "a", Kind: "bind", At: 100},
		{ID: "b", Runtime: "r", Session: "p", Segment: "b", Kind: "bind", At: 200},
	} {
		if err := s.RecordActivity(e); err != nil {
			t.Fatal(err)
		}
	}
	marker, _ := core.DispatchMarker("r", "p", "call")
	for _, e := range []core.ActivityEvent{
		{ID: marker + "-start", Runtime: "r", Session: "p", Segment: "a", Kind: "start", At: 110, Activity: marker, Category: "delegation"},
		{ID: marker + "-stop", Runtime: "r", Session: "p", Segment: "b", Kind: "stop", At: 210, Activity: marker, Category: "delegation"},
	} {
		if err := s.RecordActivity(e); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Dispatch("r", "p", "call"); err == nil {
		t.Fatal("conflicting persisted call accepted")
	}
}
