package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"tools-programmer/internal/core"
)

func TestReviewTimeEntryReplayConflictAndPersistence(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "reviewtime")
	issue, pr := 42, 7
	entry := core.ReviewTimeEntry{
		ID: "manual-1", Project: "reviewtime", IssueNumber: &issue, PR: &pr,
		SHA: strings.Repeat("a", 40), Reviewer: "Declared reviewer", DurationMS: 90000, Source: "manual",
	}

	first, err := s.RecordReviewTime(entry)
	if err != nil || first.RecordedAt == "" || first.Source != "manual" {
		t.Fatalf("record failed: %+v, %v", first, err)
	}
	replay, err := s.RecordReviewTime(entry)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatalf("identical replay changed the result: first=%+v replay=%+v err=%v", first, replay, err)
	}

	changed := entry
	changed.DurationMS++
	if _, err := s.RecordReviewTime(changed); err == nil {
		t.Fatal("changed data reused an identity")
	} else {
		var typed *core.Error
		if !errors.As(err, &typed) || typed.Code != "review_time_conflict" {
			t.Fatalf("unexpected changed-data error: %v", err)
		}
	}
	changedSHA := entry
	changedSHA.SHA = strings.Repeat("b", 40)
	if _, err := s.RecordReviewTime(changedSHA); err == nil {
		t.Fatal("changed reviewed SHA reused an existing identity")
	} else {
		var typed *core.Error
		if !errors.As(err, &typed) || typed.Code != "review_time_conflict" {
			t.Fatalf("unexpected changed-SHA error: %v", err)
		}
	}

	// Identical attributes under a different identity remain a distinct entry.
	distinct := entry
	distinct.ID = "manual-2"
	if _, err := s.RecordReviewTime(distinct); err != nil {
		t.Fatal(err)
	}
	newRevision := entry
	newRevision.ID = "manual-new-revision"
	newRevision.SHA = strings.Repeat("b", 40)
	if _, err := s.RecordReviewTime(newRevision); err != nil {
		t.Fatal(err)
	}
	unknownRevision := entry
	unknownRevision.ID = "manual-unknown-sha"
	unknownRevision.SHA = ""
	unknownRevision.DurationMS = 120000
	if _, err := s.RecordReviewTime(unknownRevision); err != nil {
		t.Fatal(err)
	}
	projectOnly := core.ReviewTimeEntry{ID: "manual-unassigned", Project: "reviewtime", Reviewer: "Declared reviewer", DurationMS: 60000, Source: "manual"}
	if _, err := s.RecordReviewTime(projectOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordReviewTime(core.ReviewTimeEntry{ID: "missing-project", Project: "unknown", Reviewer: "Reviewer", DurationMS: 1, Source: "manual"}); err == nil {
		t.Fatal("unregistered project was accepted")
	} else {
		var typed *core.Error
		if !errors.As(err, &typed) || typed.Code != "not_found" {
			t.Fatalf("unexpected project error: %v", err)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entries, err := reopened.ReviewTimeEntries("reviewtime")
	if err != nil || len(entries) != 5 {
		t.Fatalf("history after reopen: %d entries, %v", len(entries), err)
	}
	if entries[0].Source != "manual" {
		t.Fatalf("manual provenance was not retained: %+v", entries[0])
	}
	var foundProjectOnly, foundUnknown bool
	for _, got := range entries {
		if got.ID == projectOnly.ID {
			foundProjectOnly = got.IssueNumber == nil && got.PR == nil && got.SHA == ""
		}
		if got.ID == unknownRevision.ID {
			foundUnknown = got.PR != nil && *got.PR == 7 && got.SHA == ""
		}
	}
	if !foundProjectOnly || !foundUnknown {
		t.Fatalf("project-only or explicit unknown-SHA history was lost: %+v", entries)
	}
}

func TestReviewTimeEntryCanceledWriteDoesNotPersist(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	entry := core.ReviewTimeEntry{ID: "cancelled-entry", Project: "cancelled", Reviewer: "Reviewer", DurationMS: 1000, Source: "manual"}
	if _, err := s.WithReadContext(ctx).RecordReviewTime(entry); err == nil {
		t.Fatal("canceled write unexpectedly succeeded")
	}
	entries, err := s.ReviewTimeEntries("cancelled")
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled write persisted data: %+v, %v", entries, err)
	}
}

func TestReviewTimeEntryConcurrentReplayAcrossConnections(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "concurrent")
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	entry := core.ReviewTimeEntry{ID: "shared-entry", Project: "concurrent", Reviewer: "Reviewer", DurationMS: 45000, Source: "manual"}
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			writer := s
			if index%2 != 0 {
				writer = second
			}
			_, err := writer.RecordReviewTime(entry)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent identical replay failed: %v", err)
		}
	}
	entries, err := s.ReviewTimeEntries("concurrent")
	if err != nil || len(entries) != 1 {
		t.Fatalf("concurrent replay stored %d entries: %v", len(entries), err)
	}
}

