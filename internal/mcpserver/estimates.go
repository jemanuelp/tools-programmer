package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type issueEstimateProposalInput struct {
	Project   string `json:"project" jsonschema:"Registered project name"`
	Number    int    `json:"number" jsonschema:"Positive registered issue number"`
	Kind      string `json:"kind" jsonschema:"Estimate kind: initial, revision, or final"`
	Score     int    `json:"score" jsonschema:"Fibonacci score: 1, 2, 3, 5, 8, 13, or 21"`
	Proposer  string `json:"proposer" jsonschema:"Agent or person proposing the estimate"`
	Rationale string `json:"rationale" jsonschema:"Reason for the relative estimate"`
}

func addIssueEstimateProposal(server *mcp.Server, db *store.Store) {
	no := false
	mcp.AddTool(server, &mcp.Tool{Name: "issue_estimate_propose",
		Description: "Append an explicitly authorized local issue estimate proposal. Requires a registered project and issue. Uses Fibonacci 1, 2, 3, 5, 8, 13, or 21; never confirms an estimate, calls GitHub, or overwrites prior proposals. Human confirmation remains a separate explicit action.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: false, OpenWorldHint: &no}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in issueEstimateProposalInput) (*mcp.CallToolResult, output, error) {
			if err := ctx.Err(); err != nil {
				return nil, output{}, errors.New("request canceled")
			}
			view := db.WithReadContext(ctx)
			history, err := view.IssueHistory(in.Project, in.Number)
			if err == nil {
				proposal, proposalErr := view.ProposeEstimate(history.Issue.ID, in.Kind, in.Score, in.Proposer, in.Rationale)
				if proposalErr == nil {
					return nil, output{Data: proposal}, nil
				}
				err = proposalErr
			}
			if err != nil {
				var typed *core.Error
				if errors.As(err, &typed) {
					return nil, output{}, errors.New(typed.Code + ": " + typed.Message)
				}
				return nil, output{}, errors.New("storage_error: cannot record estimate proposal")
			}
			return nil, output{}, errors.New("storage_error: cannot record estimate proposal")
		})
}
