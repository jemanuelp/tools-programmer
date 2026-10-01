package mcpserver

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestIdentityUpdateMCPConsentRefusalNativeContinuationAndPreservation(t *testing.T) {
	db, path := history(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := db.Projects()
	if err != nil {
		t.Fatal(err)
	}
	// Even a matching server identity is not committing-environment evidence.
	for key, value := range map[string]string{"GIT_AUTHOR_NAME": "New", "GIT_AUTHOR_EMAIL": "new@example.com", "GIT_COMMITTER_NAME": "New", "GIT_COMMITTER_EMAIL": "new@example.com"} {
		t.Setenv(key, value)
	}
	// Any accidental Git invocation must fail; the expected continuation is not git_io.
	t.Setenv("PATH", t.TempDir())
	session, ctx := connect(t, db)
	for _, scenario := range []string{"no_consent", "valid_declared_consent", "same_tuple", "invalid_tuple", "relative_path", "missing_project"} {
		t.Run(scenario, func(t *testing.T) {
			args := map[string]any{"project": "demo", "git_name": "New", "git_email": "new@example.com", "path": "/private/committing-checkout", "authorized": true}
			want := "native_validation_required"
			switch scenario {
			case "no_consent":
				args["authorized"] = false
				want = "authorization_required"
			case "invalid_tuple":
				args["git_email"] = "invalid"
				want = "identity_unbound"
			case "relative_path":
				args["path"] = "relative"
				want = "invalid_input"
			case "missing_project":
				args["project"] = "missing"
				want = "not_found"
			case "same_tuple":
				args["git_name"] = "Reviewer"
				args["git_email"] = "reviewer@example.com"
			}
			got, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "project_update_git_identity", Arguments: args})
			if err != nil || !got.IsError {
				t.Fatal(got, err)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), want) || strings.Contains(string(b), "/private/committing-checkout") || strings.Contains(string(b), "new@example.com") {
				t.Fatal("incorrect refusal or private diagnostic", string(b))
			}
			if scenario == "valid_declared_consent" && (!strings.Contains(string(b), "project update-git-identity") || !strings.Contains(string(b), "--authorized") || !strings.Contains(string(b), "human consent")) {
				t.Fatal("missing safe continuation", string(b))
			}
		})
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("MCP changed state", err)
	}
	current, err := db.Projects()
	if err != nil || !reflect.DeepEqual(current, projects) {
		t.Fatal("MCP changed policy", current, err)
	}
}
