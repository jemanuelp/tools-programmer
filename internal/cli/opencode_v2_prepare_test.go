package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"tools-programmer/internal/core"
)

func TestOpenCodeV2PreparationGuardsBeforeConfigAndNeverWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	database := filepath.Join(home, "missing-state", "history.db")
	root := filepath.Join(home, ".config", "opencode-v2")
	host := filepath.Join(home, "fake-opencode")
	for _, version := range []string{"opencode v2.0.23", "opencode v2.0.26"} {
		if err := os.WriteFile(host, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		plan, err := prepareOpenCodeV2(database, host, root, "opencode-v2:realm")
		if version == "opencode v2.0.26" {
			if err != nil || plan.Mode != "plan_only" || !plan.ConfigChanged {
				t.Fatal(plan, err)
			}
		} else {
			var typed *core.Error
			if !errors.As(err, &typed) || typed.Code != "opencode_version_mismatch" || plan.Config != "" {
				t.Fatal("wrong-version preparation accepted", plan, err)
			}
		}
		for _, absent := range []string{filepath.Dir(root), filepath.Dir(database)} {
			if _, err := os.Stat(absent); !os.IsNotExist(err) {
				t.Fatal("preparation created files", absent, err)
			}
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "opencode.jsonc")
	const malformed = `{"plugins":null}`
	if err := os.WriteFile(destination, []byte(malformed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host, []byte("#!/bin/sh\nprintf 'opencode v2.0.24\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := prepareOpenCodeV2(database, host, root, "opencode-v2:realm")
	var typed *core.Error
	if !errors.As(err, &typed) || typed.Code != "opencode_version_mismatch" {
		t.Fatal("config was considered before version refusal", err)
	}
	if err := os.WriteFile(host, []byte("#!/bin/sh\nprintf 'opencode v2.0.26\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareOpenCodeV2(database, host, root, "opencode-v2:realm"); err == nil {
		t.Fatal("malformed configuration accepted")
	}
	if content, err := os.ReadFile(destination); err != nil || string(content) != malformed {
		t.Fatal("refusal changed configuration", err)
	}
}
