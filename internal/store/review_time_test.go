package store

import (
	"path/filepath"
	"strings"
	"testing"

	"tools-programmer/internal/core"
)

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
