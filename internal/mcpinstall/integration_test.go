package mcpinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bundle "tools-programmer"
)

func TestIntegrationSyncInventoryIdempotenceAndJSONC(t *testing.T) {
	for _, config := range []string{"", "{\n// keep header\n\"model\":\"fixture/model\",\"plugin\":[\"other-plugin\", /* keep */],\"permission\":{\"bash\":{\"*\":\"ask\",\"git *\":\"deny\"}},\"number\":9007199254740993,\n}\n"} {
		t.Run(map[bool]string{true: "new-parent", false: "jsonc"}[config == ""], func(t *testing.T) {
			home, path := isolatedHome(t)
			if config != "" {
				write(t, path, config)
			}
			db := filepath.Join(home, "missing-state", "history.db")
			assets, manifest, err := bundle.IntegrationAssets()
			if err != nil {
				t.Fatal(err)
			}
			r, err := syncIntegration(db, "/fixture/binary", assets, manifest)
			if err != nil || !r.ConfigChanged || len(r.Updated) != len(assets)+1 {
				t.Fatal(r, err)
			}
			u, _ := url.Parse(r.Plugin)
			root := filepath.Dir(u.Path)
			if err := verifyIntegration(root); err != nil {
				t.Fatal(err)
			}
			for name, want := range assets {
				got, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || !bytes.Equal(got, want) {
					t.Fatal(name, err)
				}
			}
			before, _ := os.ReadFile(path)
			if config != "" {
				for _, keep := range []string{"// keep header", "/* keep */", `"other-plugin"`, `"permission":{"bash":{"*":"ask","git *":"deny"}}`, "9007199254740993"} {
					if !bytes.Contains(before, []byte(keep)) {
						t.Fatal("lost custom bytes", keep)
					}
				}
			}
			write(t, filepath.Join(root, "user-notes.txt"), "preserve")
			stat, _ := os.Stat(path)
			again, err := syncIntegration(db, "/fixture/binary", assets, manifest)
			if err != nil || again.ConfigChanged || len(again.Updated) != 0 {
				t.Fatal(again, err)
			}
			after, _ := os.ReadFile(path)
			now, _ := os.Stat(path)
			if !bytes.Equal(before, after) || !stat.ModTime().Equal(now.ModTime()) {
				t.Fatal("non-idempotent config write")
			}
			if b, err := os.ReadFile(filepath.Join(root, "user-notes.txt")); err != nil || string(b) != "preserve" {
				t.Fatal("unknown file changed")
			}
			for _, absent := range []string{filepath.Dir(db), filepath.Join(home, ".config", "opencode", "AGENTS.md"), filepath.Join(home, ".config", "opencode", "skills"), filepath.Join(home, ".config", "opencode", "node_modules")} {
				if _, err := os.Stat(absent); !os.IsNotExist(err) {
					t.Fatal("unexpected installation scope", absent, err)
				}
			}
		})
	}
}

func changedBundle(t *testing.T) (map[string][]byte, bundle.IntegrationManifest) {
	t.Helper()
	a, m, err := bundle.IntegrationAssets()
	if err != nil {
		t.Fatal(err)
	}
	a["issue-tracking.md"] = append(a["issue-tracking.md"], []byte("\nFixture upgrade.\n")...)
	h := sha256.Sum256(a["issue-tracking.md"])
	m.Files["issue-tracking.md"] = hex.EncodeToString(h[:])
	b, _ := json.Marshal(m.Files)
	h = sha256.Sum256(b)
	m.Digest = hex.EncodeToString(h[:])
	return a, m
}

