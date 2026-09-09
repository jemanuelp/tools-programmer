package mcpinstall

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

func isolatedHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	return home, filepath.Join(home, ".config", "opencode", "opencode.jsonc")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallPreservesConfigAndIsIdempotent(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "existing"}[existing], func(t *testing.T) {
			home, path := isolatedHome(t)
			if existing {
				write(t, path, `{"$schema":"custom-schema","model":"provider/model","mcp":{"other":{"type":"local","command":["other"],"environment":{"KEEP":"private-value"}}},"permission":{"bash":{"z*":"deny","*":"allow"}},"unknown":{"number":9007199254740993}}`)
				if err := os.Chmod(path, 0640); err != nil {
					t.Fatal(err)
				}
			}
			database := filepath.Join(home, "missing", "history.db")
			result, err := Install("opencode", database)
			if err != nil || !result.Changed || result.Destination != path {
				t.Fatalf("%+v %v", result, err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var cfg map[string]json.RawMessage
			if err := json.Unmarshal(b, &cfg); err != nil {
				t.Fatal(err)
			}
			var servers map[string]json.RawMessage
			json.Unmarshal(cfg["mcp"], &servers)
			var entry struct {
				Type    string   `json:"type"`
				Command []string `json:"command"`
				Enabled bool     `json:"enabled"`
			}
			if err := json.Unmarshal(servers["tools-programmer"], &entry); err != nil {
				t.Fatal(err)
			}
			// Targeted verification of the published McpLocalConfig schema: only
			// type/local, string-array command, and boolean enabled are generated.
			var fields map[string]json.RawMessage
			json.Unmarshal(servers["tools-programmer"], &fields)
			if len(fields) != 3 || entry.Type != "local" || !entry.Enabled || !reflect.DeepEqual(entry.Command, result.Command) || len(entry.Command) != 5 || !filepath.IsAbs(entry.Command[0]) || entry.Command[1] != "--db" || entry.Command[2] != database || entry.Command[3] != "mcp" || entry.Command[4] != "serve" {
				t.Fatalf("invalid local config: %s", servers["tools-programmer"])
			}
			if existing {
				if !strings.Contains(string(b), "private-value") || !strings.Contains(string(b), "9007199254740993") || string(cfg["$schema"]) != `"custom-schema"` || bytes.Index(cfg["permission"], []byte(`"z*"`)) > bytes.Index(cfg["permission"], []byte(`"*"`)) {
					t.Fatalf("unrelated settings changed: %s", b)
				}
				info, _ := os.Stat(path)
				if info.Mode().Perm() != 0640 {
					t.Fatal("permissions changed")
				}
			}
			info, _ := os.Stat(path)
			again, err := Install("opencode", database)
			if err != nil || again.Changed {
				t.Fatalf("not idempotent: %+v %v", again, err)
			}
			after, _ := os.ReadFile(path)
			infoAfter, _ := os.Stat(path)
			if !bytes.Equal(b, after) || !info.ModTime().Equal(infoAfter.ModTime()) {
				t.Fatal("idempotent call rewrote file")
			}
			if _, err := os.Stat(filepath.Dir(database)); !os.IsNotExist(err) {
				t.Fatal("installer touched database directory")
			}
		})
	}
}

