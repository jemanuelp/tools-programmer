package mcpinstall

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"

	bundle "tools-programmer"
)

// SyncOpenCodeV2 publishes only the read-only V2 candidate. The caller must
// preflight the selected host before entering. Trusted, non-concurrently-edited
// directories are required; individual atomic writes are not a transaction.
func SyncOpenCodeV2(root, binary, database, runtimeID string) (IntegrationResult, error) {
	r := IntegrationResult{Target: "opencode-v2", Updated: []string{},
		Next: "Read-only candidate only. Dependencies, skills, MCP, permissions and databases were not installed. Verify the lock and prepare dependencies separately. Launch the selected V2 host with OPENCODE_CONFIG_DIR set to this root; data/cache/state and project config need separate isolation. Real host compatibility is unverified."}
	plan, err := PrepareOpenCodeV2Config(root, binary, database, runtimeID)
	if err != nil {
		return r, err
	}
	r.Destination, r.Plugin, r.Digest = plan.Destination, plan.Plugin, plan.Digest
	original, err := integrationRead(plan.Destination, true)
	if err != nil {
		return r, err
	}
	// Reconstruct from exactly the bytes retained for the compare-before-write.
	base := filepath.Join(plan.Root, "tools-programmer-integration-v2")
	content, _, err := openCodeV2Content(original, base, plan.Digest, binary, database, runtimeID)
	if err != nil || !bytes.Equal(content, []byte(plan.Config)) {
		if err != nil {
			return r, err
		}
		return r, integrationDrift()
	}
	assets, manifest, err := bundle.OpenCodeV2Assets()
	if err != nil {
		return r, err
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return r, err
	}
	assets["manifest.json"] = append(manifestBytes, '\n')
	target := filepath.Join(base, plan.Digest)
	names := make([]string, 0, len(assets))
	before := map[string][]byte{}
	for name, expected := range assets {
		actual, err := integrationRead(filepath.Join(target, filepath.FromSlash(name)), true)
		if err != nil {
			return r, err
		}
		if actual != nil && !bytes.Equal(actual, expected) {
			return r, integrationDrift()
		}
		before[name] = actual
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if before[name] != nil {
			continue
		}
		if err := replace(filepath.Join(target, filepath.FromSlash(name)), nil, assets[name]); err != nil {
			return r, err
		}
		r.Updated = append(r.Updated, name)
	}
	// Exact readback before config publication, including the manifest itself.
	for _, name := range names {
		actual, err := integrationRead(filepath.Join(target, filepath.FromSlash(name)), false)
		if err != nil {
			return r, err
		}
		if !bytes.Equal(actual, assets[name]) {
			return r, integrationDrift()
		}
	}
	if !bytes.Equal(original, content) {
		if err := replace(plan.Destination, original, content); err != nil {
			return r, err
		}
		r.ConfigChanged = true
	}
	actual, err := integrationRead(plan.Destination, false)
	if err != nil {
		return r, err
	}
	if !bytes.Equal(actual, content) {
		return r, integrationDrift()
	}
	return r, nil
}
