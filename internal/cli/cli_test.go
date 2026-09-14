package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tools-programmer/internal/core"
	"tools-programmer/internal/github"
)

type forbiddenRunner struct{ t *testing.T }

func (r forbiddenRunner) Run(context.Context, []string, []byte) ([]byte, error) {
	r.t.Fatal("local command invoked GitHub")
	return nil, nil
}
func TestHelpVersionAndInvalidCommandsDoNotCreateDefaultState(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "untouched", "history.db")
	t.Setenv("TOOLS_PROGRAMMER_DB", db)
	for _, args := range [][]string{{"--help"}, {"--version"}, {"review", "record", "--help"}, {"approval", "apply", "--help"}, {"unknown", "command"}} {
		var out bytes.Buffer
		Run(context.Background(), args, &out, forbiddenRunner{t})
		if _, err := os.Stat(filepath.Dir(db)); !os.IsNotExist(err) {
			t.Fatal("help/invalid command created state")
		}
	}
}
func TestVersionJSONCompatibility(t *testing.T) {
	t.Setenv("TOOLS_PROGRAMMER_DB", filepath.Join(t.TempDir(), "absent", "history.db"))
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"--version"}, &out, forbiddenRunner{t}); code != 0 {
		t.Fatalf("version failed: %s", out.String())
	}
	// Existing consumers can still decode the data envelope as string values.
	var response struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := core.BuildVersion()
	if response.Data["version"] != want.Version || response.Data["commit"] != want.Commit || response.Data["state"] != want.State || response.Data["build"] != want.Build {
		t.Fatalf("unexpected version envelope: %s", out.String())
	}
}

func TestLocalCLIHistorySurvivesSeparateInvocations(t *testing.T) {
	db := filepath.Join(t.TempDir(), "state", "history.db")
	sha := strings.Repeat("a", 40)
	run := func(args ...string) string {
		t.Helper()
		var out bytes.Buffer
		code := Run(context.Background(), append([]string{"--db", db}, args...), &out, forbiddenRunner{t})
		if code != 0 {
			t.Fatal(out.String())
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal("not JSON")
		}
		return out.String()
	}
	run("project", "add", "--name", "demo", "--repo", "example/demo", "--github-login", "reviewer", "--git-name", "Review User", "--git-email", "review@example.com")
	run("review", "record", "--project", "demo", "--pr", "8", "--sha", sha, "--reviewer", "human", "--method", "manual", "--outcome", "approved", "--evidence", "test-output.txt")
	got := run("review", "list", "--project", "demo")
	if !strings.Contains(got, "local-recorded") || !strings.Contains(got, sha) || !strings.Contains(got, "unobserved") {
		t.Fatal(got)
	}
	var out bytes.Buffer
	if Run(context.Background(), []string{"--db", db, "approval", "plan", "--project", "demo", "--prs", "1,1"}, &out, forbiddenRunner{t}) == 0 {
		t.Fatal("duplicate PRs accepted")
	}
}

// The helper replaces gh on PATH. It cannot reach a network and only touches its
// isolated fixture. No product test invokes a real gh executable.
func TestFakeGHProcess(t *testing.T) {
	if os.Getenv("TP_HELPER_MODE") != "1" {
		return
	}
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_DEBUG"} {
		if os.Getenv(key) != "" {
			os.Exit(70)
		}
	}
	args := os.Args
	separator := 0
	for i, a := range args {
		if a == "--" {
			separator = i + 1
			break
		}
	}
	args = args[separator:]
	if len(args) < 10 || args[0] != "api" || args[1] != "--hostname" || args[2] != "github.com" {
		os.Exit(71)
	}
	method, path := args[4], args[9]
	statePath := os.Getenv("TP_FIXTURE_STATE")
	if os.Getenv("TP_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "private stderr sentinel")
		os.Exit(1)
	}
	if os.Getenv("TP_HELPER_FLOOD") == "1" {
		fmt.Print(strings.Repeat("x", 3<<20))
		os.Exit(0)
	}
	sha := strings.Repeat("a", 40)
	base := strings.Repeat("b", 40)
	var result any
	switch {
	case method == "POST" && path == "repos/example/demo/pulls/8/reviews":
		var body map[string]string
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 16000))
		if err != nil || json.Unmarshal(b, &body) != nil || body["event"] != "APPROVE" || body["commit_id"] != sha || !strings.Contains(body["body"], "tools-programmer-operation:") {
			os.Exit(72)
		}
		if _, err = os.Stat(statePath); err == nil {
			os.Exit(73)
		}
		result = map[string]any{"id": 900, "user": map[string]string{"login": "reviewer"}, "state": "APPROVED", "commit_id": sha, "body": body["body"], "submitted_at": "2026-09-09T00:00:00Z"}
		encoded, _ := json.Marshal(result)
		if os.WriteFile(statePath, encoded, 0600) != nil {
			os.Exit(74)
		}
	case method != "GET":
		os.Exit(75)
	case path == "user":
		actor := os.Getenv("TP_ACTOR")
		if actor == "" {
			actor = "reviewer"
		}
		if os.Getenv("TP_SWITCH_AFTER_WRITE") == "1" {
			if _, err := os.Stat(statePath); err == nil {
				actor = "other"
			}
		}
		result = map[string]string{"login": actor}
	case path == "repos/example/demo":
		result = map[string]any{"full_name": "example/demo", "permissions": map[string]bool{"push": true}}
	case path == "repos/example/demo/pulls/8":
		result = map[string]any{"number": 8, "state": "open", "draft": false, "user": map[string]string{"login": "author"}, "head": map[string]string{"sha": sha}, "base": map[string]any{"sha": base, "ref": "main", "repo": map[string]string{"full_name": "example/demo"}}}
	case strings.Contains(path, "/check-runs?"):
		result = map[string]any{"check_runs": []any{}}
	case strings.Contains(path, "/statuses?"):
		result = []any{map[string]any{"id": 1, "context": "ci", "state": "success"}}
	case strings.Contains(path, "/reviews?"):
		result = []any{}
		if b, err := os.ReadFile(statePath); err == nil {
			var r any
			if json.Unmarshal(b, &r) != nil {
				os.Exit(76)
			}
			result = []any{r}
		}
	case path == "repos/example/demo/pulls/8/reviews/900":
		b, err := os.ReadFile(statePath)
		if err != nil {
			os.Exit(77)
		}
		if json.Unmarshal(b, &result) != nil {
			os.Exit(78)
		}
	default:
		os.Exit(79)
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(80)
	}
	os.Exit(0)
}

