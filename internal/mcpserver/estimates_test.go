package mcpserver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func TestExplicitEstimateProposalAppendsHistory(t *testing.T) {
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
	r, err := disabled.CallTool(ctx, &mcp.CallToolParams{Name: "issue_estimate_propose", Arguments: map[string]any{}})
	if err == nil && !r.IsError {
		t.Fatal("estimate proposals exposed without setup opt-in")
	}

	client, ctx := connect(t, db, Options{EnableIssueEstimates: true})
	before := time.Now().UTC().Add(-time.Second)
	for _, args := range []map[string]any{
		{"project": "demo", "number": 19, "kind": "initial", "score": 3, "proposer": "planning-agent", "rationale": "Known scope and one integration point"},
		{"project": "demo", "number": 19, "kind": "revision", "score": 5, "proposer": "planning-agent", "rationale": "Observed integration complexity"},
		{"project": "demo", "number": 19, "kind": "final", "score": 5, "proposer": "planning-agent", "rationale": "Retrospective relative assessment"},
	} {
		r, err = client.CallTool(ctx, &mcp.CallToolParams{Name: "issue_estimate_propose", Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%+v %v", r, err)
		}
		encoded, _ := json.Marshal(r.StructuredContent)
		var result struct {
			Data core.Estimate `json:"data"`
		}
		if err := json.Unmarshal(encoded, &result); err != nil || result.Data.ID <= 0 || result.Data.IssueID <= 0 {
			t.Fatalf("%+v %v", result, err)
		}
	}

	history, err := db.IssueHistory("demo", 19)
	if err != nil || len(history.Estimates) != 3 || history.Initial != nil || history.Final != nil {
		t.Fatalf("%+v %v", history, err)
	}
	for _, estimate := range history.Estimates {
		at, err := time.Parse(time.RFC3339Nano, estimate.ProposedAt)
		if err != nil || estimate.ID <= 0 || estimate.Proposer != "planning-agent" || at.Before(before) || at.After(time.Now().UTC()) {
			t.Fatalf("%+v %v", estimate, err)
		}
	}

	for _, args := range []map[string]any{
		{"project": "missing", "number": 19, "kind": "initial", "score": 3, "proposer": "planning-agent", "rationale": "Known scope"},
		{"project": "demo", "number": 20, "kind": "initial", "score": 3, "proposer": "planning-agent", "rationale": "Known scope"},
		{"project": "demo", "number": 19, "kind": "invalid", "score": 3, "proposer": "planning-agent", "rationale": "Known scope"},
		{"project": "demo", "number": 19, "kind": "initial", "score": 4, "proposer": "planning-agent", "rationale": "Known scope"},
		{"project": "demo", "number": 19, "kind": "initial", "score": 3, "proposer": "", "rationale": "Known scope"},
		{"project": "demo", "number": 19, "kind": "initial", "score": 3, "proposer": "planning-agent", "rationale": "", "unexpected": true},
	} {
		r, err = client.CallTool(ctx, &mcp.CallToolParams{Name: "issue_estimate_propose", Arguments: args})
		if err != nil || !r.IsError {
			t.Fatalf("accepted invalid input: %+v %v", r, err)
		}
	}

	history, err = db.IssueHistory("demo", 19)
	if err != nil || len(history.Estimates) != 3 {
		t.Fatalf("invalid inputs changed history: %+v %v", history, err)
	}
}
