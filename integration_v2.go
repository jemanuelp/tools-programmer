package toolsprogrammer

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Keep the V2 read-only dependency closure separate from the V1 bundle. Explicit
// paths prevent test fixtures, assignments and native prototypes from shipping.
// The sibling directories preserve source imports without rewriting V1 assets.
//
//go:embed integrations/opencode-v2/plugin.ts integrations/opencode-v2/runtime.mjs integrations/opencode-v2/runtime.d.mts
//go:embed integrations/opencode-v2/package.json integrations/opencode-v2/package-lock.json
//go:embed integrations/opencode/context.mjs integrations/opencode/context.d.mts
//go:embed integrations/opencode/diagnostics.d.mts
var openCodeV2Files embed.FS

var openCodeV2AssetNames = [...]string{
	"opencode-v2/package.json",
	"opencode-v2/package-lock.json",
	"opencode-v2/plugin.ts",
	"opencode-v2/runtime.mjs",
	"opencode-v2/runtime.d.mts",
	"opencode/context.mjs",
	"opencode/context.d.mts",
	"opencode/diagnostics.d.mts",
}

// OpenCodeV2Assets authors a portable source bundle only. It does not publish
// files, install dependencies, load a host or authorize the excluded mutators.
// Protocol 2 intentionally cannot pass the existing V1 bundle verifier.
func OpenCodeV2Assets() (map[string][]byte, IntegrationManifest, error) {
	assets := map[string][]byte{}
	manifest := IntegrationManifest{Protocol: 2, Files: map[string]string{}}
	for _, name := range openCodeV2AssetNames {
		content, err := openCodeV2Files.ReadFile("integrations/" + name)
		if err != nil {
			return nil, manifest, err
		}
		assets[name] = content
		manifest.Files[name] = openCodeV2Hash(content)
	}
	inventory, err := json.Marshal(manifest.Files)
	if err != nil {
		return nil, manifest, err
	}
	manifest.Digest = openCodeV2Hash(inventory)
	return assets, manifest, nil
}

// VerifyOpenCodeV2Assets checks an exact inventory and its content hashes. This
// is corruption detection, not authenticity, dependency resolution or native
// host compatibility: a caller must separately trust the manifest provenance.
func VerifyOpenCodeV2Assets(assets map[string][]byte, manifest IntegrationManifest) error {
	invalid := errors.New("invalid OpenCode V2 read-only bundle")
	if manifest.Protocol != 2 || len(assets) != len(openCodeV2AssetNames) || len(manifest.Files) != len(openCodeV2AssetNames) {
		return invalid
	}
	for _, name := range openCodeV2AssetNames {
		content, ok := assets[name]
		if !ok || len(content) == 0 || len(content) > 1<<20 || manifest.Files[name] != openCodeV2Hash(content) {
			return invalid
		}
	}
	inventory, err := json.Marshal(manifest.Files)
	if err != nil || manifest.Digest != openCodeV2Hash(inventory) {
		return invalid
	}
	return nil
}

func openCodeV2Hash(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
