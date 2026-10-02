package mcpserver

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/service"
	"tools-programmer/internal/store"
)

func projectArguments() map[string]any {
	return map[string]any{"name": "new", "repo": "Example/New", "github_login": "Explicit", "git_name": "Explicit Name", "git_email": "explicit@example.com"}
}

func TestProjectRegistrationProtocol(t *testing.T) {
	_, path := history(t)
	db, err := store.OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	before, _ := (&service.Service{Store: db}).Show("plan-test")
	baseline, _ := db.Projects()
	defaultSession, ctx := connect(t, db)
	r, err := defaultSession.CallTool(ctx, &mcp.CallToolParams{Name: "project_register", Arguments: projectArguments()})
	if err == nil && !r.IsError {
		t.Fatal("default enabled project registration")
	}
	session, ctx := connect(t, db, Options{EnableProjectRegistration: true})
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 12 {
		t.Fatalf("%+v %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Name == "project_register" {
			a := tool.Annotations
			if a == nil || a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint || a.OpenWorldHint == nil || *a.OpenWorldHint || !a.IdempotentHint {
				t.Fatalf("unsafe annotations: %+v", a)
			}
		}
	}
	for _, field := range []string{"name", "repo", "github_login", "git_name", "git_email"} {
		for _, value := range []any{nil, "", "bad\nSECRET", 42} {
			t.Run(field, func(t *testing.T) {
				args := projectArguments()
				args[field] = value
				assertProjectRejected(t, session, ctx, args)
			})
		}
		args := projectArguments()
		delete(args, field)
		assertProjectRejected(t, session, ctx, args)
	}
	for _, extra := range []map[string]any{
		{"repo": "https://SECRET@github.com/example/new"}, {"repo": "example/new.git"},
		{"git_email": "not-an-email"}, {"git_name": " padded "},
		{"path": nil}, {"allow_no_checks": nil}, {"allow_no_checks": "true"},
		{"session": "forbidden"}, {"expected_github_login": "inferred"},
	} {
		args := projectArguments()
		maps.Copy(args, extra)
		assertProjectRejected(t, session, ctx, args)
	}
	unchanged, _ := db.Projects()
	if !reflect.DeepEqual(baseline, unchanged) {
		t.Fatal("rejected calls changed registry")
	}
	// Process identity and policy must never fill or override explicit declarations.
	t.Setenv("GIT_AUTHOR_NAME", "Unrelated")
	t.Setenv("GIT_COMMITTER_EMAIL", "unrelated@example.com")
	t.Setenv("GH_USER", "Unrelated")
	t.Setenv("ALLOW_NO_CHECKS", "true")
	args := projectArguments()
	args["path"] = "checkout/../project"
	r, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "project_register", Arguments: args})
	if err != nil || r.IsError {
		t.Fatalf("%+v %v", r, err)
	}
	encoded, _ := json.Marshal(r.StructuredContent)
	var got struct {
		Data core.Project `json:"data"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil || len(r.Content) != 1 {
		t.Fatalf("%s %v", encoded, err)
	}
	wantPath, _ := filepath.Abs("project")
	p, err := db.Project("new")
	if err != nil || p != got.Data || p.Repo != "github.com/example/new" || p.Path != wantPath || p.AllowNoChecks || p.ExpectedGitHub != "Explicit" || p.GitName != "Explicit Name" || p.GitEmail != "explicit@example.com" || p.CreatedAt == "" {
		t.Fatalf("%+v %v", p, err)
	}
	for _, extra := range []map[string]any{{"git_name": "Replacement"}, {"name": "other"}, {"repo": "example/other"}} {
		duplicate := maps.Clone(args)
		maps.Copy(duplicate, extra)
		assertProjectRejected(t, session, ctx, duplicate)
	}
	stored, _ := db.Project("new")
	if stored != p {
		t.Fatal("duplicate overwrote binding")
	}
	args = projectArguments()
	args["name"], args["repo"], args["allow_no_checks"] = "enterprise", "GIT.Example.COM/Owner/Repo", true
	r, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "project_register", Arguments: args})
	if err != nil || r.IsError {
		t.Fatalf("%+v %v", r, err)
	}
	p, _ = db.Project("enterprise")
	if p.Path != "" || !p.AllowNoChecks || p.Repo != "git.example.com/owner/repo" {
		t.Fatalf("%+v", p)
	}
	after, _ := (&service.Service{Store: db}).Show("plan-test")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("registration changed approval history")
	}
	for _, table := range []string{"issues", "activity_segments", "activity_events", "issue_estimates", "issue_confirmations", "issue_outcomes"} {
		var n int
		if err := db.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("registration changed %s: %d %v", table, n, err)
		}
	}
}

func assertProjectRejected(t *testing.T, session *mcp.ClientSession, ctx context.Context, args map[string]any) {
	t.Helper()
	r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "project_register", Arguments: args})
	if err == nil && !r.IsError {
		t.Fatalf("accepted invalid registration: %+v", args)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "SECRET") || (err != nil && strings.Contains(err.Error(), "SECRET")) {
		t.Fatalf("unsanitized error: %s %v", b, err)
	}
}

func TestProjectRegistrationCancellationAndStorageErrors(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "private", "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	in := projectRegistrationInput{Name: "demo", Repo: "example/demo", GitHubLogin: "User", GitName: "User", GitEmail: "user@example.com"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = registerProject(ctx, db, in)
	if err == nil || err.Error() != "request canceled" {
		t.Fatal(err)
	}
	// Hold the only connection so cancellation occurs while persistence is waiting.
	db.DB.SetMaxOpenConns(1)
	conn, err := db.DB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _, err = registerProject(ctx, db, in)
	conn.Close()
	if err == nil || err.Error() != "request canceled" || db.DB.Stats().WaitCount == 0 {
		t.Fatalf("cancellation did not reach persistence: %v", err)
	}
	projects, err := db.Projects()
	if err != nil || len(projects) != 0 {
		t.Fatalf("partial registration: %+v %v", projects, err)
	}
	db.Close()
	_, _, err = registerProject(context.Background(), db, in)
	if err == nil || err.Error() != "storage_error: cannot register local project" {
		t.Fatalf("unsafe storage error: %v", err)
	}
}

func TestProjectRegistrationCompetingDuplicates(t *testing.T) {
	_, path := history(t)
	db, err := store.OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	session, ctx := connect(t, db, Options{EnableProjectRegistration: true})
	results := make(chan bool, 2)
	for range 2 {
		go func() {
			r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "project_register", Arguments: projectArguments()})
			results <- err == nil && !r.IsError
		}()
	}
	a, b := <-results, <-results
	projects, err := db.Projects()
	if a == b || err != nil || len(projects) != 2 {
		t.Fatalf("competing duplicates: %v %v %+v %v", a, b, projects, err)
	}
}
