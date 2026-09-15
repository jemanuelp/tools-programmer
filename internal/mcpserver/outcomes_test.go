package mcpserver

import (
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/store"
)

func TestExplicitOutcomePreservesEvidence(t *testing.T) {
	_, path := history(t)
	db, err := store.OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.AddIssue("demo", 19, ""); err != nil {
		t.Fatal(err)
	}
	disabled, ctx := connect(t, db)
	r, err := disabled.CallTool(ctx, &mcp.CallToolParams{Name: "issue_outcome", Arguments: map[string]any{}})
	if err == nil && !r.IsError {
		t.Fatal("outcomes exposed without setup opt-in")
	}
	client, ctx := connect(t, db, Options{EnableIssueOutcomes: true})
	args := map[string]any{"project": "demo", "number": 19, "outcome": "completed", "actor": "implementing-agent", "rationale": "Requested implementation verified by focused tests; not delivered", "authorized": false}
	r, err = client.CallTool(ctx, &mcp.CallToolParams{Name: "issue_outcome", Arguments: args})
	if err != nil || !r.IsError {
		t.Fatal("unauthorized outcome accepted", r, err)
	}
	args["authorized"] = true
	before := time.Now().UTC().Add(-time.Second)
	for _, outcome := range []string{"completed", "completed", "reopened", "cancelled"} {
		args["outcome"] = outcome
		r, err = client.CallTool(ctx, &mcp.CallToolParams{Name: "issue_outcome", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		h, e := db.IssueHistory("demo", 19)
		if e != nil {
			t.Fatal(e)
		}
		if outcome == "completed" && len(h.Outcomes) == 1 && r.IsError {
			continue
		}
		if r.IsError {
			t.Fatal(r)
		}
	}
	h, err := db.IssueHistory("demo", 19)
	if err != nil || len(h.Outcomes) != 3 || h.Initial != nil || h.Final != nil {
		t.Fatal(h, err)
	}
	for _, o := range h.Outcomes {
		at, err := time.Parse(time.RFC3339Nano, o.RecordedAt)
		if err != nil || at.Before(before) || at.After(time.Now().UTC()) || o.Actor != "implementing-agent" || !strings.Contains(o.Rationale, "not delivered") {
			t.Fatal(o, err)
		}
	}
	args["recorded_at"] = "2000-01-01T00:00:00Z"
	r, err = client.CallTool(ctx, &mcp.CallToolParams{Name: "issue_outcome", Arguments: args})
	if err == nil && !r.IsError {
		t.Fatal("backdated field accepted")
	}
}
