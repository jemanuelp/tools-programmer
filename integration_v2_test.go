package toolsprogrammer_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	bundle "tools-programmer"
)

func TestOpenCodeV2BundleInventory(t *testing.T) {
	assets, manifest, err := bundle.OpenCodeV2Assets()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"opencode-v2/package-lock.json", "opencode-v2/package.json", "opencode-v2/plugin.ts", "opencode-v2/runtime.d.mts", "opencode-v2/runtime.mjs", "opencode/context.d.mts", "opencode/context.mjs", "opencode/diagnostics.d.mts"}
	var names []string
	for name, content := range assets {
		names = append(names, name)
		digest := sha256.Sum256(content)
		if manifest.Files[name] != hex.EncodeToString(digest[:]) {
			t.Fatalf("incorrect hash for %s", name)
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, want) || manifest.Protocol != 2 {
		t.Fatal(names, manifest)
	}
	t.Logf("V2 source bundle: protocol=%d files=%d digest=%s", manifest.Protocol, len(names), manifest.Digest)
	inventory, err := json.Marshal(manifest.Files)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(inventory)
	if manifest.Digest != hex.EncodeToString(digest[:]) {
		t.Fatal("inventory digest mismatch")
	}
	imports := regexp.MustCompile(`(?:from |import\()["'](\.[^"']+)["']`)
	for name, content := range assets {
		for _, match := range imports.FindAllSubmatch(content, -1) {
			dependency := path.Clean(path.Join(path.Dir(name), string(match[1])))
			if _, ok := assets[dependency]; !ok {
				// Declaration-only references do not require a runtime module.
				declaration := strings.TrimSuffix(dependency, ".mjs") + ".d.mts"
				if _, exists := assets[declaration]; !exists || !strings.HasSuffix(name, ".d.mts") {
					t.Fatalf("%s imports missing %s", name, dependency)
				}
			}
		}
	}
	var production struct {
		Name            string            `json:"name"`
		Version         string            `json:"version"`
		Main            string            `json:"main"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(assets["opencode-v2/package.json"], &production); err != nil {
		t.Fatal(err)
	}
	if production.Version != "0.1.0-candidate.2.0.26" || production.Main != "./plugin.ts" ||
		!reflect.DeepEqual(production.Dependencies, map[string]string{"@opencode/plugin": "2.0.26", "@opencode/client": "2.0.26"}) {
		t.Fatal("unexpected production dependencies", production)
	}
	var lock struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Packages map[string]struct {
			Name            string            `json:"name"`
			Version         string            `json:"version"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
			Resolved        string            `json:"resolved"`
			Integrity       string            `json:"integrity"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(assets["opencode-v2/package-lock.json"], &lock); err != nil {
		t.Fatal(err)
	}
	root := lock.Packages[""]
	if lock.Name != production.Name || lock.Version != production.Version || root.Name != production.Name || root.Version != production.Version || !reflect.DeepEqual(root.Dependencies, production.Dependencies) || !reflect.DeepEqual(root.DevDependencies, production.DevDependencies) {
		t.Fatal("manifest and lock identities diverge")
	}
	for name, entry := range lock.Packages {
		if name == "node_modules/@opencode/cli" || strings.HasPrefix(name, "node_modules/@opencode/cli-") || (entry.Resolved != "" && (!strings.HasPrefix(entry.Resolved, "https://registry.npmjs.org/") || entry.Integrity == "")) {
			t.Fatal("unsafe lock graph", name)
		}
		if strings.HasPrefix(name, "node_modules/@opencode/") && entry.Version != "2.0.26" {
			t.Fatal("unaligned OpenCode dependency", name, entry.Version)
		}
	}
	again, repeated, err := bundle.OpenCodeV2Assets()
	if err != nil || !reflect.DeepEqual(assets, again) || !reflect.DeepEqual(manifest, repeated) {
		t.Fatal("bundle is not deterministic", err)
	}
	assets["opencode-v2/plugin.ts"][0] ^= 1
	if reflect.DeepEqual(assets, again) {
		t.Fatal("bundle shares mutable asset storage")
	}
	_, v1, err := bundle.IntegrationAssets()
	if err != nil || v1.Protocol != 1 || v1.Digest != "503a08fc6aaa3541cce42c17b2932f5df695469dc12d33aae172cc563e8b4c42" {
		t.Fatal("V1 bundle changed", v1, err)
	}
}

func TestOpenCodeV2BundleValidation(t *testing.T) {
	cases := []struct {
		name   string
		change func(map[string][]byte, *bundle.IntegrationManifest)
	}{
		{"valid", nil},
		{"wrong protocol", func(_ map[string][]byte, m *bundle.IntegrationManifest) { m.Protocol = 1 }},
		{"wrong inventory digest", func(_ map[string][]byte, m *bundle.IntegrationManifest) { m.Digest = "invalid" }},
		{"missing asset", func(a map[string][]byte, _ *bundle.IntegrationManifest) { delete(a, "opencode/context.mjs") }},
		{"modified asset", func(a map[string][]byte, _ *bundle.IntegrationManifest) {
			a["opencode-v2/plugin.ts"] = []byte("modified")
		}},
		{"modified file hash", func(_ map[string][]byte, m *bundle.IntegrationManifest) { m.Files["opencode-v2/plugin.ts"] = "invalid" }},
		{"extra asset", func(a map[string][]byte, _ *bundle.IntegrationManifest) {
			a["opencode-v2/assignment.mjs"] = []byte("not shipped")
		}},
		{"extra manifest entry", func(_ map[string][]byte, m *bundle.IntegrationManifest) { m.Files["../outside"] = "invalid" }},
		{"empty asset with matching hash", func(a map[string][]byte, m *bundle.IntegrationManifest) {
			a["opencode-v2/plugin.ts"] = nil
			digest := sha256.Sum256(nil)
			m.Files["opencode-v2/plugin.ts"] = hex.EncodeToString(digest[:])
			inventory, _ := json.Marshal(m.Files)
			digest = sha256.Sum256(inventory)
			m.Digest = hex.EncodeToString(digest[:])
		}},
		{"oversized asset with matching hash", func(a map[string][]byte, m *bundle.IntegrationManifest) {
			a["opencode-v2/plugin.ts"] = make([]byte, (1<<20)+1)
			digest := sha256.Sum256(a["opencode-v2/plugin.ts"])
			m.Files["opencode-v2/plugin.ts"] = hex.EncodeToString(digest[:])
			inventory, _ := json.Marshal(m.Files)
			digest = sha256.Sum256(inventory)
			m.Digest = hex.EncodeToString(digest[:])
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assets, manifest, err := bundle.OpenCodeV2Assets()
			if err != nil {
				t.Fatal(err)
			}
			if tt.change != nil {
				tt.change(assets, &manifest)
			}
			err = bundle.VerifyOpenCodeV2Assets(assets, manifest)
			if (err != nil) != (tt.change != nil) {
				t.Fatal("unexpected validation result", err)
			}
		})
	}
}

func TestOpenCodeV2BundleRelocatedReadOnlyRuntime(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node required for relocated fake-CLI verification")
	}
	assets, manifest, err := bundle.OpenCodeV2Assets()
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.VerifyOpenCodeV2Assets(assets, manifest); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, content := range assets {
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// No native binary, database or OpenCode host: this fixture checks only exact
	// read-only CLI arguments and returns a synthetic unavailable/unknown result.
	fake := `#!/bin/sh
set -eu
test "$#" = 10
test "$1" = --db && test "$2" = /synthetic/missing.db
test "$3" = activity && test "$4" = context
test "$5" = --runtime && test "$6" = opencode-v2:relocated
test "$7" = --session && test "$8" = native-session
test "$9" = --directory && test "${10}" = /synthetic/session-checkout
`
	fake += `printf '%s\n' '{"data":{"version":1,"runtime":"opencode-v2:relocated","session":"native-session","observed_at":"2026-10-07T00:00:00Z","project_resolution":"unavailable","assignment":"unknown","warnings":["session_unobserved"]}}'
`
	if err := os.WriteFile(filepath.Join(root, "fake-cli"), []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	probe := `import assert from "node:assert/strict";
import { createContextReader } from "./opencode-v2/runtime.mjs";
const read = createContextReader({binary: process.argv[2], database: "/synthetic/missing.db", runtimeId: "opencode-v2:relocated"});
let calls = 0;
const session = {get: async (input, options) => {
  calls++;
  assert.deepEqual(input, {sessionID: "native-session"});
  assert.ok(options.signal instanceof AbortSignal);
  return {id: "native-session", projectID: "synthetic", location: {directory: "/synthetic/session-checkout"}};
}};
const result = await read(session, "native-session", new AbortController().signal);
assert.equal(result.runtime, "opencode-v2:relocated");
assert.equal(result.session, "native-session");
assert.equal(result.assignment, "unknown");
assert.deepEqual(result.warnings, ["session_unobserved"]);
const cancelled = AbortSignal.abort();
assert.equal((await read(session, "native-session", cancelled)).assignment, "unknown");
assert.equal(calls, 1);
`
	if err := os.WriteFile(filepath.Join(root, "probe.mjs"), []byte(probe), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, node, "probe.mjs", filepath.Join(root, "fake-cli"))
	command.Dir = root
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root}
	command.WaitDelay = 100 * time.Millisecond
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("relocated fake-CLI probe: %v\n%s", err, output)
	}
}
