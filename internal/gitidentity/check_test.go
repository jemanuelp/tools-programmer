package gitidentity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tools-programmer/internal/core"
)

func TestCheckUsesCurrentCheckoutAndBothEffectiveIdentities(t *testing.T) {
	if testing.Short() {
		t.Skip("fake git subprocess")
	}
	for _, tt := range []struct {
		name, key, value string
		valid            bool
	}{
		{"matching", "", "", true},
		{"author override", "GIT_AUTHOR_NAME", "Different", false},
		{"author email override", "GIT_AUTHOR_EMAIL", "other@example.com", false},
		{"committer override", "GIT_COMMITTER_NAME", "Different", false},
		{"committer email override", "GIT_COMMITTER_EMAIL", "other@example.com", false},
		{"redirect", "GIT_DIR", "/must/not/be/executed", false},
		{"wrong origin", "TP_ORIGIN", "https://github.com/other/repo.git", false},
		{"malformed identity", "GIT_AUTHOR_NAME", "Bad\nName", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
				if strings.HasSuffix(key, "NAME") {
					t.Setenv(key, "Review User")
				} else {
					t.Setenv(key, "review@example.com")
				}
			}
			t.Setenv("TP_ROOT", dir)
			t.Setenv("TP_ORIGIN", "git@github.com:example/demo.git")
			script := `#!/bin/sh
case "$*" in
"rev-parse --show-toplevel") printf '%s\n' "$TP_ROOT" ;;
"config --local --get-all remote.origin.url") printf '%s\n' "$TP_ORIGIN" ;;
"var GIT_AUTHOR_IDENT") printf '%s <%s> 1700000000 +0000\n' "$GIT_AUTHOR_NAME" "$GIT_AUTHOR_EMAIL" ;;
"var GIT_COMMITTER_IDENT") printf '%s <%s> 1700000000 +0000\n' "$GIT_COMMITTER_NAME" "$GIT_COMMITTER_EMAIL" ;;
*) exit 99 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			if tt.key != "" {
				t.Setenv(tt.key, tt.value)
			}
			p := core.Project{Name: "demo", Repo: "github.com/example/demo", Path: dir, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"}
			r, err := Check(context.Background(), p)
			if (err == nil) != tt.valid || r.Matches != tt.valid {
				t.Fatal(r, err)
			}
			if tt.valid {
				p.Path = "/must/not/be/executed"
				if _, err := Check(context.Background(), p); err == nil {
					t.Fatal("stored path mismatch accepted")
				}
			}
		})
	}
}

func TestRemoteAssociationRejectsAmbiguousURLs(t *testing.T) {
	for _, raw := range []string{"https://github.com/example/demo.git", "ssh://git@github.com/example/demo.git", "git@github.com:example/demo.git"} {
		if repo, err := remoteRepo(raw); err != nil || repo != "github.com/example/demo" {
			t.Fatal(raw, repo, err)
		}
	}
	for _, raw := range []string{"ext::arbitrary command", "file:///tmp/repo", "https://token@github.com/example/demo", "ssh://git@github.com:2222/example/demo", "https://github.com/example/demo.git\nhttps://github.com/other/repo.git"} {
		if _, err := remoteRepo(raw); err == nil {
			t.Fatal("unsafe origin accepted", raw)
		}
	}
}

func TestRealGitVarHonorsEnvironmentWithoutConfigWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("local read-only git subprocess")
	}
	t.Chdir(t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_AUTHOR_NAME", "Environment Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "author@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Environment Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "committer@example.com")
	for variable, want := range map[string]string{"GIT_AUTHOR_IDENT": "Environment Author <author@example.com>", "GIT_COMMITTER_IDENT": "Environment Committer <committer@example.com>"} {
		got, err := run(context.Background(), "var", variable)
		if err != nil || !strings.HasPrefix(got, want+" ") {
			t.Fatal(got, err)
		}
	}
}