func TestIntegrationUpgradePreservesPolicyAndRefusesDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "customized-owned-asset"}[drift], func(t *testing.T) {
			home, path := isolatedHome(t)
			db := filepath.Join(home, "db")
			a, m, _ := bundle.IntegrationAssets()
			r, err := syncIntegration(db, "/fixture/binary", a, m)
			if err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(path)
			policy := `"assignmentPolicy": {"directImplementation": true, "projects": ["demo"]}, /* preserve policy */ `
			original = bytes.Replace(original, []byte(`"binary":`), []byte(policy+`"binary":`), 1)
			write(t, path, string(original))
			u, _ := url.Parse(r.Plugin)
			old := filepath.Dir(u.Path)
			if drift {
				write(t, filepath.Join(old, "issue-tracking.md"), "private custom guidance")
			}
			next, manifest := changedBundle(t)
			updated, err := syncIntegration(db, "/fixture/binary", next, manifest)
			current, _ := os.ReadFile(path)
			if drift {
				if err == nil || updated.ConfigChanged || !bytes.Equal(original, current) || strings.Contains(err.Error(), "private custom guidance") {
					t.Fatal(updated, err)
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(old), manifest.Digest)); !os.IsNotExist(err) {
					t.Fatal("created bundle despite drift")
				}
			} else {
				if err != nil || !updated.ConfigChanged || !bytes.Contains(current, []byte(policy)) {
					t.Fatal(updated, err)
				}
				if err := verifyIntegration(old); err != nil {
					t.Fatal("old bundle removed", err)
				}
			}
		})
	}
}

func TestIntegrationRefusesUnsafeConfigAndPaths(t *testing.T) {
	for _, cfg := range []string{`{"plugin":null}`, `{"plugin":[],"plugin":[]}`, `{"plugin":[["x"]]}`, `{"plugin":[["x",null]]}`, `{"plugin":[null]}`, `{"plugin":[{}]}`} {
		t.Run(cfg, func(t *testing.T) {
			home, path := isolatedHome(t)
			write(t, path, cfg)
			if _, err := SyncIntegration(filepath.Join(home, "db")); err == nil {
				t.Fatal("unsafe config accepted")
			}
			if b, _ := os.ReadFile(path); string(b) != cfg {
				t.Fatal("config changed")
			}
		})
	}
	for _, component := range []string{"config", "bundle-parent", "owned-file"} {
		t.Run(component, func(t *testing.T) {
			home, path := isolatedHome(t)
			a, m, _ := bundle.IntegrationAssets()
			link := path
			if component == "bundle-parent" {
				link = filepath.Join(filepath.Dir(path), "tools-programmer-integration")
			}
			if component == "owned-file" {
				link = filepath.Join(filepath.Dir(path), "tools-programmer-integration", m.Digest, "plugin.ts")
			}
			if err := os.MkdirAll(filepath.Dir(link), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(home, "absent"), link); err != nil {
				t.Fatal(err)
			}
			if _, err := syncIntegration(filepath.Join(home, "db"), "/fixture/binary", a, m); err == nil {
				t.Fatal("symlink accepted")
			}
		})
	}
}

func TestIntegrationConfigSelectionAndChangedOptions(t *testing.T) {
	for _, name := range []string{"opencode.jsonc", "opencode.json", "config.json"} {
		t.Run(name, func(t *testing.T) {
			home, defaultPath := isolatedHome(t)
			path := filepath.Join(filepath.Dir(defaultPath), name)
			write(t, path, "{}")
			if name == "opencode.jsonc" {
				write(t, filepath.Join(filepath.Dir(path), "opencode.json"), "{\"model\":\"preserve\"}")
			}
			a, m, _ := bundle.IntegrationAssets()
			r, err := syncIntegration(filepath.Join(home, "db"), "/fixture/binary", a, m)
			if name == "config.json" {
				if err == nil {
					t.Fatal("legacy config accepted")
				}
				if b, _ := os.ReadFile(path); string(b) != "{}" {
					t.Fatal("legacy modified")
				}
				return
			}
			if err != nil || r.Destination != path {
				t.Fatal(r, err)
			}
			before, _ := os.ReadFile(path)
			_, err = syncIntegration(filepath.Join(home, "another-db"), "/fixture/binary", a, m)
			if err == nil {
				t.Fatal("changed database options overwritten")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("custom options changed")
			}
		})
	}
}