func TestConfigErrorsPreserveOriginal(t *testing.T) {
	for _, tt := range []struct{ name, file, content string }{
		{"custom-entry", "opencode.json", `{"mcp":{"tools-programmer":{"type":"remote","url":"https://example.invalid","headers":{"secret":"DO_NOT_ECHO"}}}}`},
		{"custom-args", "opencode.json", `{"mcp":{"tools-programmer":{"type":"local","command":["other","--arg"],"environment":{"TOKEN":"DO_NOT_ECHO"}}}}`},
		{"malformed", "opencode.json", `{"model":`},
		{"jsonc-extension", "opencode.jsonc", `{// keep comment\n}`},
		{"duplicate-key", "opencode.json", `{"mcp":{},"mcp":{}}`},
		{"nested-duplicate", "opencode.json", `{"permission":{"bash":{"*":"allow","*":"deny"}}}`},
		{"null-root", "opencode.json", `null`},
		{"null-mcp", "opencode.json", `{"mcp":null}`},
		{"array-mcp", "opencode.json", `{"mcp":[]}`},
		{"legacy-layer", "config.json", `{}`},
		{"escaped-duplicate", "opencode.jsonc", `{"mcp":{},"\u006dcp":{}}`},
		{"duplicate-in-array", "opencode.jsonc", `{"x":[{"a":1,/*keep*/"\u0061":2}]}`},
		{"unterminated-comment", "opencode.jsonc", `{/* comment}`},
		{"unterminated-string", "opencode.jsonc", `{"x":"abc}`},
		{"double-comma", "opencode.jsonc", `{"x":1,,}`},
		{"array-leading-comma", "opencode.jsonc", `{"x":[,]}`},
		{"bad-number", "opencode.jsonc", `{"x":01}`},
		{"bad-escape", "opencode.jsonc", `{"x":"\q"}`},
		{"extra-document", "opencode.jsonc", `{} /*keep*/ {}`},
		{"empty-document", "opencode.jsonc", ""},
		{"comment-only", "opencode.jsonc", "// comment\n"},
		{"jsonc-mcp-type", "opencode.jsonc", `{"mcp":false,}`},
		{"jsonc-conflict", "opencode.jsonc", `{"mcp":{"tools-programmer":{/*keep*/"enabled":false,},},}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, path := isolatedHome(t)
			file := filepath.Join(filepath.Dir(path), tt.file)
			write(t, file, tt.content)
			result, err := Install("opencode", filepath.Join(home, "history.db"))
			if err == nil || result.Changed || strings.Contains(err.Error(), "DO_NOT_ECHO") {
				t.Fatalf("%+v %v", result, err)
			}
			b, _ := os.ReadFile(file)
			if string(b) != tt.content {
				t.Fatal("original changed")
			}
			if tt.file != "opencode.jsonc" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("created shadow config")
				}
			}
		})
	}
}

func TestConfigSelection(t *testing.T) {
	for _, tt := range []struct {
		name     string
		files    map[string]string
		selected string
	}{
		{"neither", nil, "opencode.jsonc"},
		{"jsonc", map[string]string{"opencode.jsonc": "{/*keep*/}"}, "opencode.jsonc"},
		{"json", map[string]string{"opencode.json": "{}"}, "opencode.json"},
		{"both", map[string]string{"opencode.jsonc": "{/*keep*/}", "opencode.json": "{\n  \"model\": \"keep\"\n}"}, "opencode.jsonc"},
		{"jsonc-with-invalid-lower-files", map[string]string{"opencode.jsonc": "{}", "opencode.json": "not JSON", "config.json": "legacy"}, "opencode.jsonc"},
		{"json-with-legacy", map[string]string{"opencode.json": "{}", "config.json": "legacy"}, "opencode.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, path := isolatedHome(t)
			for name, content := range tt.files {
				write(t, filepath.Join(filepath.Dir(path), name), content)
			}
			r, err := Install("opencode", filepath.Join(home, "missing", "history.db"))
			if err != nil || !r.Changed || filepath.Base(r.Destination) != tt.selected {
				t.Fatalf("%+v %v", r, err)
			}
			for name, content := range tt.files {
				if name != tt.selected {
					b, err := os.ReadFile(filepath.Join(filepath.Dir(path), name))
					if err != nil || string(b) != content {
						t.Fatalf("nonselected %s changed: %v", name, err)
					}
				}
			}
			if tt.selected == "opencode.json" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("created shadow JSONC")
				}
			}
			if _, err := os.Stat(filepath.Join(home, "missing")); !os.IsNotExist(err) {
				t.Fatal("opened database")
			}
		})
	}
}

func TestJSONCEntryCommentsAndConflict(t *testing.T) {
	home, path := isolatedHome(t)
	database := filepath.Join(home, "history.db")
	if _, err := Install("opencode", database); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	commented := strings.Replace(string(b), `"enabled":true`, `"enabled":true /*entry comment*/,`, 1)
	write(t, path, commented)
	r, err := Install("opencode", database)
	if err != nil || r.Changed {
		t.Fatalf("commented entry is not identical: %+v %v", r, err)
	}
	r, err = Install("opencode", filepath.Join(home, "different.db"))
	if err == nil || r.Changed {
		t.Fatal("overwrote conflicting entry")
	}
	after, _ := os.ReadFile(path)
	if string(after) != commented {
		t.Fatal("changed commented entry")
	}
}

func TestSelectionDoesNotBypassUnsafeTarget(t *testing.T) {
	for _, selected := range []string{"opencode.jsonc", "opencode.json"} {
		t.Run(selected, func(t *testing.T) {
			home, path := isolatedHome(t)
			write(t, filepath.Join(filepath.Dir(path), "config.json"), "legacy")
			if selected == "opencode.jsonc" {
				write(t, filepath.Join(filepath.Dir(path), "opencode.json"), "{}")
			}
			if err := os.Symlink(filepath.Join(home, "missing"), filepath.Join(filepath.Dir(path), selected)); err != nil {
				t.Fatal(err)
			}
			if _, err := Install("opencode", filepath.Join(home, "history.db")); err == nil {
				t.Fatal("bypassed selected symlink")
			}
		})
	}
}

func TestJSONCSurgicalInsertion(t *testing.T) {
	// The marker identifies the sole insertion boundary. Removing the expected
	// addition must restore every original byte, not merely equivalent JSON.
	for _, tt := range []struct {
		name, marked string
		mcp, comma   bool
	}{
		{"empty-root", "/*before*/ {|\r\n// root\r\n}\n// after", false, false},
		{"root-trailing-comma", "{\n\"model\":\"x\"| /*tail*/, // root\n}\n", false, true},
		{"root-line-comment", "{\"x\":1|// tail\n}", false, true},
		{"empty-mcp", "{\"mcp\":{|/*keep*/},}", true, false},
		{"mcp-trailing-comma", "{\"mcp\":{\"other\":{}| /*before comma*/, // after comma\n},}", true, true},
		{"mcp-no-comma", "{\"mcp\":{\"other\":{}|// tail\n}}", true, true},
		{"strings-and-nesting", `{"unknown":[{"n":9007199254740993,"s":"https://x/*not-comment*/\\\"",},],"permission":{"bash":{"z*":"deny","*":"allow"}},"mcp":{"other":{"array":[1,{"x":"//"},],}|,}}`, true, true},
		{"escaped-mcp", `{"\u006dcp":{|}}`, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, path := isolatedHome(t)
			parts := strings.Split(tt.marked, "|")
			if len(parts) != 2 {
				t.Fatal("bad fixture")
			}
			original := parts[0] + parts[1]
			write(t, path, original)
			database := filepath.Join(home, "missing", "history.db")
			r, err := Install("opencode", database)
			if err != nil || !r.Changed {
				t.Fatalf("%+v %v", r, err)
			}
			entry, _ := json.Marshal(map[string]any{"type": "local", "command": r.Command, "enabled": true})
			// Match the generated entry's declared field order, independently of AST offsets.
			var fields map[string]json.RawMessage
			json.Unmarshal(entry, &fields)
			entry = []byte(`{"type":"local","command":` + string(fields["command"]) + `,"enabled":true}`)
			addition := "\n  \"tools-programmer\": " + string(entry)
			if !tt.mcp {
				addition = "\n  \"mcp\": {\"tools-programmer\":" + string(entry) + "}"
			}
			if tt.comma {
				addition = "," + addition
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != parts[0]+addition+parts[1] {
				t.Fatalf("non-surgical edit: %s (%v)", got, err)
			}
			standard, err := hujson.Standardize(append(append([]byte(nil), got...), '\n'))
			if err != nil || !json.Valid(standard) || !uniqueJSON(standard) {
				t.Fatal("invalid output", err)
			}
			again, err := Install("opencode", database)
			after, _ := os.ReadFile(path)
			if err != nil || again.Changed || !bytes.Equal(got, after) {
				t.Fatalf("JSONC no-op rewrote file: %+v %v\nbefore=%s\nafter=%s", again, err, got, after)
			}
		})
	}
}

func TestInstallRefusesPathHazards(t *testing.T) {
	for _, kind := range []string{"parent-link", "file-link", "dangling-link", "directory-file", "file-parent", "home-link"} {
		t.Run(kind, func(t *testing.T) {
			home, path := isolatedHome(t)
			outside := t.TempDir()
			var err error
			switch kind {
			case "parent-link":
				err = os.Symlink(outside, filepath.Join(home, ".config"))
			case "home-link":
				link := filepath.Join(home, "link")
				err = os.Symlink(outside, link)
				t.Setenv("HOME", link)
			case "file-parent":
				err = os.WriteFile(filepath.Join(home, ".config"), nil, 0600)
			case "directory-file":
				err = os.MkdirAll(path, 0700)
			default:
				if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(outside, "config")
				if kind == "file-link" {
					write(t, target, "original")
				}
				err = os.Symlink(target, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Install("opencode", filepath.Join(home, "history.db")); err == nil {
				t.Fatal("accepted unsafe path")
			}
			files, _ := os.ReadDir(outside)
			if (kind == "file-link" && len(files) != 1) || (kind != "file-link" && len(files) != 0) {
				t.Fatal("wrote through link")
			}
		})
	}
}

func TestTargetAndConfigPathResolution(t *testing.T) {
	home, path := isolatedHome(t)
	if _, err := Install("future-agent", filepath.Join(home, "history.db")); err == nil {
		t.Fatal("unsupported target accepted")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("unsupported target changed filesystem")
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	dir, err := configDirectory()
	if err != nil || dir != filepath.Join(home, "xdg", "opencode") {
		t.Fatal(dir, err)
	}
	t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(home, "override"))
	dir, err = configDirectory()
	if err != nil || dir != filepath.Join(home, "override") {
		t.Fatal(dir, err)
	}
	t.Setenv("OPENCODE_CONFIG_DIR", "relative")
	if _, err := configDirectory(); err == nil {
		t.Fatal("accepted relative override")
	}
}
