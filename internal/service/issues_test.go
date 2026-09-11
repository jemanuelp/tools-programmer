package service

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func issueActivityService(t *testing.T) (*Service, core.Issue, core.Issue) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "r@example.com"}); err != nil {
		t.Fatal(err)
	}
	a, err := db.AddIssue("demo", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.AddIssue("demo", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Store: db}, a, b
}

func recordIssueActivity(t *testing.T, s *Service, events ...core.ActivityEvent) {
	t.Helper()
	for _, event := range events {
		if err := s.Store.RecordActivity(event); err != nil {
			t.Fatal(err)
		}
	}
}

func issueSummaryJSON(t *testing.T, s *Service, number int) ([]byte, IssueSummary) {
	t.Helper()
	summary, err := s.IssueSummary("demo", number)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	return data, summary
}

func TestIssueSummaryHistoryStableAcrossDifferentIssueReuse(t *testing.T) {
	s, issueA, _ := issueActivityService(t)
	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "a-bind", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "bind", At: 100, Project: "demo", Issue: 1},
		core.ActivityEvent{ID: "a-start", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "start", At: 110, Activity: "tool-a", Category: "tool"},
		core.ActivityEvent{ID: "a-loss", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "gap", At: 150, Reason: "event_loss"},
		core.ActivityEvent{ID: "a-stop", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "stop", At: 210, Activity: "tool-a", Category: "tool"},
	)
	if err := s.Store.RecordIssueOutcome(issueA.ID, "completed", "human", "work settled"); err != nil {
		t.Fatal(err)
	}
	beforeJSON, before := issueSummaryJSON(t, s, 1)
	if before.Activity.ActiveElapsedMS != 40 || before.Activity.AgentMS != 40 || before.Activity.PerAgentMS[`["runtime","reused"]`] != 40 || len(before.Segments) != 1 || before.Segments[0].Until != nil {
		t.Fatal(before)
	}
	if len(before.Activity.Unknown) != 1 || before.Activity.Unknown[0].Reason != "event_loss" || before.Activity.Unknown[0].To != nil {
		t.Fatal(before.Activity.Unknown)
	}

	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "b-bind", Runtime: "runtime", Session: "reused", Segment: "b", Kind: "bind", At: 300, Project: "demo", Issue: 2},
		core.ActivityEvent{ID: "b-start", Runtime: "runtime", Session: "reused", Segment: "b", Kind: "start", At: 310, Activity: "tool-b", Category: "tool"},
		core.ActivityEvent{ID: "b-stop", Runtime: "runtime", Session: "reused", Segment: "b", Kind: "stop", At: 360, Activity: "tool-b", Category: "tool"},
	)
	afterJSON, _ := issueSummaryJSON(t, s, 1)
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatalf("completed issue history changed after different-issue reuse\nbefore: %s\nafter:  %s", beforeJSON, afterJSON)
	}
	bJSON, issueB := issueSummaryJSON(t, s, 2)
	if issueB.Activity.ActiveElapsedMS != 50 || issueB.Activity.AgentMS != 50 || issueB.Activity.PerAgentMS[`["runtime","reused"]`] != 50 || len(issueB.Segments) != 1 || issueB.Segments[0].Segment != "b" {
		t.Fatalf("issue B is not independent: %s", bJSON)
	}
}

func TestIssueSummaryDifferentIssueBoundaryClipsLateClosureWithoutHidingLoss(t *testing.T) {
	s, _, _ := issueActivityService(t)
	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "a-bind", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "bind", At: 100, Project: "demo", Issue: 1},
		core.ActivityEvent{ID: "a-start", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "start", At: 110, Activity: "tool-a", Category: "tool"},
		core.ActivityEvent{ID: "a-loss", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "gap", At: 150, Reason: "event_loss"},
	)
	openJSON, open := issueSummaryJSON(t, s, 1)
	if open.Activity.ActiveElapsedMS != 0 || len(open.Activity.Unknown) != 2 {
		t.Fatal(open.Activity)
	}
	for _, gap := range open.Activity.Unknown {
		if gap.To != nil {
			t.Fatal(open.Activity.Unknown)
		}
	}

	recordIssueActivity(t, s, core.ActivityEvent{ID: "b-bind", Runtime: "runtime", Session: "reused", Segment: "b", Kind: "bind", At: 300, Project: "demo", Issue: 2})
	boundedOpenJSON, boundedOpen := issueSummaryJSON(t, s, 1)
	if !bytes.Equal(openJSON, boundedOpenJSON) || boundedOpen.Activity.ActiveElapsedMS != 0 {
		t.Fatalf("open issue projection changed at a different-issue boundary\nbefore: %s\nafter:  %s", openJSON, boundedOpenJSON)
	}
	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "a-stop", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "stop", At: 350, Activity: "tool-a", Category: "tool"},
	)
	_, settled := issueSummaryJSON(t, s, 1)
	if settled.Activity.ActiveElapsedMS != 40 || settled.Activity.AgentMS != 40 {
		t.Fatal(settled.Activity)
	}
	if len(settled.Activity.Unknown) != 1 || settled.Activity.Unknown[0].Reason != "event_loss" || settled.Activity.Unknown[0].To != nil {
		t.Fatal(settled.Activity.Unknown)
	}
	if len(settled.Segments) != 1 || settled.Segments[0].Until != nil {
		t.Fatal(settled.Segments)
	}
}

