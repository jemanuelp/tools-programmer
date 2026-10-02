package mcpserver

import (
	"context"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type gitIdentityUpdateInput struct {
	Project    string `json:"project" jsonschema:"Exact existing registered project"`
	GitName    string `json:"git_name" jsonschema:"Explicit proposed expected Git name"`
	GitEmail   string `json:"git_email" jsonschema:"Explicit proposed expected Git email"`
	Path       string `json:"path" jsonschema:"Absolute intended committing checkout; not the MCP server checkout"`
	Authorized bool   `json:"authorized" jsonschema:"Authorization declaration only; not proof of human consent"`
}

func addGitIdentityContinuation(s *mcp.Server, db *store.Store) {
	add(s, "project_update_git_identity", "Validate explicit request fields and refuse mutation with native CLI continuation. Never inspect Git or certify the user's committing environment from server configuration. Authorization declaration is not proof of human consent; ordinary CLI project check remains required.", nil, func(ctx context.Context, in gitIdentityUpdateInput) (any, error) {
		if !in.Authorized {
			return nil, core.Fail("authorization_required", "explicit user consent for the exact project and proposed tuple is required; an MCP declaration is not proof of human consent")
		}
		if !core.ValidName(in.Project) || !filepath.IsAbs(in.Path) {
			return nil, core.Fail("invalid_input", "exact registered project and absolute committing checkout are required")
		}
		p, err := db.WithReadContext(ctx).Project(in.Project)
		if err != nil {
			return nil, err
		}
		if err := p.ValidateIdentity(); err != nil {
			return nil, err
		}
		p.GitName, p.GitEmail = in.GitName, in.GitEmail
		if err := p.ValidateIdentity(); err != nil {
			return nil, err
		}
		return nil, core.Fail("native_validation_required", "no mutation: MCP server Git configuration cannot prove the actual committing environment or human consent. After explicit user authorization, run tools-programmer [--db PATH] project update-git-identity --project NAME --git-name NAME --git-email EMAIL --path ABSOLUTE_CHECKOUT --authorized in that native environment. Checkout rebind is separate; run project check before committing. Replace placeholders with the approved request values")
	})
}
