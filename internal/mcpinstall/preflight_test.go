package mcpinstall

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tools-programmer/internal/core"
)

func TestComponentPreflightDoesNotPublish(t *testing.T) {
	for _, tc := range []struct {
		name, config, code string
		integration        bool
	}{
		{name: "empty MCP"},
		{name: "empty integration", integration: true},
		{name: "MCP conflict", config: `{"mcp":{"tools-programmer":{"command":["keep"]}}}`, code: "config_conflict"},
		{name: "integration conflict", config: `{"plugin":false}`, code: "config_invalid", integration: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "config")
			t.Setenv("OPENCODE_CONFIG_DIR", dir)
			path := filepath.Join(dir, "opencode.jsonc")
			if tc.config != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			database := filepath.Join(t.TempDir(), "absent.db")
			err := Preflight(database)
			if tc.integration {
				err = PreflightIntegration(database)
			}
			var typed *core.Error
			if tc.code == "" && err != nil {
				t.Fatal(err)
			}
			if tc.code != "" && (!errors.As(err, &typed) || typed.Code != tc.code) {
				t.Fatalf("expected %s, got %v", tc.code, err)
			}
			if tc.config == "" {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("preflight created config directory: %v", err)
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != tc.config {
					t.Fatalf("preflight changed active config: %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("preflight left artifacts: %v (%v)", entries, err)
				}
			}
			if _, err := os.Stat(database); !os.IsNotExist(err) {
				t.Fatalf("preflight created database: %v", err)
			}
		})
	}
}
