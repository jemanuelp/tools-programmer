package service

import (
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func newReviewTimeService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state", "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.AddProject(core.Project{Name: "reports", Repo: "example/reports", ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"}); err != nil {
		t.Fatal(err)
	}
	return &Service{Store: s}, s
}

func TestReviewTimeReportSeparatesHumanAgentAndElapsedMeasures(t *testing.T) {
	svc, db := newReviewTimeService(t)
	issue42, issue43, pr7 := 42, 43, 7
	knownSHA, otherSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	entries := []core.ReviewTimeEntry{
		{ID: "known-one", Project: "reports", IssueNumber: &issue42, PR: &pr7, SHA: knownSHA, Reviewer: "Reviewer", DurationMS: 1000, Source: "manual"},
		{ID: "known-two", Project: "reports", IssueNumber: &issue42, PR: &pr7, SHA: knownSHA, Reviewer: "Reviewer", DurationMS: 2000, Source: "manual"},
		{ID: "unknown-sha", Project: "reports", IssueNumber: &issue42, PR: &pr7, Reviewer: "Reviewer", DurationMS: 500, Source: "manual"},
		{ID: "project-only", Project: "reports", Reviewer: "Reviewer", DurationMS: 250, Source: "manual"},
		{ID: "local-known", Project: "reports", IssueNumber: &issue43, SHA: otherSHA, Reviewer: "Reviewer", DurationMS: 700, Source: "manual"},
	}
	for _, entry := range entries {
		if _, err := db.RecordReviewTime(entry); err != nil {
			t.Fatal(err)
		}
	}

	report, err := svc.ReviewTimeReport("reports", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.HumanReviewMS != 4450 || report.LocalHumanReviewMS != 950 || report.PRHumanReviewMS != 3500 || report.AgentReviewMS != nil || report.ElapsedReviewMS != nil || report.AutomaticCoverage != "pending" {
		t.Fatalf("review-time measures were combined or overstated: %+v", report)
	}
	if len(report.Buckets) != 4 {
		t.Fatalf("expected distinct project, local, known-SHA and unknown-SHA buckets: %+v", report.Buckets)
	}
	var known, unknown, projectOnly, localKnown bool
	for _, bucket := range report.Buckets {
		switch {
		case bucket.ReviewType == "pr" && bucket.PR != nil && *bucket.PR == 7 && bucket.SHA == knownSHA:
			known = bucket.HumanReviewMS == 3000 && bucket.EntryCount == 2
		case bucket.ReviewType == "pr" && bucket.PR != nil && *bucket.PR == 7 && bucket.SHA == "unknown":
			unknown = bucket.HumanReviewMS == 500 && bucket.EntryCount == 1
		case bucket.ReviewType == "local" && bucket.IssueNumber == nil && bucket.PR == nil:
			projectOnly = bucket.HumanReviewMS == 250 && bucket.EntryCount == 1
		case bucket.ReviewType == "local" && bucket.IssueNumber != nil && *bucket.IssueNumber == 43 && bucket.SHA == otherSHA:
			localKnown = bucket.HumanReviewMS == 700 && bucket.EntryCount == 1
		}
	}
	if !known || !unknown || !projectOnly || !localKnown {
		t.Fatalf("report lost a required manual-review bucket: %+v", report.Buckets)
	}

	filtered, err := svc.ReviewTimeReport("reports", &issue42, nil)
	if err != nil || filtered.HumanReviewMS != 3500 || len(filtered.Buckets) != 2 {
		t.Fatalf("issue filter mismatch: %+v, %v", filtered, err)
	}
	prReport, err := svc.ReviewTimeReport("reports", nil, &pr7)
	if err != nil || prReport.HumanReviewMS != 3500 || len(prReport.Buckets) != 2 {
		t.Fatalf("PR filter mismatch: %+v, %v", prReport, err)
	}
}

func TestReviewTimeReportRejectsOverflowAndInvalidScope(t *testing.T) {
	svc, db := newReviewTimeService(t)
	for _, entry := range []core.ReviewTimeEntry{
		{ID: "largest", Project: "reports", Reviewer: "Reviewer", DurationMS: math.MaxInt64, Source: "manual"},
		{ID: "overflow", Project: "reports", Reviewer: "Reviewer", DurationMS: 1, Source: "manual"},
	} {
		if _, err := db.RecordReviewTime(entry); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.ReviewTimeReport("reports", nil, nil); err == nil {
		t.Fatal("overflowing project total was accepted")
	} else {
		var typed *core.Error
		if !errors.As(err, &typed) || typed.Code != "review_time_overflow" {
			t.Fatalf("unexpected overflow error: %v", err)
		}
	}
	if _, err := svc.ReviewTimeReport("", nil, nil); err == nil {
		t.Fatal("report without a project was accepted")
	}
	zero := 0
	if _, err := svc.ReviewTimeReport("reports", &zero, nil); err == nil {
		t.Fatal("zero issue filter was accepted")
	}
}