func installFakeGH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec \"$TP_HELPER_EXECUTABLE\" -test.run=^TestFakeGHProcess$ -- \"$@\"\n"
	if err = os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TP_HELPER_EXECUTABLE", exe)
	t.Setenv("TP_HELPER_MODE", "1")
	t.Setenv("TP_FIXTURE_STATE", filepath.Join(dir, "remote-review.json"))
	return dir
}
func TestExecRunnerSuppressesSecretsAndBoundsOutput(t *testing.T) {
	installFakeGH(t)
	t.Setenv("GH_TOKEN", "fake-token-must-not-be-used")
	t.Setenv("GH_DEBUG", "api")
	runner := github.ExecRunner{}
	args := []string{"api", "--hostname", "github.com", "--method", "GET", "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28", "user"}
	b, err := runner.Run(context.Background(), args, nil)
	if err != nil || !strings.Contains(string(b), "reviewer") {
		t.Fatal(string(b), err)
	}
	t.Setenv("TP_HELPER_FAIL", "1")
	_, err = runner.Run(context.Background(), args, nil)
	if err == nil || strings.Contains(err.Error(), "private stderr sentinel") {
		t.Fatal("raw stderr leaked", err)
	}
	t.Setenv("TP_HELPER_FAIL", "0")
	t.Setenv("TP_HELPER_FLOOD", "1")
	if _, err = runner.Run(context.Background(), args, nil); err == nil {
		t.Fatal("flood accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = runner.Run(ctx, args, nil); err == nil {
		t.Fatal("cancelled execution accepted")
	}
}
func TestBinarySmokeWithFakeGitHub(t *testing.T) {
	if testing.Short() {
		t.Skip("binary subprocess smoke")
	}
	dir := installFakeGH(t)
	binary := os.Getenv("TOOLS_PROGRAMMER_TEST_BINARY")
	if binary == "" {
		binary = filepath.Join(dir, "tools-programmer")
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/tools-programmer")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(b), err)
		}
	}
	db := filepath.Join(dir, "state", "history.db")
	sha := strings.Repeat("a", 40)
	run := func(args ...string) []byte {
		t.Helper()
		// A command performs many sequential gh requests; race-instrumented fake
		// helpers also incur exit delays. Bound the whole smoke command separately
		// from the unchanged production per-request timeout.
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append([]string{"--db", db}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(string(b), err)
		}
		return b
	}
	run("--help")
	if _, err := os.Stat(filepath.Dir(db)); !os.IsNotExist(err) {
		t.Fatal("binary help created DB")
	}
	run("project", "add", "--name", "demo", "--repo", "example/demo", "--github-login", "reviewer", "--git-name", "Review User", "--git-email", "review@example.com")
	run("review", "record", "--project", "demo", "--pr", "8", "--sha", sha, "--reviewer", "human", "--method", "manual", "--outcome", "approved")
	if b := run("review", "list", "--project", "demo"); !bytes.Contains(b, []byte("local-recorded")) {
		t.Fatal(string(b))
	}
	run("pr", "sync", "--project", "demo", "--prs", "8")
	b := run("approval", "plan", "--project", "demo", "--prs", "8")
	var plan struct {
		Data core.Plan `json:"data"`
	}
	if json.Unmarshal(b, &plan) != nil || len(plan.Data.Items) != 1 || !plan.Data.Items[0].Eligible {
		t.Fatal(string(b))
	}
	if _, err := os.Stat(os.Getenv("TP_FIXTURE_STATE")); !os.IsNotExist(err) {
		t.Fatal("planning published review")
	}
	t.Setenv("TP_ACTOR", "other")
	cmd := exec.CommandContext(context.Background(), binary, "--db", db, "approval", "apply", "--id", plan.Data.ID)
	if output, err := cmd.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("identity_mismatch")) {
		t.Fatal("binary failed to reject wrong actor", string(output), err)
	}
	if _, err := os.Stat(os.Getenv("TP_FIXTURE_STATE")); !os.IsNotExist(err) {
		t.Fatal("wrong actor published")
	}
	t.Setenv("TP_ACTOR", "reviewer")
	b = run("approval", "apply", "--id", plan.Data.ID)
	if !bytes.Contains(b, []byte("github-confirmed")) {
		t.Fatal(string(b))
	}
	run("approval", "apply", "--id", plan.Data.ID)
	run("approval", "reconcile", "--id", plan.Data.ID)
	if b = run("approval", "show", "--id", plan.Data.ID); !bytes.Contains(b, []byte(`"review_id": 900`)) {
		t.Fatal(string(b))
	}
}
