package cli

import (
	"encoding/json"
	bundle "tools-programmer"
	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpinstall"
)

func runIntegration(path, command string, args []string) (any, error) {
	if command == "integration info" && len(args) == 1 && args[0] == "opencode-v2" {
		assets, manifest, err := bundle.OpenCodeV2Assets()
		if err != nil || bundle.VerifyOpenCodeV2Assets(assets, manifest) != nil {
			return nil, core.Fail("integration_unavailable", "embedded V2 candidate unavailable")
		}
		var adapter struct {
			Version      string            `json:"version"`
			Dependencies map[string]string `json:"dependencies"`
		}
		if err := json.Unmarshal(assets["opencode-v2/package.json"], &adapter); err != nil {
			return nil, core.Fail("integration_unavailable", "embedded V2 package unavailable")
		}
		return struct {
			Target         string                     `json:"target"`
			AdapterVersion string                     `json:"adapter_version"`
			CandidateHost  string                     `json:"candidate_host"`
			Dependencies   map[string]string          `json:"dependencies"`
			Manifest       bundle.IntegrationManifest `json:"manifest"`
			Binary         core.VersionInfo           `json:"binary"`
			Capabilities   []string                   `json:"capabilities"`
			HostVerified   bool                       `json:"host_verified"`
		}{"opencode-v2", adapter.Version, expectedOpenCodeV2Version, adapter.Dependencies, manifest, core.BuildVersion(), []string{"read-only-activity-context-v1"}, false}, nil
	}
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
		return nil, core.Fail("invalid_input", "expected: integration info [opencode-v2] | integration sync opencode")
	}
	database, err := selectedDatabase(path)
	if err != nil {
		return nil, err
	}
	return mcpinstall.SyncIntegration(database)
}
