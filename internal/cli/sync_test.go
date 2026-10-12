package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tools-programmer/internal/core"
)

type syncResponse struct {
	Data  SyncResult  `json:"data"`
	Error *core.Error `json:"error"`
}

func runSyncCommand(t *testing.T, args ...string) (syncResponse, int, string) {
	t.Helper()
	var out bytes.Buffer
	code := Run(context.Background(), args, &out, forbiddenRunner{t})
	var response syncResponse
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("%s: %v", out.String(), err)
	}
	return response, code, out.String()
}

func isolatedSyncHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("TOOLS_PROGRAMMER_DB", "")
	return home
}

func writeFakeOpenCode(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func installFakeOpenCode(t *testing.T) string {
	t.Helper()
	path := writeFakeOpenCode(t, "printf '1.18.30\\n'\n")
	t.Setenv("PATH", filepath.Dir(path))
	return path
}

func requireNoSyncWrites(t *testing.T, home, database string) {
	t.Helper()
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("preflight changed HOME: entries=%v err=%v", entries, err)
	}
	if _, err := os.Stat(filepath.Dir(database)); !os.IsNotExist(err) {
		t.Fatalf("preflight created database directory: %v", err)
	}
}

func TestSyncOpenCodeOrchestratesInOrderAndIsIdempotent(t *testing.T) {
	home := isolatedSyncHome(t)
	installFakeOpenCode(t)
	database := filepath.Join(home, "state", "history.db")

	first, code, output := runSyncCommand(t, "--db", database, "sync", "opencode")
	if code != 0 || first.Error != nil {
		t.Fatalf("first sync: code=%d output=%s", code, output)
	}
	if len(first.Data.Skill.Updated) != 2 || len(first.Data.Skill.Unchanged) != 0 {
		t.Fatalf("skill step: %+v", first.Data.Skill)
	}
	if len(first.Data.Integration.Updated) == 0 || !first.Data.Integration.ConfigChanged {
		t.Fatalf("integration step: %+v", first.Data.Integration)
	}
	if !first.Data.MCP.Changed || len(first.Data.MCP.Command) != 5 || first.Data.FailedStep != "" {
		t.Fatalf("MCP step: %+v", first.Data)
	}

	second, code, output := runSyncCommand(t, "--db", database, "sync", "opencode")
	if code != 0 || second.Error != nil {
		t.Fatalf("repeat sync: code=%d output=%s", code, output)
	}
	if len(second.Data.Skill.Updated) != 0 || len(second.Data.Skill.Unchanged) != 2 {
		t.Fatalf("repeat skill step: %+v", second.Data.Skill)
	}
	if len(second.Data.Integration.Updated) != 0 || second.Data.Integration.ConfigChanged || second.Data.MCP.Changed {
		t.Fatalf("repeat was not a no-op: %+v", second.Data)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("sync changed AGENTS.md: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(database)); !os.IsNotExist(err) {
		t.Fatalf("sync opened or created database: %v", err)
	}
}

func TestSyncOpenCodePreservesUnrelatedConfiguration(t *testing.T) {
	home := isolatedSyncHome(t)
	installFakeOpenCode(t)
	configDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	config := "{\n// preserve this comment\n\"model\":\"fixture/model\",\n\"plugin\":[\"other-plugin\"],\n\"permission\":{\"bash\":{\"*\":\"ask\"}},\n\"number\":9007199254740993,\n}\n"
	path := filepath.Join(configDir, "opencode.jsonc")
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}

	response, code, output := runSyncCommand(t, "--db", filepath.Join(home, "db", "history.db"), "sync", "opencode")
	if code != 0 || response.Error != nil {
		t.Fatalf("sync failed: code=%d output=%s", code, output)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"// preserve this comment", `"model":"fixture/model"`, `"other-plugin"`, `"permission":{"bash":{"*":"ask"}}`, "9007199254740993"} {
		if !bytes.Contains(updated, []byte(keep)) {
			t.Fatalf("lost unrelated configuration %q: %s", keep, updated)
		}
	}
}

