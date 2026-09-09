package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"tools-programmer/internal/core"
)

func TestV1MigrationPreservesHistoryAndExplicitBinding(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "legacy.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO projects VALUES('legacy','github.com/example/legacy','',0,'original-time')"); err != nil {
		t.Fatal(err)
	}
	old := &Store{DB: db}
	sha := strings.Repeat("a", 40)
	record(t, old, "legacy", sha, "approved")
	if err = old.SaveSnapshot("legacy", core.Snapshot{PR: 7, Head: sha, Actor: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if err = old.SavePlan(core.Plan{ID: "legacy-plan", Project: core.Project{Name: "legacy", Repo: "github.com/example/legacy"}}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"confirmed", "unknown", "intent"} {
		if _, err = db.Exec(`INSERT INTO operations VALUES(?, 'legacy-plan','legacy',7,?,'reviewer',?,0,'created','updated')`, state, sha+state, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO confirmations VALUES('confirmed','{"operation_id":"confirmed","origin":"github-confirmed"}')`); err != nil {
		t.Fatal(err)
	}
	queries := []string{"SELECT name,repo,path,allow_no_checks,created_at FROM projects", "SELECT * FROM snapshots", "SELECT * FROM local_reviews", "SELECT * FROM plans", "SELECT * FROM operations", "SELECT * FROM confirmations"}
	readRows := func(db *sql.DB, query string) [][]any {
		t.Helper()
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var result [][]any
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			result = append(result, values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := map[string][][]any{}
	for _, q := range queries {
		before[q] = readRows(db, q)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if ro, err := OpenReadOnly(path); err == nil {
		ro.Close()
		t.Fatal("read-only check accepted or migrated v1")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range queries {
		if !reflect.DeepEqual(before[q], readRows(s.DB, q)) {
			t.Fatal("migration changed existing data", q)
		}
	}
	p, err := s.Project("legacy")
	if err != nil || p.ValidateIdentity() == nil {
		t.Fatal("legacy silently bound", p, err)
	}
	if _, err = s.BindProject("legacy", "example/other", "reviewer", "Review User", "review@example.com"); err == nil {
		t.Fatal("wrong repository bound")
	}
	if _, err = s.BindProject("legacy", "example/legacy", "other", "Review User", "review@example.com"); err == nil {
		t.Fatal("unresolved actor replaced")
	}
	p, err = s.BindProject("legacy", "example/legacy", "Reviewer", "Review User", "review@example.com")
	if err != nil || p.ExpectedGitHub != "Reviewer" {
		t.Fatal(p, err)
	}
	if _, err = s.BindProject("legacy", "example/legacy", "reviewer", "Other", "other@example.com"); err == nil {
		t.Fatal("binding replaced")
	}
	for _, q := range queries {
		if !reflect.DeepEqual(before[q], readRows(s.DB, q)) {
			t.Fatal("binding changed history", q)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err = s.Project("legacy")
	if err != nil || p.ExpectedGitHub != "Reviewer" {
		t.Fatal("binding lost after restart", p, err)
	}
}

func TestRegistrationRequiresExplicitIdentity(t *testing.T) {
	s, _ := openTest(t)
	if _, err := s.AddProject(core.Project{Name: "missing", Repo: "example/missing"}); err == nil {
		t.Fatal("registration inferred identity")
	}
	ps, err := s.Projects()
	if err != nil || len(ps) != 0 {
		t.Fatal(ps, err)
	}
}

func TestReadOnlyStoreCannotMutateIdentity(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err = ro.Project("one"); err != nil {
		t.Fatal(err)
	}
	if _, err = ro.DB.Exec("UPDATE projects SET expected_github='other'"); err == nil {
		t.Fatal("read-only state allowed a write")
	}
}
