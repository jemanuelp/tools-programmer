package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func TestExistingCheckoutContracts(t *testing.T) {
	t.Run("identity consent", func(t *testing.T) {
		var out bytes.Buffer
		code := Run(context.Background(), []string{"--db", filepath.Join(t.TempDir(), "unused.db"), "project", "update-git-identity", "--project", "demo", "--git-name", "New User", "--git-email", "new@example.com", "--path", t.TempDir()}, &out, forbiddenRunner{t})
		var r struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if code != 1 || r.Error.Code != "authorization_required" || r.Error.Message != "obtain explicit authorization for this project and tuple, then pass --authorized" {
			t.Fatalf("exit=%d output=%s", code, &out)
		}
	})
	t.Run("unapproved checkout", func(t *testing.T) {
		if testing.Short() {
			t.Skip("Git subprocess fixture")
		}
		primary := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
		other := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
		dbPath := filepath.Join(t.TempDir(), "private", "history.db")
		db, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: primary, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		db.Close()
		t.Chdir(other)
		var out bytes.Buffer
		code := Run(context.Background(), []string{"--db", dbPath, "project", "check", "--project", "demo"}, &out, forbiddenRunner{t})
		var r struct {
			Data struct {
				Author, Committer string
				Matches           bool
			}
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if code != 1 || r.Error.Code != "checkout_mismatch" || r.Error.Message != "current checkout does not match registered path" || r.Data.Author != "" || r.Data.Committer != "" || r.Data.Matches {
			t.Fatalf("exit=%d output=%s", code, &out)
		}
	})
}
