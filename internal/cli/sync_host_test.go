package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyncHostPreflightRejectsBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name, script, target, want string
	}{
		{"wrong major", "echo 2.2.5", "opencode", "host_version"},
		{"unsupported version", "echo 1.18.31", "opencode", "host_version"},
		{"prerelease", "echo 1.18.30-rc.1", "opencode", "host_probe"},
		{"malformed", "echo not-a-version", "opencode", "host_probe"},
		{"ambiguous", "printf '1.18.30\\n2.2.5\\n'", "opencode", "host_probe"},
		{"failure", "echo PRIVATE_HOST_ERROR >&2; exit 7", "opencode", "host_probe"},
		{"overflow", "while :; do printf '1234567890'; done", "opencode", "host_probe"},
		{"overflow after valid prefix", "printf '1.18.30'; i=0; while [ \"$i\" -lt 8192 ]; do printf ' '; i=$((i+1)); done", "opencode", "host_probe"},
		{"V1 only for V2", "echo 1.18.30", "opencode-v2", "host_version"},
		{"V2 bundle unavailable", "echo 2.2.5", "opencode-v2", "integration_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolatedSyncHome(t)
			binary := fakeSyncHost(t, tc.script)
			r, code, output := runSyncCommand(t, "sync", tc.target, "--opencode-bin", binary)
			if code == 0 || r.Error == nil || r.Error.Code != tc.want || r.Data.FailedStep == "" {
				t.Fatalf("unexpected rejection: %d %s", code, output)
			}
			if strings.Contains(output, "PRIVATE_HOST_ERROR") || strings.Contains(output, binary) {
				t.Fatalf("probe leaked private details: %s", output)
			}
			assertSyncHomeEmpty(t, home)
		})
	}
}

func TestSyncHostSelectionAndBounds(t *testing.T) {
	t.Run("exact output cap", func(t *testing.T) {
		isolatedSyncHome(t)
		binary := fakeSyncHost(t, "printf '1.18.30'; i=0; while [ \"$i\" -lt 8185 ]; do printf ' '; i=$((i+1)); done")
		r, code, output := runSyncCommand(t, "sync", "opencode", "--opencode-bin", binary)
		if code != 0 || r.Error != nil {
			t.Fatalf("8192-byte valid output rejected: %d %s", code, output)
		}
	})
	t.Run("relative executable", func(t *testing.T) {
		home := isolatedSyncHome(t)
		r, code, output := runSyncCommand(t, "sync", "opencode", "--opencode-bin", "./opencode")
		if code == 0 || r.Error == nil || r.Error.Code != "invalid_input" {
			t.Fatalf("relative path accepted: %d %s", code, output)
		}
		assertSyncHomeEmpty(t, home)
	})
	t.Run("explicit host and stderr", func(t *testing.T) {
		home := isolatedSyncHome(t)
		binary := fakeSyncHost(t, `[ "$#" -eq 1 ] && [ "$1" = "--version" ] || exit 2
i=0; while [ "$i" -lt 10000 ]; do printf 'noise' >&2; i=$((i+1)); done
echo 1.18.30`)
		t.Setenv("PATH", t.TempDir())
		r, code, output := runSyncCommand(t, "sync", "opencode", "--opencode-bin", binary)
		if code != 0 || r.Error != nil || !r.Data.MCP.Changed || len(r.Data.Skill.Updated) != 2 {
			t.Fatalf("matching host failed: %d %s", code, output)
		}
		if _, err := os.Stat(filepath.Join(home, "state")); !os.IsNotExist(err) {
			t.Fatalf("setup opened state: %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		home := isolatedSyncHome(t)
		t.Setenv("PATH", t.TempDir())
		r, code, output := runSyncCommand(t, "sync", "opencode")
		if code == 0 || r.Error == nil || r.Error.Code != "host_unavailable" {
			t.Fatalf("missing host: %d %s", code, output)
		}
		assertSyncHomeEmpty(t, home)
	})
	t.Run("timeout", func(t *testing.T) {
		home := isolatedSyncHome(t)
		binary := fakeSyncHost(t, "while :; do :; done")
		start := time.Now()
		r, code, output := runSyncCommand(t, "sync", "opencode", "--opencode-bin", binary)
		if code == 0 || r.Error == nil || r.Error.Code != "host_probe" || time.Since(start) > 4*time.Second {
			t.Fatalf("timeout not bounded: %d %s (%s)", code, output, time.Since(start))
		}
		assertSyncHomeEmpty(t, home)
	})
}

func TestSyncHostRejectionPreservesExistingSetup(t *testing.T) {
	home := isolatedSyncHome(t)
	good := fakeSyncHost(t, "echo 1.18.30")
	if r, code, output := runSyncCommand(t, "sync", "opencode", "--opencode-bin", good); code != 0 || r.Error != nil {
		t.Fatalf("initial setup: %s", output)
	}
	snapshot := func() map[string]string {
		t.Helper()
		files := map[string]string{}
		err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				files[path] = string(data)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	bad := fakeSyncHost(t, "echo 2.2.5")
	if _, code, _ := runSyncCommand(t, "sync", "opencode", "--opencode-bin", bad); code == 0 {
		t.Fatal("wrong major accepted")
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatal("file inventory changed after rejection")
	}
	for path, content := range before {
		if after[path] != content {
			t.Fatalf("existing setup changed: %s", path)
		}
	}
}

func TestSyncNativeV2ConfigurationRejectedBeforeWrites(t *testing.T) {
	for _, config := range []string{
		`{"plugins":[{"package":"./v2-plugin","options":{"policy":"keep"}}]}`,
		`{"mcp":{"servers":{"tools-programmer":{"type":"local","command":["keep"]}}}}`,
	} {
		t.Run(config, func(t *testing.T) {
			home := isolatedSyncHome(t)
			dir := filepath.Join(t.TempDir(), "custom-config")
			t.Setenv("OPENCODE_CONFIG_DIR", dir)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "opencode.jsonc")
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			r, code, output := runSyncCommand(t, "sync", "opencode")
			if code == 0 || r.Error == nil || r.Error.Code != "config_conflict" {
				t.Fatalf("native V2 config accepted: %d %s", code, output)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != config {
				t.Fatalf("config modified: %s (%v)", data, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("setup left files: %v %v", entries, err)
			}
			assertSyncHomeEmpty(t, home)
		})
	}
}
