package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/service"
	"tools-programmer/internal/store"
)

func TestIssueRegistrationAndReadback(t *testing.T) {
	_, path := history(t)
	db, err := store.OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	before, err := (&service.Service{Store: db}).Show("plan-test")
	if err != nil {
		t.Fatal(err)
	}
	session, ctx := connect(t, db)
	for _, args := range []map[string]any{
		{"project": "demo", "number": 12},
		{"project": "demo", "number": 12, "url": nil},
		{"project": "demo", "number": 12, "url": "https://github.com/example/demo/issues/12", "session": "forbidden"},
	} {
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "issue_register", Arguments: args})
		if err == nil && !r.IsError {
			t.Fatalf("accepted invalid schema: %+v", args)
		}
	}
	url := "https://github.com/example/demo/issues/12"
	args := map[string]any{"project": "demo", "number": 12, "url": url, "remote_id": "I_existing"}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "issue_register", Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var registration struct {
		Data core.Issue `json:"data"`
	}
	if err := json.Unmarshal(encoded, &registration); err != nil {
		t.Fatal(err)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "issue_summary", Arguments: map[string]any{"project": "demo", "number": 12}})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	encoded, _ = json.Marshal(result.StructuredContent)
	var summary struct {
		Data service.IssueSummary `json:"data"`
	}
	if err := json.Unmarshal(encoded, &summary); err != nil {
		t.Fatal(err)
	}
	i := summary.Data.Issue
	if i != registration.Data || i.ID <= 0 || i.Repository != "github.com/example/demo" || i.URL != url || i.RemoteID != "I_existing" {
		t.Fatalf("%+v", i)
	}
	for _, tt := range []struct {
		name, project, url, code string
		number                   int
	}{
		{"duplicate", "demo", url, "issue_conflict", 12},
		{"unknown project", "missing", url, "not_found", 12},
		{"missing url", "demo", "", "invalid_input", 13},
		{"repository mismatch", "demo", "https://github.com/other/demo/issues/13", "invalid_input", 13},
		{"number mismatch", "demo", url, "invalid_input", 13},
		{"invalid number", "demo", url, "invalid_input", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "issue_register", Arguments: map[string]any{"project": tt.project, "number": tt.number, "url": tt.url}})
			b, _ := json.Marshal(r)
			if err != nil || !r.IsError || !strings.Contains(string(b), tt.code) {
				t.Fatalf("%s %v", b, err)
			}
		})
	}
	for _, table := range []string{"issues", "activity_segments", "activity_events", "issue_estimates", "issue_confirmations", "issue_outcomes"} {
		var n int
		if err := db.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		want := 0
		if table == "issues" {
			want = 1
		}
		if n != want {
			t.Fatalf("%s: %d", table, n)
		}
	}
	after, err := (&service.Service{Store: db}).Show("plan-test")
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if err != nil || string(a) != string(b) {
		t.Fatalf("approval history changed: %v", err)
	}
}
