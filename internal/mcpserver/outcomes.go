package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type issueOutcomeInput struct {
	Project    string `json:"project" jsonschema:"Registered project name"`
	Number     int    `json:"number" jsonschema:"Positive local issue number"`
	Outcome    string `json:"outcome" jsonschema:"completed, cancelled or reopened"`
	Actor      string `json:"actor" jsonschema:"Actual actor making this declaration; never invent a human"`
	Rationale  string `json:"rationale" jsonschema:"Explicit scope and actual evidence; keep delivery evidence distinct"`
	Authorized bool   `json:"authorized" jsonschema:"True only after separate explicit authorization for this outcome"`
}

func addIssueOutcome(server *mcp.Server, db *store.Store) {
	no := false
	mcp.AddTool(server, &mcp.Tool{Name: "issue_outcome",
		Description: "Record an explicitly authorized local outcome with actual actor and evidence. Completed describes agreed implementation and verification, not delivery or merge. No estimate prerequisite. Never infer from idle/tests/PR alone; a child cannot independently complete its parent's issue. On uncertainty read issue_summary before further writes. This authorization declaration is not proof of human presence.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &no}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in issueOutcomeInput) (*mcp.CallToolResult, output, error) {
			if !in.Authorized {
				return nil, output{}, errors.New("authorization_required: obtain separate explicit outcome authorization")
			}
			view := db.WithReadContext(ctx)
			history, err := view.IssueHistory(in.Project, in.Number)
			if err == nil {
				err = view.RecordIssueOutcome(history.Issue.ID, in.Outcome, in.Actor, in.Rationale)
			}
			if err != nil {
				var typed *core.Error
				if errors.As(err, &typed) {
					return nil, output{}, errors.New(typed.Code + ": " + typed.Message)
				}
				return nil, output{}, errors.New("storage_error: outcome uncertain; read issue summary before further writes")
			}
			return nil, output{Data: map[string]bool{"recorded": true}}, nil
		})
}
