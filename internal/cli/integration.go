package cli

import (
	bundle "tools-programmer"
	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpinstall"
)

func runIntegration(path, command string, args []string) (any, error) {
	if command == "integration info" && len(args) == 0 {
		_, manifest, err := bundle.IntegrationAssets()
		if err != nil {
			return nil, core.Fail("integration_unavailable", "embedded integration unavailable")
		}
		return struct {
			Protocol     int              `json:"protocol"`
			Capabilities []string         `json:"capabilities"`
			BundleDigest string           `json:"bundle_digest"`
			Binary       core.VersionInfo `json:"binary"`
		}{1, []string{"activity-context-v1", "existing-only-record", "explicit-issue-outcome"}, manifest.Digest, core.BuildVersion()}, nil
	}
	if command != "integration sync" || len(args) != 1 || args[0] != "opencode" {
		return nil, core.Fail("invalid_input", "expected: integration info | integration sync opencode")
	}
	database, err := selectedDatabase(path)
	if err != nil {
		return nil, err
	}
	return mcpinstall.SyncIntegration(database)
}
