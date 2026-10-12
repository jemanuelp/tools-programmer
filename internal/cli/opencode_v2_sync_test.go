package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeV2PublicSetupCandidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(home, "v1"))
	if err := os.MkdirAll(filepath.Join(home, "v1"), 0700); err != nil {
		t.Fatal(err)
	}
	v1 := filepath.Join(home, "v1", "opencode.jsonc")
	const original = "{\n // V1 unchanged\n \"plugin\": []\n}\n"
	if err := os.WriteFile(v1, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(home, "missing", "history.db")
	root := filepath.Join(home, "config", "opencode-v2")
	host := filepath.Join(home, "fake-host")
	args := []string{"--db", database, "sync", "opencode-v2", "--opencode-bin", host, "--runtime-id", "opencode-v2:candidate"}
	run := func(args []string) (int, map[string]json.RawMessage) {
		t.Helper()
		var out bytes.Buffer
		code := RunWithIO(context.Background(), args, io.NopCloser(strings.NewReader("")), &out, io.Discard, nil)
		var result map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(out.String(), err)
		}
		return code, result
	}
	for _, version := range []string{"opencode v2.0.23", "opencode v2.0.26"} {
		if err := os.WriteFile(host, []byte("#!/bin/sh\nprintf '%s\\n' '"+version+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
		code, result := run(args)
		if version == "opencode v2.0.23" {
			if code == 0 || !bytes.Contains(result["error"], []byte("opencode_version_mismatch")) {
				t.Fatal(code, result)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("refusal wrote setup", err)
			}
			continue
		}
		if code != 0 {
			t.Fatal(code, result)
		}
		var data struct {
			Target        string   `json:"target"`
			Updated       []string `json:"updated"`
			ConfigChanged bool     `json:"config_changed"`
			Digest        string   `json:"digest"`
		}
		if err := json.Unmarshal(result["data"], &data); err != nil {
			t.Fatal(err)
		}
		if data.Target != "opencode-v2" || len(data.Updated) != 9 || !data.ConfigChanged || len(data.Digest) != 64 {
			t.Fatal(data)
		}
		configured, err := os.ReadFile(filepath.Join(root, "opencode.jsonc"))
		if err != nil || !bytes.Contains(configured, []byte("opencode-v2:candidate")) || bytes.Contains(configured, []byte("assignmentPolicy")) {
			t.Fatal(string(configured), err)
		}
		code, result = run(args)
		if code != 0 {
			t.Fatal(code, result)
		}
		if err := json.Unmarshal(result["data"], &data); err != nil || data.ConfigChanged || len(data.Updated) != 0 {
			t.Fatal("not idempotent", data, err)
		}
		if again, err := os.ReadFile(filepath.Join(root, "opencode.jsonc")); err != nil || !bytes.Equal(again, configured) {
			t.Fatal("idempotence changed bytes", err)
		}
		asset := filepath.Join(root, "tools-programmer-integration-v2", data.Digest, "opencode-v2", "plugin.ts")
		if err := os.WriteFile(asset, []byte("operator edit"), 0600); err != nil {
			t.Fatal(err)
		}
		code, result = run(args)
		if code == 0 || !bytes.Contains(result["error"], []byte("integration_drift")) {
			t.Fatal("drift accepted", code, result)
		}
		if after, err := os.ReadFile(asset); err != nil || string(after) != "operator edit" {
			t.Fatal("drift overwritten", err)
		}
		if after, err := os.ReadFile(filepath.Join(root, "opencode.jsonc")); err != nil || !bytes.Equal(after, configured) {
			t.Fatal("drift changed config", err)
		}
	}
	if b, err := os.ReadFile(v1); err != nil || string(b) != original {
		t.Fatal("V1 changed", err)
	}
	if _, err := os.Stat(filepath.Dir(database)); !os.IsNotExist(err) {
		t.Fatal("database created", err)
	}
	code, result := run([]string{"integration", "info", "opencode-v2"})
	if code != 0 || !bytes.Contains(result["data"], []byte("0.1.0-candidate.2.0.26")) || !bytes.Contains(result["data"], []byte("2.0.26")) {
		t.Fatal(code, result)
	}
}
