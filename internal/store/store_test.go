package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tools-programmer/internal/core"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}
func register(t *testing.T, s *Store, name string) {
	t.Helper()
	if _, err := s.AddProject(core.Project{Name: name, Repo: "example/" + name, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"}); err != nil {
		t.Fatal(err)
	}
}
func record(t *testing.T, s *Store, project, sha, outcome string) core.LocalReview {
	t.Helper()
	r, err := s.Record(core.LocalReview{Project: project, PR: 7, SHA: sha, Reviewer: "human", Method: "manual", Outcome: outcome})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecordMatchesListedFreshness(t *testing.T) {
	sha := strings.Repeat("a", 40)
	observedAt := "2026-09-09T00:00:00Z"
	for _, tt := range []struct {
		name, observedHead, freshness, lastObservedAt string
	}{
		{"unobserved", "", "unobserved", ""},
		{"current", sha, "current-at-last-observation", observedAt},
		{"stale", strings.Repeat("b", 40), "stale", observedAt},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := openTest(t)
			register(t, s, "one")
			if tt.observedHead != "" {
				if err := s.SaveSnapshot("one", core.Snapshot{PR: 7, Head: tt.observedHead, ObservedAt: observedAt}); err != nil {
					t.Fatal(err)
				}
			}
			recorded := record(t, s, "one", sha, "approved")
			listed, err := s.Reviews("one", 7)
			if err != nil || len(listed) != 1 {
				t.Fatalf("expected one appended review: %+v, %v", listed, err)
			}
			if recorded != listed[0] {
				t.Errorf("record/list mismatch:\nrecord: %+v\nlist:   %+v", recorded, listed[0])
			}
			if recorded.Freshness != tt.freshness || recorded.LastObservedAt != tt.lastObservedAt {
				t.Errorf("freshness = %q, observation = %q; want %q, %q", recorded.Freshness, recorded.LastObservedAt, tt.freshness, tt.lastObservedAt)
			}
			if recorded.ID <= 0 || recorded.SHA != sha || recorded.Outcome != "approved" || recorded.Origin != "local-recorded" {
				t.Fatalf("review identity changed: %+v", recorded)
			}
			var storedAt string
			if err := s.DB.QueryRow("SELECT recorded_at FROM local_reviews WHERE id=?", recorded.ID).Scan(&storedAt); err != nil {
				t.Fatal(err)
			}
			if recorded.RecordedAt != storedAt || listed[0].RecordedAt != storedAt {
				t.Fatal("recording timestamp changed between storage and responses")
			}
			if parsed, err := time.Parse(time.RFC3339Nano, storedAt); err != nil || parsed.Location() != time.UTC {
				t.Fatalf("invalid UTC recording timestamp: %q", storedAt)
			}
		})
	}
}

