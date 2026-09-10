package mcpserver

import (
	"context"
	"encoding/json"
	"os"
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

func history(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "history.db")
	w, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := w.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "reviewer@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range []int{1, 2, 3} {
		if _, err := w.Record(core.LocalReview{Project: p.Name, PR: pr, SHA: strings.Repeat("a", 40), Reviewer: "reviewer", Method: "manual", Outcome: "approved"}); err != nil {
			t.Fatal(err)
		}
	}
	for pr, sha := range map[int]string{1: "a", 2: "b"} {
		if err := w.SaveSnapshot(p.Name, core.Snapshot{PR: pr, Head: strings.Repeat(sha, 40), ObservedAt: "2026-09-09T10:00:00Z", Reviews: []core.RemoteReview{{Body: "SECRET_REMOTE_BODY"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.SavePlan(core.Plan{ID: "plan-test", Project: p, CreatedAt: core.Now(), Items: []core.PlanItem{}}); err != nil {
		t.Fatal(err)
	}
	confirmed := core.Operation{ID: "confirmed-test", PlanID: "plan-test", Project: p.Name, PR: 3, SHA: strings.Repeat("a", 40), Actor: "reviewer", CreatedAt: core.Now()}
	if err := w.Claim(confirmed); err != nil {
		t.Fatal(err)
	}
	if err := w.Confirm(confirmed, core.RemoteReview{ID: 42, Actor: "reviewer", State: "APPROVED", SHA: confirmed.SHA, SubmittedAt: core.Now(), Body: "SECRET_REMOTE_BODY"}); err != nil {
		t.Fatal(err)
	}
	op := core.Operation{ID: "operation-test", PlanID: "plan-test", Project: p.Name, PR: 1, SHA: strings.Repeat("a", 40), Actor: "reviewer", CreatedAt: core.Now()}
	if err := w.Claim(op); err != nil {
		t.Fatal(err)
	}
	if err := w.Unknown(op.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func connect(t *testing.T, db *store.Store) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a, b := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- New(db).Run(ctx, a) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(ctx, b, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		session.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("server did not stop")
		}
	})
	return session, ctx
}

func TestReadOnlyProtocolAndStoredSemantics(t *testing.T) {
	db, path := history(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	session, ctx := connect(t, db)
	if session.InitializeResult().ProtocolVersion == "" {
		t.Fatal("no negotiated protocol version")
	}
	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("unsafe annotation: %+v", tool)
		}
	}
	if !reflect.DeepEqual(names, []string{"approval_show", "issue_summary", "pr_list", "project_list", "review_list"}) {
		t.Fatalf("surface: %v", names)
	}
	projects, _ := db.Projects()
	snapshots, _ := db.Snapshots("demo")
	reviews, _ := db.Reviews("demo", 0)
	filtered, _ := db.Reviews("demo", 1)
	report, _ := (&service.Service{Store: db}).Show("plan-test")
	if len(report.Confirmations) != 1 || len(report.Operations) != 2 || report.Operations[0].State != "unknown" || report.Operations[1].State != "confirmed" {
		t.Fatal(report)
	}
	if reviews[0].Freshness != "current-at-last-observation" || reviews[1].Freshness != "stale" || reviews[2].Freshness != "unobserved" {
		t.Fatal(reviews)
	}
	for _, tt := range []struct {
		name string
		args any
		want any
	}{
		{"project_list", map[string]any{}, projects},
		{"pr_list", map[string]any{"project": "demo"}, snapshots},
		{"review_list", map[string]any{"project": "demo"}, reviews},
		{"review_list", map[string]any{"project": "demo", "pr": 1}, filtered},
		{"approval_show", map[string]any{"id": "plan-test"}, report},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tt.name, Arguments: tt.args})
			if err != nil || got.IsError {
				t.Fatalf("%+v %v", got, err)
			}
			encoded, err := json.Marshal(got.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			var a, b any
			json.Unmarshal(actual["data"], &a)
			want, _ := json.Marshal(tt.want)
			json.Unmarshal(want, &b)
			if !reflect.DeepEqual(a, b) || strings.Contains(string(encoded), "SECRET_REMOTE_BODY") {
				t.Fatalf("got %s want %s", encoded, want)
			}
			if len(got.Content) != 1 {
				t.Fatalf("missing compatibility text: %+v", got)
			}
		})
	}
	if _, err := db.DB.Exec("DELETE FROM projects"); err == nil {
		t.Fatal("store accepted a mutation")
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("history changed", err)
	}
}

func TestToolErrorsAndValidation(t *testing.T) {
	db, _ := history(t)
	session, ctx := connect(t, db)
	for _, tt := range []struct {
		name string
		args any
	}{
		{"project_list", map[string]any{"extra": true}},
		{"pr_list", map[string]any{}},
		{"pr_list", map[string]any{"project": 12}},
		{"pr_list", map[string]any{"project": "missing"}},
		{"pr_list", map[string]any{"project": ""}},
		{"review_list", map[string]any{"project": "demo", "pr": 0}},
		{"review_list", map[string]any{"project": "demo", "pr": -1}},
		{"review_list", map[string]any{"project": "demo", "pr": "1"}},
		{"review_list", map[string]any{"project": "demo", "pr": 1.5}},
		{"review_list", map[string]any{"project": "demo", "pr": nil}},
		{"review_list", map[string]any{"project": "demo", "unknown": 1}},
		{"approval_show", map[string]any{"id": "missing"}},
		{"approval_show", map[string]any{"id": ""}},
		{"approval_apply", map[string]any{"id": "plan-test"}},
		{"project_check", map[string]any{"project": "demo"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tt.name, Arguments: tt.args})
			if err == nil && (got == nil || !got.IsError) {
				t.Fatalf("accepted invalid call: %+v", got)
			}
		})
	}
	db.Close()
	got, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "project_list"})
	if err != nil || !got.IsError {
		t.Fatalf("%+v %v", got, err)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), "storage_error") || strings.Contains(string(b), "sql:") {
		t.Fatalf("unsanitized storage failure: %s", b)
	}
}

func TestRequestCancellationReleasesWaitingQuery(t *testing.T) {
	db, _ := history(t)
	session, ctx := connect(t, db)
	// Occupy the only database connection so cancellation must reach database/sql.
	conn, err := db.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: "project_list"}); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for db.DB.Stats().WaitCount == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if db.DB.Stats().WaitCount == 0 {
		t.Fatal("query never reached pool")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled call succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not cancel")
	}
	for db.DB.Stats().WaitDuration == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if db.DB.Stats().WaitDuration == 0 {
		t.Fatal("query did not leave connection wait")
	}
}
