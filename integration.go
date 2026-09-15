package toolsprogrammer

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
)

// IntegrationFiles is separate from the two-file skill bundle. Only owned runtime
// assets and declarations are embedded; tests and package installations are not.
//
//go:embed integrations/opencode/plugin.ts integrations/opencode/observer.mjs integrations/opencode/observer.d.mts integrations/opencode/transport.mjs integrations/opencode/transport.d.mts
//go:embed integrations/opencode/parent-snapshot.mjs integrations/opencode/parent-snapshot.d.mts integrations/opencode/session-snapshot.mjs integrations/opencode/session-snapshot.d.mts integrations/opencode/runtime-read.mjs
//go:embed integrations/opencode/context.mjs integrations/opencode/context.d.mts integrations/opencode/advisory.mjs integrations/opencode/advisory.d.mts integrations/opencode/issue-tracking.md integrations/opencode/diagnostics.mjs integrations/opencode/diagnostics.d.mts
var IntegrationFiles embed.FS

type IntegrationManifest struct {
	Protocol int               `json:"protocol"`
	Digest   string            `json:"digest"`
	Files    map[string]string `json:"files"`
}

func IntegrationAssets() (map[string][]byte, IntegrationManifest, error) {
	assets := map[string][]byte{}
	m := IntegrationManifest{Protocol: 1, Files: map[string]string{}}
	const prefix = "integrations/opencode/"
	entries, err := IntegrationFiles.ReadDir("integrations/opencode")
	if err != nil {
		return nil, m, err
	}
	for _, entry := range entries {
		b, err := IntegrationFiles.ReadFile(prefix + entry.Name())
		if err != nil {
			return nil, m, err
		}
		assets[entry.Name()] = b
		h := sha256.Sum256(b)
		m.Files[entry.Name()] = hex.EncodeToString(h[:])
	}
	// encoding/json sorts string keys, making the inventory digest reproducible.
	b, err := json.Marshal(m.Files)
	if err != nil {
		return nil, m, err
	}
	h := sha256.Sum256(b)
	m.Digest = hex.EncodeToString(h[:])
	return assets, m, nil
}
