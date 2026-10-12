package mcpinstall

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path/filepath"

	bundle "tools-programmer"
	"tools-programmer/internal/core"
)

type OpenCodeV2ConfigPlan struct {
	Mode        string `json:"mode"`
	Root        string `json:"root"`
	Destination string `json:"destination"`
	Plugin      string `json:"plugin"`
	Digest      string `json:"bundle_digest"`
	// Existing configuration may contain secrets. Keep proposed bytes internal;
	// JSON summaries expose metadata only, not preserved operator values.
	Config        string `json:"-"`
	ConfigChanged bool   `json:"config_would_change"`
	Next          string `json:"next"`
}

// PrepareOpenCodeV2Config reads trusted, non-concurrently-edited local paths and
// proposes configuration only. No version probe or write occurs here; the future
// adapter must preflight the selected host first and revalidate before writes.
func PrepareOpenCodeV2Config(root, binary, database, runtimeID string) (OpenCodeV2ConfigPlan, error) {
	var plan OpenCodeV2ConfigPlan
	location, err := resolveOpenCodeV2Config(root)
	if err != nil {
		return plan, err
	}
	assets, manifest, err := bundle.OpenCodeV2Assets()
	if err != nil || bundle.VerifyOpenCodeV2Assets(assets, manifest) != nil {
		return plan, core.Fail("integration_unavailable", "embedded V2 bundle unavailable")
	}
	original, err := integrationRead(location.Destination, true)
	if err != nil {
		return plan, err
	}
	base := filepath.Join(location.Root, "tools-programmer-integration-v2")
	target := filepath.Join(base, manifest.Digest)
	content, old, err := openCodeV2Content(original, base, manifest.Digest, binary, database, runtimeID)
	if err != nil {
		return plan, err
	}
	if old != "" && old != target {
		return plan, core.Fail("config_manual", "previous V2 bundle requires explicit verification before upgrade; no configuration was written")
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	assets["manifest.json"] = append(manifestBytes, '\n')
	// A missing new bundle is expected for a proposal. An existing configured
	// bundle must be complete; never hide customized or unsafe owned destinations.
	for name, expected := range assets {
		actual, err := integrationRead(filepath.Join(target, filepath.FromSlash(name)), true)
		if err != nil {
			return plan, err
		}
		if (old != "" && actual == nil) || (actual != nil && !bytes.Equal(actual, expected)) {
			return plan, integrationDrift()
		}
	}
	plan = OpenCodeV2ConfigPlan{
		Mode: "plan_only", Root: location.Root, Destination: location.Destination,
		Plugin: (&url.URL{Scheme: "file", Path: filepath.Join(target, "opencode-v2", "plugin.ts")}).String(),
		Digest: manifest.Digest, Config: string(content), ConfigChanged: !bytes.Equal(original, content),
		Next: "Plan only: no files, dependencies, MCP entries or permission rules were installed. Host loading and permission enforcement remain unverified.",
	}
	return plan, nil
}
