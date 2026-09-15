package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIntegrationCommandsNeverOpenState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	path := filepath.Join(home, "missing-state", "history.db")
	var out bytes.Buffer
	if Run(context.Background(), []string{"--db", path, "integration", "info"}, &out, forbiddenRunner{}) != 0 {
		t.Fatal(out.String())
	}
	var info struct {
		Data struct {
			Protocol     int      `json:"protocol"`
			BundleDigest string   `json:"bundle_digest"`
			Capabilities []string `json:"capabilities"`
		} `json:"data"`
	}
	if json.Unmarshal(out.Bytes(), &info) != nil || info.Data.Protocol != 1 || len(info.Data.BundleDigest) != 64 || len(info.Data.Capabilities) != 3 {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Fatal("info wrote config")
	}
	for _, args := range [][]string{{"integration", "sync", "unsupported"}, {"integration", "sync", "opencode", "--overwrite"}, {"integration", "info", "opencode"}} {
		out.Reset()
		if Run(context.Background(), args, &out, forbiddenRunner{}) == 0 {
			t.Fatal("invalid args accepted")
		}
	}
	out.Reset()
	if Run(context.Background(), []string{"--db", path, "integration", "sync", "opencode"}, &out, forbiddenRunner{}) != 0 {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("sync opened state")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("sync changed global guidance")
	}
}
