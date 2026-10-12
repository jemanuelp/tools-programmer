package mcpinstall

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	bundle "tools-programmer"
)

func TestOpenCodeV2PlanNeverWritesAndRefusesOwnedDrift(t *testing.T) {
	home, v1 := isolatedHome(t)
	write(t, v1, `{"plugin":["keep-v1"]}`)
	root := filepath.Join(home, ".config", "opencode-v2")
	database := filepath.Join(home, "missing-state", "history.db")
	plan, err := PrepareOpenCodeV2Config(root, "/fixture/binary", database, "opencode-v2:realm")
	if err != nil || plan.Mode != "plan_only" || plan.Root != root || plan.Config == "" {
		t.Fatal(plan, err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil || bytes.Contains(encoded, []byte("proposed_config")) || bytes.Contains(encoded, []byte("$schema")) {
		t.Fatal("serialized plan exposed configuration", err)
	}
	for _, absent := range []string{root, filepath.Dir(database)} {
		if _, err := os.Lstat(absent); !os.IsNotExist(err) {
			t.Fatal("plan created a destination", absent, err)
		}
	}
	if before, err := os.ReadFile(v1); err != nil || string(before) != `{"plugin":["keep-v1"]}` {
		t.Fatal("V1 changed", err)
	}
	assets, manifest, err := bundle.OpenCodeV2Assets()
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	assets["manifest.json"] = append(manifestBytes, '\n')
	target := filepath.Join(root, "tools-programmer-integration-v2", manifest.Digest)
	for name, content := range assets {
		write(t, filepath.Join(target, name), string(content))
	}
	write(t, plan.Destination, plan.Config)
	before, _ := os.ReadFile(plan.Destination)
	again, err := PrepareOpenCodeV2Config(root, "/fixture/binary", database, "opencode-v2:realm")
	if err != nil || again.ConfigChanged || again.Config != plan.Config {
		t.Fatal("complete fixture was not idempotent", err)
	}
	owned := filepath.Join(target, "opencode-v2", "runtime.mjs")
	write(t, owned, "customized owned bytes")
	if _, err := PrepareOpenCodeV2Config(root, "/fixture/binary", database, "opencode-v2:realm"); err == nil {
		t.Fatal("owned drift accepted")
	}
	after, _ := os.ReadFile(plan.Destination)
	if !bytes.Equal(before, after) {
		t.Fatal("refusal changed config")
	}
	write(t, owned, string(assets["opencode-v2/runtime.mjs"]))
	if err := os.Remove(owned); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareOpenCodeV2Config(root, "/fixture/binary", database, "opencode-v2:realm"); err == nil {
		t.Fatal("configured but incomplete bundle accepted")
	}
}

func TestOpenCodeV2PlanRefusesLegacyAndProtectedRootWithoutWrites(t *testing.T) {
	home, v1 := isolatedHome(t)
	if _, err := PrepareOpenCodeV2Config(filepath.Dir(v1), "/fixture/binary", "/fixture/db", "opencode-v2:realm"); err == nil {
		t.Fatal("V1 root accepted")
	}
	root := filepath.Join(home, "v2")
	write(t, filepath.Join(root, "opencode.jsonc"), `{"plugin":["legacy"]}`)
	if _, err := PrepareOpenCodeV2Config(root, "/fixture/binary", "/fixture/db", "opencode-v2:realm"); err == nil {
		t.Fatal("active legacy config accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "tools-programmer-integration-v2")); !os.IsNotExist(err) {
		t.Fatal("refusal created bundle", err)
	}
}
