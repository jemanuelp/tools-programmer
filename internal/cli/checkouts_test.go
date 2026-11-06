package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func TestAuthorizedWorktreesShareProject(t *testing.T) {
	if testing.Short() {
		t.Skip("Git subprocess fixture")
	}
	root := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
	for _, args := range [][]string{{"-C", root, "commit", "--allow-empty", "-m", "fixture"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %s: %v", out, err)
		}
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "--detach", linked).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %s: %v", out, err)
	}
	unapproved := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
	wrongOrigin := gitFixture(t, t.TempDir(), "https://github.com/example/other.git")
	dbPath := filepath.Join(t.TempDir(), "private", "history.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: root, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Record(core.LocalReview{Project: "demo", PR: 8, SHA: strings.Repeat("a", 40), Reviewer: "human", Method: "manual", Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	run := func(want int, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if code := Run(context.Background(), append([]string{"--db", dbPath}, args...), &out, forbiddenRunner{t}); code != want {
			t.Fatalf("%v: code %d: %s", args, code, out.String())
		}
		return out.String()
	}
	t.Chdir(linked)
	if out := run(1, "project", "check", "--project", "demo"); !strings.Contains(out, "checkout_mismatch") {
		t.Fatal(out)
	}
	run(1, "project", "authorize-checkout", "--project", "demo", "--path", linked)
	run(1, "project", "authorize-checkout", "--project", "demo", "--path", wrongOrigin, "--authorized")
	run(1, "project", "authorize-checkout", "--project", "demo", "--path", "relative", "--authorized")
	run(0, "project", "authorize-checkout", "--project", "demo", "--path", linked, "--authorized")
	run(0, "project", "authorize-checkout", "--project", "demo", "--path", linked, "--authorized")
	if out := run(0, "project", "checkouts", "--project", "demo"); !strings.Contains(out, linked) || !strings.Contains(out, root) {
		t.Fatal(out)
	}
	for _, checkout := range []string{root, linked, root, linked} {
		t.Chdir(checkout)
		if out := run(0, "project", "check", "--project", "demo"); !strings.Contains(out, `"matches": true`) {
			t.Fatal(out)
		}
		if out := run(0, "review", "list", "--project", "demo"); !strings.Contains(out, strings.Repeat("a", 40)) {
			t.Fatal("history was not shared", out)
		}
	}
	t.Chdir(unapproved)
	run(1, "project", "check", "--project", "demo")
	t.Chdir(linked)
	t.Run("wrong author", func(t *testing.T) {
		t.Setenv("GIT_AUTHOR_EMAIL", "wrong@example.com")
		run(1, "project", "check", "--project", "demo")
	})
	t.Run("wrong committer", func(t *testing.T) {
		t.Setenv("GIT_COMMITTER_EMAIL", "wrong@example.com")
		run(1, "project", "check", "--project", "demo")
	})
	t.Run("redirection", func(t *testing.T) {
		t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
		run(1, "project", "check", "--project", "demo")
	})
	s, err = store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, checkout := range []string{root, linked} {
		activity, err := s.CurrentActivity("opencode", "native-session", checkout)
		if err != nil || activity.Project != p.Name || activity.ProjectResolution != "matched" {
			t.Fatalf("activity: %+v %v", activity, err)
		}
	}
	current, err := s.Project("demo")
	if err != nil || current.Path != p.Path || current.GitName != p.GitName || current.GitEmail != p.GitEmail {
		t.Fatal("primary checkout or identity changed", current, err)
	}
	s.Close()
	run(1, "project", "revoke-checkout", "--project", "demo", "--path", root, "--authorized")
	run(1, "project", "revoke-checkout", "--project", "demo", "--path", linked)
	run(0, "project", "revoke-checkout", "--project", "demo", "--path", linked, "--authorized")
	run(1, "project", "check", "--project", "demo")
	t.Chdir(root)
	run(0, "project", "check", "--project", "demo")
	s, err = store.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	activity, err := s.CurrentActivity("opencode", "native-session", linked)
	if err != nil || activity.ProjectResolution != "unavailable" {
		t.Fatalf("revoked activity: %+v %v", activity, err)
	}
	if reviews, err := s.Reviews("demo", 8); err != nil || len(reviews) != 1 {
		t.Fatal("revocation changed history", reviews, err)
	}
}

func TestRevokeRecordedCheckoutAfterSymlinkReplacement(t *testing.T) {
	if testing.Short() {
		t.Skip("Git subprocess fixture")
	}
	primary := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
	extra := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
	dbPath := filepath.Join(t.TempDir(), "private", "history.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: primary, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	run := func(want int, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if code := Run(context.Background(), append([]string{"--db", dbPath}, args...), &out, forbiddenRunner{t}); code != want {
			t.Fatalf("%v: code %d: %s", args, code, out.String())
		}
		return out.String()
	}
	run(0, "project", "authorize-checkout", "--project", "demo", "--path", extra, "--authorized")
	moved := filepath.Join(filepath.Dir(extra), "moved")
	if err := os.Rename(extra, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, extra); err != nil {
		t.Fatal(err)
	}
	t.Chdir(moved)
	run(0, "project", "check", "--project", "demo")
	if output := run(0, "project", "revoke-checkout", "--project", "demo", "--path", extra, "--authorized"); !strings.Contains(output, `"action": "revoked"`) {
		t.Fatal("recorded root was not revoked", output)
	}
	run(1, "project", "check", "--project", "demo")
	if output := run(0, "project", "checkouts", "--project", "demo"); strings.Contains(output, extra) {
		t.Fatal("revoked root remains listed", output)
	}
}
