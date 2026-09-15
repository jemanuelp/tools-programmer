package store

import (
	"path/filepath"
	"testing"
	"tools-programmer/internal/core"
)

func TestSetProjectOriginPreservesBindingAndHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: t.TempDir(), ExpectedGitHub: "reviewer", GitName: "User", GitEmail: "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot(p.Name, core.Snapshot{PR: 8, Head: "retained"}); err != nil {
		t.Fatal(err)
	}
	// Recreate the preceding schema without touching any historical rows.
	if _, err := s.DB.Exec("ALTER TABLE projects DROP COLUMN origin_url; PRAGMA user_version=4;"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	migrated, err := s.Project(p.Name)
	if err != nil || migrated.OriginURL != "https://github.com/example/demo" {
		t.Fatal(migrated, err)
	}
	alias := "git@github-P.com:Example/Demo.git"
	for i := 0; i < 2; i++ {
		got, err := s.SetProjectOrigin(p.Name, p.Repo, alias)
		if err != nil {
			t.Fatal(err)
		}
		got.OriginURL = p.OriginURL
		if got != p {
			t.Fatal("binding changed", got, p)
		}
	}
	for _, tt := range []struct{ repo, origin string }{
		{"other/demo", alias}, {p.Repo, "git@github-P.com:other/demo.git"},
		{p.Repo, "https://secret@github.com/example/demo"},
	} {
		if _, err := s.SetProjectOrigin(p.Name, tt.repo, tt.origin); err == nil {
			t.Fatal("invalid association accepted")
		}
	}
	got, _ := s.Project(p.Name)
	if got.OriginURL != alias {
		t.Fatal("failed write changed association")
	}
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	current, err := ro.CurrentActivity("test", "session", p.Path)
	ro.Close()
	if err != nil || current.ProjectResolution != "matched" || current.Repository != p.Repo {
		t.Fatal(current, err)
	}
	other, err := s.AddProject(core.Project{Name: "other", Repo: "enterprise.example/example/demo", ExpectedGitHub: "reviewer", GitName: "User", GitEmail: "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProjectOrigin(other.Name, other.Repo, alias); err == nil {
		t.Fatal("ambiguous association accepted")
	}
	if _, err := s.SetProjectOrigin(p.Name, p.Repo, "https://enterprise.example/example/demo"); err == nil {
		t.Fatal("canonical identity collision accepted")
	}
	if other.MatchesOrigin(alias) {
		t.Fatal("association leaked to unrelated project")
	}
	if _, err := s.AddProject(core.Project{Name: "collision", Repo: "github-p.com/example/demo", ExpectedGitHub: "reviewer", GitName: "User", GitEmail: "user@example.com"}); err == nil {
		t.Fatal("new canonical project claimed an existing alias")
	}
	if _, err := s.SetProjectOrigin(p.Name, p.Repo, "git@replacement:example/demo.git"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Project(p.Name)
	if got.MatchesOrigin(alias) || !got.MatchesOrigin("git@replacement:example/demo.git") {
		t.Fatal("replacement failed")
	}
	if _, err := s.SetProjectOrigin(p.Name, p.Repo, "https://github.com/example/demo"); err != nil {
		t.Fatal(err)
	}
	snaps, err := s.Snapshots(p.Name)
	if err != nil || len(snaps) != 1 || snaps[0].Head != "retained" {
		t.Fatal(snaps, err)
	}
}
