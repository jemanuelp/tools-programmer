package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bundle "tools-programmer"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func proposedGitEnvironment(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{"GIT_AUTHOR_NAME": "New User", "GIT_AUTHOR_EMAIL": "new@example.com", "GIT_COMMITTER_NAME": "New User", "GIT_COMMITTER_EMAIL": "new@example.com"} {
		t.Setenv(key, value)
	}
}

func identityCLI(t *testing.T, db, root string, extra ...string) (int, string) {
	t.Helper()
	args := []string{"--db", db, "project", "update-git-identity", "--project", "demo", "--git-name", "New User", "--git-email", "new@example.com", "--path", root}
	args = append(args, extra...)
	var out bytes.Buffer
	code := Run(context.Background(), args, &out, forbiddenRunner{t})
	return code, out.String()
}

func TestIdentityUpdateCLISeparateRebindAndOrdinaryGate(t *testing.T) {
	if testing.Short() {
		t.Skip("Git subprocess fixture")
	}
	old := gitFixture(t, t.TempDir(), "https://github.com/example/demo")
	root := gitFixture(t, t.TempDir(), "https://github.com/example/demo.git")
	path := filepath.Join(t.TempDir(), "private", "history.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: old, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com", AllowNoChecks: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	proposedGitEnvironment(t) // Config still has old tuple: effective environment is authoritative.
	for i := range 3 {
		code, out := identityCLI(t, path, root, "--authorized")
		if code != 0 {
			t.Fatal(out)
		}
		var result struct {
			Data store.IdentityReceipt `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		want := "no_op"
		if i == 0 {
			want = "changed"
		}
		if result.Data.Result != want || result.Data.ID == "" || result.Data.IdentityRevision != 1 {
			t.Fatal(out)
		}
		if strings.Contains(out, root) || strings.Contains(out, "New User <") || strings.Contains(out, "new@example.com") {
			t.Fatal("private diagnostic leaked", out)
		}
	}
	s, err = store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Project("demo")
	if err != nil {
		t.Fatal(err)
	}
	want := p
	want.GitName, want.GitEmail, want.IdentityRevision = "New User", "new@example.com", 1
	if got != want {
		t.Fatal("update changed unrelated registration", got)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM identity_changes").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	s.Close()
	t.Chdir(root)
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"--db", path, "project", "check", "--project", "demo"}, &out, forbiddenRunner{t}); code != 1 || !strings.Contains(out.String(), "checkout_mismatch") {
		t.Fatal("update silently rebound checkout", out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"--db", path, "project", "rebind-checkout", "--project", "demo", "--path", root}, &out, forbiddenRunner{t}); code != 0 {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := Run(context.Background(), []string{"--db", path, "project", "check", "--project", "demo"}, &out, forbiddenRunner{t}); code != 0 {
		t.Fatal("ordinary check failed after separate rebind", out.String())
	}
}

func TestIdentityUpdateCLIRefusesInvalidEvidenceAndConsent(t *testing.T) {
	if testing.Short() {
		t.Skip("Git subprocess fixture")
	}
	for _, scenario := range []string{"no_consent", "author_name", "author_email", "committer_name", "committer_email", "wrong_origin", "invalid_name", "invalid_email", "missing_name", "relative_path", "missing_project", "same_tuple_mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			origin := "https://github.com/example/demo"
			if scenario == "wrong_origin" {
				origin = "https://github.com/other/demo"
			}
			root := gitFixture(t, t.TempDir(), origin)
			path := filepath.Join(t.TempDir(), "private", "history.db")
			s, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			proposedGitEnvironment(t)
			extra := []string{"--authorized"}
			switch scenario {
			case "no_consent":
				extra = nil
			case "author_name":
				t.Setenv("GIT_AUTHOR_NAME", "Other")
			case "author_email":
				t.Setenv("GIT_AUTHOR_EMAIL", "other@example.com")
			case "committer_name":
				t.Setenv("GIT_COMMITTER_NAME", "Other")
			case "committer_email":
				t.Setenv("GIT_COMMITTER_EMAIL", "other@example.com")
			case "invalid_name":
				extra = append(extra, "--git-name", "Bad\nName")
			case "invalid_email":
				extra = append(extra, "--git-email", "invalid")
			case "missing_name":
				extra = append(extra, "--git-name", "")
			case "relative_path":
				root = "relative"
			case "missing_project":
				extra = append(extra, "--project", "missing")
			case "same_tuple_mismatch":
				extra = append(extra, "--git-name", "Review User", "--git-email", "review@example.com")
			}
			if code, out := identityCLI(t, path, root, extra...); code != 1 || !strings.Contains(out, `"error"`) {
				t.Fatal("invalid request accepted", code, out)
			}
			s, err = store.OpenReadOnly(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := s.Project("demo")
			if err != nil || got != p {
				t.Fatal("failed request changed registration", got, err)
			}
			var count int
			if err := s.DB.QueryRow("SELECT count(*) FROM identity_changes").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
	// Consent rejection happens before opening even a missing database.
	path := filepath.Join(t.TempDir(), "not-created", "history.db")
	if code, _ := identityCLI(t, path, "/checkout"); code != 1 {
		t.Fatal("no-consent accepted")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("no-consent created state", err)
	}
}

func TestIdentityUpdateHelpAndShippedGuidance(t *testing.T) {
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"project", "update-git-identity", "--help"}, &out, forbiddenRunner{t}); code != 0 {
		t.Fatal(out.String())
	}
	for _, text := range []string{"project update-git-identity", "--authorized", "no_op", "author AND committer", "Atomic audit failures", "project check"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("help missing contract", text)
		}
	}
	for _, path := range []string{".opencode/skills/tools-programmer/SKILL.md", ".opencode/skills/tools-programmer/references/cli.md"} {
		body, err := bundle.SkillFiles.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"project update-git-identity", "MCP", "consent", "A -> B -> A", "pre-commit check"} {
			if !strings.Contains(string(body), text) {
				t.Fatal("shipped guidance missing contract", path, text)
			}
		}
	}
}