func TestIssueSummaryDifferentIssueBoundaryClipsLateStopWithoutEventLoss(t *testing.T) {
	s, _, _ := issueActivityService(t)
	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "a-bind", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "bind", At: 100, Project: "demo", Issue: 1},
		core.ActivityEvent{ID: "a-start", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "start", At: 110, Activity: "tool-a", Category: "tool"},
	)
	beforeBoundaryJSON, beforeBoundary := issueSummaryJSON(t, s, 1)
	if beforeBoundary.Activity.ActiveElapsedMS != 0 || len(beforeBoundary.Activity.Unknown) != 1 || beforeBoundary.Activity.Unknown[0].Reason != "missing_activity_boundary" || beforeBoundary.Activity.Unknown[0].To != nil {
		t.Fatal(beforeBoundary.Activity)
	}

	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "b-bind", Runtime: "runtime", Session: "reused", Segment: "b", Kind: "bind", At: 300, Project: "demo", Issue: 2},
	)
	afterBoundaryJSON, afterBoundary := issueSummaryJSON(t, s, 1)
	if !bytes.Equal(beforeBoundaryJSON, afterBoundaryJSON) {
		t.Fatalf("issue A projection changed at the hidden different-issue boundary\nbefore: %s\nafter:  %s", beforeBoundaryJSON, afterBoundaryJSON)
	}
	if len(afterBoundary.Segments) != 1 || afterBoundary.Segments[0].Until != nil {
		t.Fatal(afterBoundary.Segments)
	}

	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "a-stop", Runtime: "runtime", Session: "reused", Segment: "a", Kind: "stop", At: 350, Activity: "tool-a", Category: "tool"},
	)
	settledJSON, settled := issueSummaryJSON(t, s, 1)
	if settled.Activity.ActiveElapsedMS != 190 || settled.Activity.AgentMS != 190 || settled.Activity.PerAgentMS[`["runtime","reused"]`] != 190 {
		t.Fatal(settled.Activity)
	}
	if len(settled.Activity.Unknown) != 0 {
		t.Fatal(settled.Activity.Unknown)
	}
	if len(settled.Segments) != 1 || settled.Segments[0].Until != nil {
		t.Fatal(settled.Segments)
	}

	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "b-start", Runtime: "runtime", Session: "reused", Segment: "b", Kind: "start", At: 310, Activity: "tool-b", Category: "tool"},
		core.ActivityEvent{ID: "b-stop", Runtime: "runtime", Session: "reused", Segment: "b", Kind: "stop", At: 360, Activity: "tool-b", Category: "tool"},
	)
	afterBJSON, _ := issueSummaryJSON(t, s, 1)
	if !bytes.Equal(settledJSON, afterBJSON) {
		t.Fatalf("issue A projection changed after issue B activity\nbefore: %s\nafter:  %s", settledJSON, afterBJSON)
	}
	bJSON, issueB := issueSummaryJSON(t, s, 2)
	if issueB.Activity.ActiveElapsedMS != 50 || issueB.Activity.AgentMS != 50 || issueB.Activity.PerAgentMS[`["runtime","reused"]`] != 50 || len(issueB.Activity.Unknown) != 0 {
		t.Fatalf("issue B activity is not independent: %s", bJSON)
	}
	if len(issueB.Segments) != 1 || issueB.Segments[0].Segment != "b" || issueB.Segments[0].Until != nil {
		t.Fatalf("issue B segment is not independent: %s", bJSON)
	}
}

func TestIssueSummarySameIssueContinuationExposesSegmentBoundary(t *testing.T) {
	s, _, _ := issueActivityService(t)
	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "a1-bind", Runtime: "runtime", Session: "continued", Segment: "a1", Kind: "bind", At: 100, Project: "demo", Issue: 1},
		core.ActivityEvent{ID: "a1-start", Runtime: "runtime", Session: "continued", Segment: "a1", Kind: "start", At: 110, Activity: "tool-a", Category: "tool"},
		core.ActivityEvent{ID: "a1-loss", Runtime: "runtime", Session: "continued", Segment: "a1", Kind: "gap", At: 150, Reason: "event_loss"},
	)
	_, before := issueSummaryJSON(t, s, 1)
	if len(before.Segments) != 1 || before.Segments[0].Until != nil {
		t.Fatal(before.Segments)
	}
	recordIssueActivity(t, s,
		core.ActivityEvent{ID: "a2-bind", Runtime: "runtime", Session: "continued", Segment: "a2", Kind: "bind", At: 300, Project: "demo", Issue: 1},
		core.ActivityEvent{ID: "a1-stop", Runtime: "runtime", Session: "continued", Segment: "a1", Kind: "stop", At: 350, Activity: "tool-a", Category: "tool"},
	)
	_, summary := issueSummaryJSON(t, s, 1)
	if len(summary.Segments) != 2 || summary.Segments[0].Until == nil || *summary.Segments[0].Until != 300 || summary.Segments[1].Until != nil {
		t.Fatal(summary.Segments)
	}
	if summary.Activity.ActiveElapsedMS != 40 || len(summary.Activity.Unknown) != 1 || summary.Activity.Unknown[0].To == nil || *summary.Activity.Unknown[0].To != 300 {
		t.Fatal(summary.Activity)
	}
}
