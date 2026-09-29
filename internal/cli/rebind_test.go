package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func gitFixture(t *testing.T, base, origin string) string {
	t.Helper()
	root := filepath.Join(base, "checkout")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", root}, {"-C", root, "config", "user.name", "Review User"}, {"-C", root, "config", "user.email", "review@example.com"}, {"-C", root, "remote", "add", "origin", origin}} {
		cmd := exec.Command("git", args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
	}
	return root
}

func TestRebindCheckoutCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("Git subprocess fixture")
	}
	root := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
	dbPath := filepath.Join(t.TempDir(), "private", "history.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Project("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(core.Plan{ID: "prior-plan", Project: before}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DROP TABLE checkout_rebindings; PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	run := func(want int, path string) string {
		t.Helper()
		var out bytes.Buffer
		if code := Run(context.Background(), []string{"--db", dbPath, "project", "rebind-checkout", "--project", "demo", "--path", path}, &out, forbiddenRunner{t}); code != want {
			t.Fatalf("code %d: %s", code, out.String())
		}
		return out.String()
	}
	run(1, "relative")
	preflight, err := store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal("failed validation should leave additive migration in place", err)
	}
	unchanged, err := preflight.Project("demo")
	if err != nil || unchanged != before {
		t.Fatal(unchanged, err)
	}
	preflight.Close()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Join(root, "sub"), alias); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data core.Project `json:"data"`
	}
	if err := json.Unmarshal([]byte(run(0, alias)), &got); err != nil || got.Data.Path != root {
		t.Fatalf("project: %+v %v", got, err)
	}
	run(0, root)
	var blocked bytes.Buffer
	if code := Run(context.Background(), []string{"--db", dbPath, "approval", "apply", "--id", "prior-plan"}, &blocked, forbiddenRunner{t}); code != 1 || !strings.Contains(blocked.String(), "stored plan project policy differs") {
		t.Fatalf("old plan: %d %s", code, blocked.String())
	}
	s, err = store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM checkout_rebindings WHERE project='demo' AND path_before='' AND path_after=? AND result='success'", root).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit: %d %v", count, err)
	}
	for _, path := range []string{"", "relative", t.TempDir()} {
		if output := run(1, path); !strings.Contains(output, `"error"`) {
			t.Fatal(output)
		}
	}
	if err := s.DB.QueryRow("SELECT count(*) FROM checkout_rebindings").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestRebindCheckoutPolicyAndWorktrees(t *testing.T) {
	if testing.Short() {
		t.Skip("Git subprocess fixture")
	}
	root := gitFixture(t, t.TempDir(), "https://github.com/example/demo")
	other := gitFixture(t, t.TempDir(), "git@github-alias.com:example/demo.git")
	dbPath := filepath.Join(t.TempDir(), "private", "history.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: root, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	run := func(path string) (int, string) {
		t.Helper()
		var out bytes.Buffer
		code := Run(context.Background(), []string{"--db", dbPath, "project", "rebind-checkout", "--project", "demo", "--path", path}, &out, forbiddenRunner{t})
		return code, out.String()
	}
	if code, _ := run(other); code != 1 {
		t.Fatal("unregistered alias accepted")
	}
	s, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProjectOrigin("demo", "example/demo", "git@github-alias.com:example/demo.git"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, scenario := range []struct{ name, key, value string }{
		{"author", "GIT_AUTHOR_EMAIL", "other@example.com"},
		{"committer", "GIT_COMMITTER_NAME", "Other"},
		{"redirection", "GIT_DIR", filepath.Join(root, ".git")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv(scenario.key, scenario.value)
			if code, _ := run(other); code != 1 {
				t.Fatal("invalid Git environment accepted")
			}
		})
	}
	if code, output := run(other); code != 0 {
		t.Fatal(output)
	}
	// A linked worktree has a .git file rather than a .git directory.
	cmd := exec.Command("git", "-C", other, "commit", "--allow-empty", "-m", "fixture")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit fixture: %s: %v", output, err)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	cmd = exec.Command("git", "-C", other, "worktree", "add", "--detach", linked)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("linked fixture: %s: %v", output, err)
	}
	if code, output := run(linked); code != 0 || !strings.Contains(output, linked) {
		t.Fatalf("linked worktree: %d %s", code, output)
	}
	s, err = store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.CheckoutRebindings("demo")
	if err != nil || len(events) != 2 || events[0].PathBefore != root || events[1].PathBefore != other || events[1].PathAfter != linked {
		t.Fatal(events, err)
	}
}