func TestReviewTimeMigrationPreservesV7History(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "legacy")
	sha := strings.Repeat("b", 40)
	record(t, s, "legacy", sha, "incomplete")
	if _, err := s.AddIssue("legacy", 42, ""); err != nil {
		t.Fatal(err)
	}
	event := core.ActivityEvent{ID: "v7-bind", Runtime: "runtime", Session: "session", Segment: "segment", Kind: "bind", At: 100, Project: "legacy", Issue: 42}
	if err := s.RecordActivity(event); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO plans(id,project,payload) VALUES('pending-plan','legacy','{}')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO operations(id,plan_id,project,pr,sha,actor,state,review_id,created_at,updated_at) VALUES('pending-operation','pending-plan','legacy',7,?,'reviewer','intent',0,'created','updated')", sha); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO plans(id,project,payload) VALUES('confirmed-plan','legacy','{}')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO operations(id,plan_id,project,pr,sha,actor,state,review_id,created_at,updated_at) VALUES('confirmed-operation','confirmed-plan','legacy',8,?,'reviewer','confirmed',77,'created','updated')", strings.Repeat("c", 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("INSERT INTO confirmations(operation_id,payload) VALUES('confirmed-operation','{}')"); err != nil {
		t.Fatal(err)
	}
	// Recreate the exact v7 boundary in this temporary database before exercising
	// the additive v7-to-v8 migration.
	if _, err := s.DB.Exec("DROP TABLE review_time_entries; PRAGMA user_version=7"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	var version int
	if err := migrated.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 8 {
		t.Fatalf("schema version after migration: %d, %v", version, err)
	}
	var projectCount, reviewCount, activityCount, planCount, operationCount, confirmationCount int
	for _, check := range []struct {
		query string
		dest  *int
	}{
		{"SELECT count(*) FROM projects WHERE name='legacy'", &projectCount},
		{"SELECT count(*) FROM local_reviews WHERE project='legacy'", &reviewCount},
		{"SELECT count(*) FROM activity_events WHERE runtime='runtime' AND id='v7-bind'", &activityCount},
		{"SELECT count(*) FROM plans WHERE project='legacy'", &planCount},
		{"SELECT count(*) FROM operations WHERE project='legacy'", &operationCount},
		{"SELECT count(*) FROM confirmations WHERE operation_id='confirmed-operation'", &confirmationCount},
	} {
		if err := migrated.DB.QueryRow(check.query).Scan(check.dest); err != nil {
			t.Fatal(err)
		}
	}
	if projectCount != 1 || reviewCount != 1 || activityCount != 1 || planCount != 2 || operationCount != 2 || confirmationCount != 1 {
		t.Fatalf("v7 state changed during migration: projects=%d reviews=%d activity=%d plans=%d operations=%d confirmations=%d", projectCount, reviewCount, activityCount, planCount, operationCount, confirmationCount)
	}
	var pending, confirmed int
	if err := migrated.DB.QueryRow("SELECT count(*) FROM operations WHERE state='intent'").Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := migrated.DB.QueryRow("SELECT count(*) FROM operations WHERE state='confirmed' AND review_id=77").Scan(&confirmed); err != nil {
		t.Fatal(err)
	}
	if pending != 1 || confirmed != 1 {
		t.Fatalf("approval states changed during migration: intent=%d confirmed=%d", pending, confirmed)
	}
}
