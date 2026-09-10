package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"tools-programmer/internal/core"
)

func TestIssueHistoryAndOwnership(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	i, err := s.AddIssue("one", 1, "I_example")
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.AddIssue("one", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveIssueRemoteID(j.ID, "I_later"); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveIssueRemoteID(j.ID, "I_later"); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveIssueRemoteID(j.ID, "I_changed"); err == nil {
		t.Fatal("remote identity replaced")
	}
	if _, err = s.AddIssue("one", 3, "I_later"); err == nil {
		t.Fatal("observed stable identity reused")
	}
	if _, err = s.AddIssue("one", 4, "MDU6SXNzdWUxMjM="); err != nil {
		t.Fatal("opaque base64 identity rejected", err)
	}
	if err = s.LinkIssuePR(i.ID, 7); err != nil {
		t.Fatal(err)
	}
	if err = s.LinkIssuePR(j.ID, 7); err == nil {
		t.Fatal("PR acquired two owners")
	}
	for _, score := range []int{0, 4, 22, 34} {
		if _, err := s.ProposeEstimate(i.ID, "initial", score, "agent", "relative effort"); err == nil {
			t.Fatalf("accepted %d", score)
		}
	}
	p, err := s.ProposeEstimate(i.ID, "initial", 3, "agent", "bounded scope")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ConfirmEstimate(p.ID, "human", "reviewed scope"); err != nil {
		t.Fatal(err)
	}
	q, _ := s.ProposeEstimate(i.ID, "initial", 5, "agent", "another baseline")
	if _, err = s.ConfirmEstimate(q.ID, "human", "overwrite"); err == nil {
		t.Fatal("baseline overwritten")
	}
	for _, kind := range []string{"revision", "final"} {
		p, err = s.ProposeEstimate(i.ID, kind, 8, "agent", "new information")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.ConfirmEstimate(p.ID, "human", "accepted"); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.RecordIssueOutcome(i.ID, "completed", "human", "implementation verified"); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordIssueOutcome(i.ID, "reopened", "human", "additional work"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE issue_estimates SET score=21 WHERE id=?", p.ID); err == nil {
		t.Fatal("mutable history")
	}
	h, err := s.IssueHistory("one", 1)
	if err != nil || len(h.Estimates) != 4 || len(h.Confirmations) != 3 || len(h.Outcomes) != 2 {
		t.Fatalf("%+v %v", h, err)
	}
	if h.Initial == nil || h.Initial.Score != 3 || h.Final == nil || h.Final.Score != 8 {
		t.Fatal(h)
	}
}

func TestV2IssueMigrationPreservesUnresolvedOperations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "history.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(schema + `ALTER TABLE projects ADD COLUMN expected_github TEXT NOT NULL DEFAULT ''; ALTER TABLE projects ADD COLUMN git_name TEXT NOT NULL DEFAULT ''; ALTER TABLE projects ADD COLUMN git_email TEXT NOT NULL DEFAULT ''; PRAGMA user_version=2;
INSERT INTO projects VALUES('legacy','github.com/example/legacy','',0,'old','reviewer','Reviewer','r@example.com');
INSERT INTO plans VALUES('plan','legacy','{"id":"plan"}');
INSERT INTO operations VALUES('op','plan','legacy',7,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','reviewer','unknown',0,'old','old');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if ro, err := OpenReadOnly(path); err == nil {
		ro.Close()
		t.Fatal("read-only opener migrated v2")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var state string
	if err = s.DB.QueryRow("SELECT state FROM operations WHERE id='op'").Scan(&state); err != nil || state != "unknown" {
		t.Fatal(state, err)
	}
	if _, err = s.AddIssue("legacy", 1, ""); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM operations").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestConcurrentActivityDedupAndBaselineConfirmation(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	i, err := s.AddIssue("one", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	e := core.ActivityEvent{ID: "e", Runtime: "r", Session: "s", Segment: "x", Kind: "bind", At: 1, Project: "one", Issue: 1}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			db := s
			if n%2 == 1 {
				db = other
			}
			errs <- db.RecordActivity(e)
		}(n)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM activity_events").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	p, _ := s.ProposeEstimate(i.ID, "initial", 3, "agent", "scope")
	q, _ := s.ProposeEstimate(i.ID, "initial", 5, "agent", "alternative")
	results := make(chan error, 2)
	go func() { _, err := s.ConfirmEstimate(p.ID, "human", "approved"); results <- err }()
	go func() { _, err := other.ConfirmEstimate(q.ID, "human", "approved"); results <- err }()
	success := 0
	for n := 0; n < 2; n++ {
		if <-results == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
}

func TestRuntimeResumeReuseAndClosedHistory(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	s.AddIssue("one", 1, "")
	s.AddIssue("one", 2, "")
	record := func(e core.ActivityEvent) {
		t.Helper()
		if err := s.RecordActivity(e); err != nil {
			t.Fatal(err)
		}
	}
	record(core.ActivityEvent{ID: "p1", Runtime: "r", Session: "p", Segment: "p1", Kind: "bind", At: 10, Project: "one", Issue: 1})
	dispatch := int64(11)
	record(core.ActivityEvent{ID: "p2", Runtime: "r", Session: "p", Segment: "p2", Kind: "bind", At: 20, Project: "one", Issue: 2})
	record(core.ActivityEvent{ID: "c1", Runtime: "r", Session: "c", Segment: "c1", Kind: "bind", At: 30, ParentSession: "p", ParentSegment: "p1", DispatchAt: &dispatch})
	record(core.ActivityEvent{ID: "c2", Runtime: "r", Session: "c", Segment: "c2", Kind: "bind", At: 40, ParentSession: "p", ParentSegment: "p2"})
	record(core.ActivityEvent{ID: "resume", Runtime: "r", Session: "p", Segment: "p3", Kind: "bind", At: 50, Resume: true})
	_, segments, err := s.IssueActivity("one", 1)
	if err != nil || len(segments) != 2 {
		t.Fatal(segments, err)
	}
	_, segments, err = s.IssueActivity("one", 2)
	if err != nil || len(segments) != 3 {
		t.Fatal(segments, err)
	}
	if err = s.RecordActivity(core.ActivityEvent{ID: "retro", Runtime: "r", Session: "p", Segment: "old", Kind: "bind", At: 15, Project: "one", Issue: 1}); err == nil {
		t.Fatal("retroactive switch accepted")
	}
	for _, table := range []string{"issue_prs", "issue_estimates", "issue_confirmations", "issue_outcomes", "activity_events", "activity_segments"} {
		if _, err = s.DB.Exec("DELETE FROM " + table); err == nil && table == "activity_events" {
			t.Fatal("deleted immutable events")
		}
	}
}

func TestActivityRecorderDuplicateOrderingAndInheritance(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	s.AddIssue("one", 1, "")
	s.AddIssue("one", 2, "")
	bind := core.ActivityEvent{ID: "bind", Runtime: "runtime", Session: "parent", Segment: "p1", Kind: "bind", At: 1000, Project: "one", Issue: 1}
	if err := s.RecordActivity(bind); err != nil {
		t.Fatal(err)
	}
	child := core.ActivityEvent{ID: "child", Runtime: "runtime", Session: "child", Segment: "c1", Kind: "bind", At: 2000, ParentSession: "parent", ParentSegment: "p1"}
	if err := s.RecordActivity(child); err != nil {
		t.Fatal(err)
	}
	bind.ID, bind.Segment, bind.At, bind.Issue = "switch", "p2", 3000, 2
	if err := s.RecordActivity(bind); err != nil {
		t.Fatal(err)
	}
	stop := core.ActivityEvent{ID: "stop", Runtime: "runtime", Session: "child", Segment: "c1", Kind: "stop", At: 602000, Activity: "tool1", Category: "tool"}
	if err := s.RecordActivity(stop); err != nil {
		t.Fatal(err)
	}
	start := stop
	start.ID, start.Kind, start.At = "start", "start", 2000
	if err := s.RecordActivity(start); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity(start); err != nil {
		t.Fatal(err)
	}
	start.At++
	if err := s.RecordActivity(start); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	events, segments, err := s.IssueActivity("one", 1)
	if err != nil {
		t.Fatal(err)
	}
	report := core.CalculateActivity(events, segments)
	if report.ActiveElapsedMS != 600000 || report.AgentMS != 600000 {
		t.Fatal(report)
	}
}