func TestSyncOpenCodeRetainsEarlierResultsWhenMCPConflicts(t *testing.T) {
	home := isolatedSyncHome(t)
	installFakeOpenCode(t)
	configDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	config := `{"mcp":{"tools-programmer":{"type":"local","command":["custom"]}}}`
	if err := os.WriteFile(filepath.Join(configDir, "opencode.jsonc"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}

	response, code, output := runSyncCommand(t, "--db", filepath.Join(home, "db", "history.db"), "sync", "opencode")
	if code == 0 || response.Error == nil || response.Error.Code != "config_conflict" {
		t.Fatalf("expected MCP conflict: code=%d output=%s", code, output)
	}
	if response.Data.FailedStep != syncMCPInstallStep {
		t.Fatalf("wrong failed step: %+v", response.Data)
	}
	if len(response.Data.Skill.Updated) != 2 || len(response.Data.Integration.Updated) == 0 || !response.Data.Integration.ConfigChanged {
		t.Fatalf("earlier results were lost: %+v", response.Data)
	}
	if len(response.Data.MCP.Command) != 5 || response.Data.MCP.Changed {
		t.Fatalf("MCP result: %+v", response.Data.MCP)
	}
	updated, err := os.ReadFile(filepath.Join(configDir, "opencode.jsonc"))
	if err != nil || !bytes.Contains(updated, []byte(`"command":["custom"]`)) {
		t.Fatalf("conflicting configuration was changed: %s (%v)", updated, err)
	}
}

func TestSyncOpenCodeRejectsMissingHostBeforeWrites(t *testing.T) {
	home := isolatedSyncHome(t)
	database := filepath.Join(home, "state", "history.db")
	response, code, output := runSyncCommand(t, "--db", database, "sync", "opencode")
	if code == 0 || response.Error == nil || response.Error.Code != "opencode_not_found" {
		t.Fatalf("expected missing-host preflight error: code=%d output=%s", code, output)
	}
	if !strings.Contains(response.Error.Message, "expected version 1.18.30") || !strings.Contains(response.Error.Message, "--opencode-bin PATH") {
		t.Fatalf("missing-host error is not actionable: %s", response.Error.Message)
	}
	if response.Data.FailedStep != "OpenCode version preflight" {
		t.Fatalf("wrong failed step: %+v", response.Data)
	}
	requireNoSyncWrites(t, home, database)
}

func TestSyncOpenCodeRejectsMismatchedExplicitHostBeforeWrites(t *testing.T) {
	home := isolatedSyncHome(t)
	database := filepath.Join(home, "state", "history.db")
	fake := writeFakeOpenCode(t, "printf '2.0.23\\n'\n")
	response, code, output := runSyncCommand(t, "--db", database, "sync", "opencode", "--opencode-bin", fake)
	if code == 0 || response.Error == nil || response.Error.Code != "opencode_version_mismatch" {
		t.Fatalf("expected version mismatch: code=%d output=%s", code, output)
	}
	if !strings.Contains(response.Error.Message, "expected version 1.18.30") || !strings.Contains(response.Error.Message, "detected version 2.0.23") {
		t.Fatalf("version mismatch is not actionable: %s", response.Error.Message)
	}
	requireNoSyncWrites(t, home, database)
}

func TestSyncOpenCodeValidatesBeforeWrites(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
	}{
		{name: "missing command", args: []string{"sync"}, code: 1},
		{name: "wrong target", args: []string{"sync", "other"}, code: 1},
		{name: "extra argument", args: []string{"sync", "opencode", "extra"}, code: 1},
		{name: "memory database", args: []string{"--db", ":memory:", "sync", "opencode"}, code: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := isolatedSyncHome(t)
			response, code, output := runSyncCommand(t, tt.args...)
			if code != tt.code {
				t.Fatalf("code=%d output=%s", code, output)
			}
			if tt.name == "memory database" && (response.Error == nil || response.Error.Code != "invalid_input") {
				t.Fatalf("memory database error: %s", output)
			}
			if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
				t.Fatalf("validation touched HOME: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestSyncOpenCodeHelpDoesNotWrite(t *testing.T) {
	home := isolatedSyncHome(t)
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "opencode", "--help"}, &out, forbiddenRunner{t}); code != 0 {
		t.Fatalf("code=%d output=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "sync            opencode") || !strings.Contains(out.String(), "--opencode-bin PATH") {
		t.Fatalf("sync help missing: %s", out.String())
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("help touched HOME: entries=%v err=%v", entries, err)
	}
}
