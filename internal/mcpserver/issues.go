package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type issueRegistrationInput struct {
	Project  string `json:"project" jsonschema:"Registered project name"`
	Number   int    `json:"number" jsonschema:"Positive existing GitHub issue number"`
	URL      string `json:"url" jsonschema:"Canonical HTTPS URL matching the registered repository and issue number"`
	RemoteID string `json:"remote_id,omitempty" jsonschema:"Optional already observed GitHub stable ID"`
}

func addIssueRegistration(s *mcp.Server, db *store.Store) {
	no := false
	mcp.AddTool(s, &mcp.Tool{Name: "issue_register", Description: "Register an existing GitHub issue locally after explicit user authorization. Requires project, number and canonical url; remote_id is optional. Duplicate identity returns issue_conflict without changes. Never calls GitHub, verifies existence, assigns a session, confirms estimates or changes approval state.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in issueRegistrationInput) (*mcp.CallToolResult, output, error) {
			if err := ctx.Err(); err != nil {
				return nil, output{}, errors.New("request canceled")
			}
			if in.URL == "" {
				return nil, output{}, errors.New("invalid_input: url is required")
			}
			i, err := db.WithReadContext(ctx).AddIssueWithURL(in.Project, in.Number, in.RemoteID, in.URL)
			if err != nil {
				var typed *core.Error
				if errors.As(err, &typed) {
					return nil, output{}, errors.New(typed.Code + ": " + typed.Message)
				}
				return nil, output{}, errors.New("storage_error: cannot register local issue")
			}
			return nil, output{Data: i}, nil
		})
}