func TestPersistenceIsolationAndStaleness(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	register(t, s, "two")
	sha := strings.Repeat("a", 40)
	r := record(t, s, "one", sha, "approved")
	if r.Origin != "local-recorded" {
		t.Fatal(r)
	}
	parsed, err := time.Parse(time.RFC3339Nano, r.RecordedAt)
	if err != nil || parsed.Location() != time.UTC {
		t.Fatal("timestamp is not UTC", r.RecordedAt)
	}
	if err = s.SaveSnapshot("one", core.Snapshot{PR: 7, Head: sha, ObservedAt: core.Now(), Reviews: []core.RemoteReview{{ID: 1, Body: "sensitive remote body"}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reviews, err := s.Reviews("one", 0)
	if err != nil || len(reviews) != 1 || reviews[0].Freshness != "current-at-last-observation" {
		t.Fatalf("restart: %+v %v", reviews, err)
	}
	other, err := s.Reviews("two", 0)
	if err != nil || len(other) != 0 {
		t.Fatal("project leakage", other, err)
	}
	if err = s.SaveSnapshot("one", core.Snapshot{PR: 7, Head: strings.Repeat("b", 40), ObservedAt: core.Now()}); err != nil {
		t.Fatal(err)
	}
	reviews, err = s.Reviews("one", 0)
	if err != nil || reviews[0].Freshness != "stale" || reviews[0].SHA != sha {
		t.Fatal(reviews, err)
	}
	ok, err := s.Approved("one", 7, strings.Repeat("b", 40))
	if err != nil || ok {
		t.Fatal("new head inherited verdict")
	}
	record(t, s, "one", sha, "incomplete")
	ok, err = s.Approved("one", 7, sha)
	if err != nil || ok {
		t.Fatal("latest local verdict did not supersede")
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM confirmations").Scan(&count); err != nil || count != 0 {
		t.Fatal("local verdict became remote confirmation")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "sensitive remote body") {
		t.Fatal("remote body persisted")
	}
}
func TestSQLitePragmasSchemaAndPrivateStorage(t *testing.T) {
	s, path := openTest(t)
	for query, want := range map[string]int{"PRAGMA foreign_keys": 1, "PRAGMA busy_timeout": 5000, "PRAGMA synchronous": 2, "PRAGMA user_version": 7} {
		var got int
		if err := s.DB.QueryRow(query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %d %v", query, got, err)
		}
	}
	if _, err := s.DB.Exec("INSERT INTO snapshots VALUES('missing',1,'{}')"); err == nil {
		t.Fatal("foreign keys not enforced")
	}
	for p, want := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
		info, err := os.Stat(p)
		if err != nil || info.Mode().Perm() != want {
			t.Fatal("insecure permissions", p, info, err)
		}
	}
	if _, err := s.DB.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if reopened, err := Open(path); err == nil {
		reopened.Close()
		t.Fatal("newer schema accepted")
	}
}
func TestRejectUnsafeStorageAndInput(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	if _, err := s.AddProject(core.Project{Name: "evil'; DROP TABLE projects;--", Repo: "example/x"}); err == nil {
		t.Fatal("malicious project accepted")
	}
	if _, err := s.Record(core.LocalReview{Project: "one", PR: 1, SHA: "short", Reviewer: "me", Method: "manual", Outcome: "approved"}); err == nil {
		t.Fatal("short SHA accepted")
	}
	s.Close()
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(path); err == nil {
		reopened.Close()
		t.Fatal("public database accepted")
	}
	dir := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(filepath.Join(dir, "data.db")); err == nil {
		reopened.Close()
		t.Fatal("public directory accepted")
	}
	private := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(private, "link.db")); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(filepath.Join(private, "link.db")); err == nil {
		reopened.Close()
		t.Fatal("symlink accepted")
	}
}
func TestAtomicClaimAcrossPlansAndUnknownGlobalGate(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	project, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	p := core.Plan{ID: "plan", Project: project}
	if err := s.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	o := core.Operation{ID: "operation", PlanID: "plan", Project: "one", PR: 1, SHA: strings.Repeat("a", 40), Actor: "reviewer", CreatedAt: core.Now()}
	if _, err := s.Record(core.LocalReview{Project: "one", PR: 1, SHA: o.SHA, Reviewer: "human", Method: "manual", Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Claim(o); err != nil {
		t.Fatal(err)
	}
	if err := s.Claim(o); err == nil {
		t.Fatal("duplicate claim")
	}
	o.ID = "different"
	o.PR = 2
	if err := s.Claim(o); err == nil {
		t.Fatal("new mutation allowed while unresolved")
	}
	id, err := s.Unresolved()
	if err != nil || id != "plan" {
		t.Fatal(id, err)
	}
}

func TestConcurrentFirstOpenMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "history.db")
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path)
			if err == nil {
				err = s.Close()
			}
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	projects, err := s.Projects()
	if err != nil || len(projects) != 0 {
		t.Fatal(projects, err)
	}
}

func TestClaimRechecksRevokedVerdictAtomically(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	sha := strings.Repeat("a", 40)
	record(t, s, "one", sha, "approved")
	project, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(core.Plan{ID: "plan", Project: project}); err != nil {
		t.Fatal(err)
	}
	record(t, s, "one", sha, "changes_requested")
	o := core.Operation{ID: "intent", PlanID: "plan", Project: "one", PR: 7, SHA: sha, Actor: "reviewer", CreatedAt: core.Now()}
	if err := s.Claim(o); err == nil {
		t.Fatal("revoked verdict claimed")
	}
	if id, err := s.Unresolved(); err != nil || id != "" {
		t.Fatal("failed claim left intent", id, err)
	}
}
