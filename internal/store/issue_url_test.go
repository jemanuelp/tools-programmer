package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestV3URLMigrationPreservesIssueActivity(t *testing.T) {
	s, path := openTest(t)
	// Recreate the historical v3 shape in isolated test state, with its guards.
	if _, err := s.DB.Exec("DROP TABLE checkout_rebindings; ALTER TABLE issues DROP COLUMN url; PRAGMA user_version=3;"); err != nil {
		t.Fatal(err)
	}
	register(t, s, "one")
	if _, err := s.DB.Exec("ALTER TABLE projects DROP COLUMN origin_url"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO issues(id,project,repository,number,remote_id,created_at) SELECT 42,name,repo,12,'I_old','old' FROM projects WHERE name='one';
INSERT INTO activity_segments VALUES('r','s','x',100,42);
INSERT INTO activity_events VALUES('r','e','s','x','bind','',100,'{"id":"e","runtime":"r","session":"s","segment":"x","kind":"bind","at":100,"project":"one","issue":12}');`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, opener := range []func(string) (*Store, error){OpenReadOnly, OpenExisting} {
		if db, err := opener(path); err == nil {
			db.Close()
			t.Fatal("accepted old schema")
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("non-migrating open changed database")
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, events, segments, err := s.IssueSnapshot("one", 12)
	if err != nil || h.Issue.ID != 42 || h.Issue.URL != "" || h.Issue.RemoteID != "I_old" || h.Issue.CreatedAt != "old" || len(events) != 1 || len(segments) != 1 || segments[0].IssueID == nil || *segments[0].IssueID != 42 {
		t.Fatalf("%+v %+v %+v %v", h, events, segments, err)
	}
	rows, err := s.DB.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("broken foreign key")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := s.DB.Exec("UPDATE issues SET url='changed' WHERE id=42"); err == nil {
		t.Fatal("append-only guard lost")
	}
	if _, err := s.AddIssue("one", 13, ""); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.IssueHistory("one", 13)
	if err != nil || legacy.Issue.URL != "" {
		t.Fatal(legacy, err)
	}
}

func TestOpenExistingDoesNotCreateState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if db, err := OpenExisting(path); err == nil {
		db.Close()
		t.Fatal("created database")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("database created", err)
	}
}
