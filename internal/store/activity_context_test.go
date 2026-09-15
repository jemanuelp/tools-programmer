package store

import (
	"encoding/json"
	"strings"
	"testing"

	"tools-programmer/internal/core"
)

func TestCurrentActivityResolutionAndHistory(t *testing.T) {
	s, _ := openTest(t)
	dir := t.TempDir()
	p := core.Project{Name: "demo", Repo: "example/demo", Path: dir, ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "private@example.com"}
	if _, err := s.AddProject(p); err != nil {
		t.Fatal(err)
	}
	i, err := s.AddIssue("demo", 19, "")
	if err != nil {
		t.Fatal(err)
	}
	read := func(runtime, session string) ActivityContext {
		t.Helper()
		c, err := s.CurrentActivity(runtime, session, dir)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(c)
		if strings.Contains(string(b), dir) || strings.Contains(string(b), p.GitEmail) {
			t.Fatal("private identity leaked")
		}
		return c
	}
	if c := read("r", "native"); c.Assignment != "unknown" || c.ProjectResolution != "matched" {
		t.Fatal(c)
	}
	bind := func(id, session, project string, issue int, at int64) {
		t.Helper()
		if err := s.RecordActivity(core.ActivityEvent{ID: id, Runtime: "r", Session: session, Segment: id, Kind: "bind", At: at, Project: project, Issue: issue}); err != nil {
			t.Fatal(err)
		}
	}
	bind("a", "native", "demo", 19, 1)
	if c := read("r", "native"); c.Assignment != "assigned" || c.Issue.ID != i.ID || c.Issue.Outcome != "open" {
		t.Fatal(c)
	}
	if c := read("other-origin", "native"); c.Assignment != "unknown" {
		t.Fatal("namespace inherited", c)
	}
	if c := read("r", "other-session"); c.Assignment != "unknown" {
		t.Fatal("session inherited", c)
	}
	if err := s.RecordIssueOutcome(i.ID, "completed", "agent", "explicitly authorized; implementation and verification complete"); err != nil {
		t.Fatal(err)
	}
	if c := read("r", "native"); c.Issue.Outcome != "completed" {
		t.Fatal(c)
	}
	if err := s.RecordActivity(core.ActivityEvent{ID: "child", Runtime: "r", Session: "child", Segment: "child", Kind: "bind", At: 2, ParentSession: "native", ParentSegment: "a"}); err != nil {
		t.Fatal(err)
	}
	bind("b", "native", "", 0, 3)
	if c := read("r", "native"); c.Assignment != "unassigned" || c.Issue != nil {
		t.Fatal(c)
	}
	if c := read("r", "child"); c.Assignment != "assigned" || c.ParentSession != "native" {
		t.Fatal(c)
	}
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM activity_segments").Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	p.Name, p.Repo = "other", "example/other"
	if _, err := s.AddProject(p); err != nil {
		t.Fatal(err)
	}
	if c := read("r", "native"); c.ProjectResolution != "ambiguous" || c.Assignment != "unknown" || c.Project != "" {
		t.Fatal(c)
	}
	c, err := s.CurrentActivity("r", "native", t.TempDir())
	if err != nil || c.ProjectResolution != "unavailable" || c.Assignment != "unknown" {
		t.Fatal(c, err)
	}
}
