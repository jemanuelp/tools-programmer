package gitidentity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"tools-programmer/internal/core"
)

func TestRealCheckoutAssociationAndEnvironmentPrecedence(t *testing.T) {
	if testing.Short() {
		t.Skip("real local read-only Git against temporary fixture")
	}
	dir := t.TempDir()
	for _, sub := range []string{".git/objects", ".git/refs/heads", "subdir"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := "[core]\nrepositoryformatversion = 0\nbare = false\n[user]\nname = Config User\nemail = config@example.com\n[remote \"origin\"]\nurl = https://github.com/example/demo.git\n"
	for name, data := range map[string]string{".git/HEAD": "ref: refs/heads/main\n", ".git/config": config} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(dir, "subdir"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_COUNT", "0")
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(key, "Expected User")
	}
	for _, key := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(key, "expected@example.com")
	}
	p := core.Project{Name: "demo", Repo: "github.com/example/demo", Path: dir, ExpectedGitHub: "reviewer", GitName: "Expected User", GitEmail: "expected@example.com"}
	if result, err := Check(context.Background(), p); err != nil || !result.Matches {
		t.Fatal(result, err)
	}
	t.Setenv("GIT_COMMITTER_EMAIL", "wrong@example.com")
	if _, err := Check(context.Background(), p); err == nil {
		t.Fatal("committer override ignored")
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".git/config")); err != nil || string(data) != config {
		t.Fatal("checker changed configuration", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git/index")); !os.IsNotExist(err) {
		t.Fatal("checker created index")
	}
}
