package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type projectRegistrationInput struct {
	Name          string `json:"name" jsonschema:"Explicit project name"`
	Repo          string `json:"repo" jsonschema:"Repository as [HOST/]OWNER/REPO"`
	GitHubLogin   string `json:"github_login" jsonschema:"Explicit expected GitHub login"`
	GitName       string `json:"git_name" jsonschema:"Expected Git author and committer name"`
	GitEmail      string `json:"git_email" jsonschema:"Expected Git author and committer email"`
	Path          string `json:"path,omitempty" jsonschema:"Optional checkout path; relative to the server working directory"`
	AllowNoChecks bool   `json:"allow_no_checks,omitempty" jsonschema:"Explicitly allow no observed checks; defaults to false"`
}

func addProjectRegistration(s *mcp.Server, db *store.Store) {
	no := false
	mcp.AddTool(s, &mcp.Tool{Name: "project_register", Description: "Declare explicit project identities in the selected existing local database after user authorization. Not ownership, access or committing identity certification; run CLI project check separately. No inferred identities, Git, GitHub, assignments, backfill or approval actions. Duplicates are rejected without overwriting bindings.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in projectRegistrationInput) (*mcp.CallToolResult, output, error) {
			return registerProject(ctx, db, in)
		})
}

func registerProject(ctx context.Context, db *store.Store, in projectRegistrationInput) (*mcp.CallToolResult, output, error) {
	p, err := db.AddProjectContext(ctx, core.Project{Name: in.Name, Repo: in.Repo, ExpectedGitHub: in.GitHubLogin, GitName: in.GitName, GitEmail: in.GitEmail, Path: in.Path, AllowNoChecks: in.AllowNoChecks})
	if err != nil {
		if ctx.Err() != nil {
			return nil, output{}, errors.New("request canceled")
		}
		var typed *core.Error
		if errors.As(err, &typed) {
			return nil, output{}, errors.New(typed.Code + ": " + typed.Message)
		}
		return nil, output{}, errors.New("storage_error: cannot register local project")
	}
	return nil, output{Data: p}, nil
}
