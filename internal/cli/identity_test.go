package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func TestCLIIdentityRegistrationBindingAndCheck(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "history.db")
	run := func(want int, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if code := Run(context.Background(), append([]string{"--db", dbPath}, args...), &out, forbiddenRunner{t}); code != want {
			t.Fatal(code, out.String())
		}
		return out.String()
	}
	run(1, "project", "check", "--project", "demo")
	if _, err := os.Stat(filepath.Dir(dbPath)); !os.IsNotExist(err) {
		t.Fatal("read-only check created state")
	}
	run(1, "project", "add", "--name", "demo", "--repo", "example/demo")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.DB.Exec(`INSERT INTO projects(name,repo,path,allow_no_checks,created_at) VALUES('demo','github.com/example/demo','',0,'legacy')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	run(1, "project", "check", "--project", "demo")
	run(1, "project", "bind", "--project", "demo", "--repo", "example/wrong", "--github-login", "reviewer", "--git-name", "Review User", "--git-email", "review@example.com")
	run(0, "project", "bind", "--project", "demo", "--repo", "example/demo", "--github-login", "reviewer", "--git-name", "Review User", "--git-email", "review@example.com")
	run(0, "project", "origin", "--project", "demo", "--repo", "example/demo", "--origin-url", "git@github-P.com:example/demo.git")
	if !strings.Contains(run(0, "project", "list"), `"expected_github_login": "reviewer"`) {
		t.Fatal("identity missing from listing")
	}
	if !testing.Short() {
		checkout := t.TempDir()
		t.Chdir(checkout)
		script := `#!/bin/sh
case "$*" in
"rev-parse --show-toplevel") pwd ;;
"config --local --get-all remote.origin.url") printf '%s\n' 'git@github-P.com:example/demo.git' ;;
"var GIT_AUTHOR_IDENT"|"var GIT_COMMITTER_IDENT") printf '%s\n' 'Review User <review@example.com> 1700000000 +0000' ;;
*) exit 99 ;;
esac
`
		if err := os.WriteFile(filepath.Join(checkout, "git"), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", checkout+string(os.PathListSeparator)+os.Getenv("PATH"))
		if output := run(0, "project", "check", "--project", "demo"); !strings.Contains(output, `"matches": true`) {
			t.Fatal(output)
		}
	}
	run(1, "project", "bind", "--project", "demo", "--repo", "example/demo", "--github-login", "other", "--git-name", "Other", "--git-email", "other@example.com")
}

func TestFakeGHIdentityChangeAfterWritePreservesUnknownUntilRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("fake gh subprocess")
	}
	dir := installFakeGH(t)
	db := filepath.Join(dir, "state", "history.db")
	run := func(want int, args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		if code := Run(context.Background(), append([]string{"--db", db}, args...), &out, nil); code != want {
			t.Fatal(code, out.String())
		}
		return out.Bytes()
	}
	run(0, "project", "add", "--name", "demo", "--repo", "example/demo", "--github-login", "reviewer", "--git-name", "Review User", "--git-email", "review@example.com")
	run(0, "project", "origin", "--project", "demo", "--repo", "example/demo", "--origin-url", "git@github-P.com:example/demo.git")
	run(0, "review", "record", "--project", "demo", "--pr", "8", "--sha", strings.Repeat("a", 40), "--reviewer", "human", "--method", "manual", "--outcome", "approved")
	var result struct {
		Data core.Plan `json:"data"`
	}
	if err := json.Unmarshal(run(0, "approval", "plan", "--project", "demo", "--prs", "8"), &result); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TP_SWITCH_AFTER_WRITE", "1")
	if b := run(1, "approval", "apply", "--id", result.Data.ID); !bytes.Contains(b, []byte("unknown_outcome")) {
		t.Fatal(string(b))
	}
	before := run(0, "approval", "show", "--id", result.Data.ID)
	run(1, "approval", "reconcile", "--id", result.Data.ID)
	after := run(0, "approval", "show", "--id", result.Data.ID)
	if !bytes.Equal(before, after) {
		t.Fatal("mismatched recovery mutated durable state")
	}
	t.Setenv("TP_SWITCH_AFTER_WRITE", "0")
	if b := run(0, "approval", "reconcile", "--id", result.Data.ID); !bytes.Contains(b, []byte("github-confirmed")) {
		t.Fatal(string(b))
	}
	if _, err := os.Stat(os.Getenv("TP_FIXTURE_STATE")); err != nil {
		t.Fatal(err)
	}
}
