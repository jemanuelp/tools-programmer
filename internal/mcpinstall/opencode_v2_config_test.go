package mcpinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openCodeV2TestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	return home
}

func TestOpenCodeV2DefaultRootIgnoresV1Override(t *testing.T) {
	for _, useXDG := range []bool{false, true} {
		t.Run(map[bool]string{false: "home-default", true: "xdg"}[useXDG], func(t *testing.T) {
			home := openCodeV2TestHome(t)
			base := filepath.Join(home, ".config")
			if useXDG {
				base = filepath.Join(home, "xdg")
				t.Setenv("XDG_CONFIG_HOME", base)
			}
			t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(home, "v1-override"))

			got, err := resolveOpenCodeV2Config("")
			want := filepath.Join(base, "opencode-v2")
			if err != nil || got.Root != want || got.Destination != filepath.Join(want, "opencode.jsonc") {
				t.Fatalf("resolved %+v, %v; want root %q", got, err, want)
			}
			if _, err := os.Stat(want); !os.IsNotExist(err) {
				t.Fatalf("read-only resolution created root: %v", err)
			}
		})
	}
}

func TestOpenCodeV2ExplicitRootAndV1OverlapProtection(t *testing.T) {
	t.Run("explicit-root", func(t *testing.T) {
		home := openCodeV2TestHome(t)
		t.Setenv("OPENCODE_CONFIG_DIR", filepath.Join(home, "active-v1"))
		root := filepath.Join(home, "chosen-v2")
		got, err := resolveOpenCodeV2Config(root)
		if err != nil || got.Root != root || got.Destination != filepath.Join(root, "opencode.jsonc") {
			t.Fatalf("resolved %+v, %v", got, err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("explicit resolution created root: %v", err)
		}
	})

	for _, overlap := range []string{
		"active-exact", "active-child", "active-parent",
		"default-exact", "default-child", "default-parent",
	} {
		t.Run(overlap, func(t *testing.T) {
			home := openCodeV2TestHome(t)
			active := filepath.Join(home, "active-v1")
			t.Setenv("OPENCODE_CONFIG_DIR", active)
			fallback := filepath.Join(home, ".config", "opencode")
			var root string
			switch overlap {
			case "active-exact":
				root = active
			case "active-child":
				root = filepath.Join(active, "nested")
			case "active-parent":
				root = filepath.Dir(active)
			case "default-exact":
				root = fallback
			case "default-child":
				root = filepath.Join(fallback, "nested")
			case "default-parent":
				root = filepath.Dir(fallback)
			}
			_, beforeErr := os.Lstat(root)
			if beforeErr != nil && !os.IsNotExist(beforeErr) {
				t.Fatal(beforeErr)
			}
			if _, err := resolveOpenCodeV2Config(root); err == nil || !strings.Contains(err.Error(), "distinct") {
				t.Fatalf("accepted overlapping root %q: %v", root, err)
			}
			_, afterErr := os.Lstat(root)
			if (beforeErr == nil) != (afterErr == nil) {
				t.Fatalf("rejected resolution changed root existence: %v", afterErr)
			}
		})
	}
}

func TestOpenCodeV2ConfigSelectionAndReadOnlyPreflight(t *testing.T) {
	for _, tt := range []struct {
		name     string
		files    map[string]string
		selected string
	}{
		{name: "empty-root", selected: "opencode.jsonc"},
		{name: "jsonc-preferred", files: map[string]string{
			"opencode.jsonc": "{/* preferred */}", "opencode.json": "not JSON", "config.json": "legacy",
		}, selected: "opencode.jsonc"},
		{name: "json-fallback", files: map[string]string{"opencode.json": `{"model":"keep"}`}, selected: "opencode.json"},
		{name: "native-containers", files: map[string]string{"opencode.jsonc": `{"plugin":[],"plugins":[{"package":"example","options":{"enabled":true}}],"mcp":{"servers":{"docs":{"type":"remote","url":"https://example.invalid"}}}}`}, selected: "opencode.jsonc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := openCodeV2TestHome(t)
			root := filepath.Join(home, "v2")
			for name, content := range tt.files {
				write(t, filepath.Join(root, name), content)
			}

			got, err := resolveOpenCodeV2Config(root)
			if err != nil || got.Root != root || filepath.Base(got.Destination) != tt.selected {
				t.Fatalf("resolved %+v, %v; want %s", got, err, tt.selected)
			}
			for name, content := range tt.files {
				b, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(b) != content {
					t.Fatalf("preflight changed %s: %v", name, err)
				}
			}
			if len(tt.files) == 0 {
				if _, err := os.Stat(root); !os.IsNotExist(err) {
					t.Fatalf("successful resolution created root: %v", err)
				}
			}
		})
	}
}

func TestOpenCodeV2RefusesLegacyOnlyAndUnsafeConfig(t *testing.T) {
	for _, tt := range []struct {
		name, file, content string
	}{
		{"legacy-config-only", "config.json", `{}`},
		{"empty-document", "opencode.jsonc", ""},
		{"malformed", "opencode.jsonc", `{"model":`},
		{"duplicate-key", "opencode.json", `{"plugins":[],"plugins":[]}`},
		{"native-plugins-not-array", "opencode.jsonc", `{"plugins":{}}`},
		{"mcp-not-object", "opencode.jsonc", `{"mcp":null}`},
		{"mcp-servers-not-object", "opencode.jsonc", `{"mcp":{"servers":[]}}`},
		{"legacy-plugin-mask", "opencode.jsonc", `{"plugin":["legacy-plugin"]}`},
		{"legacy-and-native-plugin", "opencode.jsonc", `{"plugin":["legacy-plugin"],"plugins":["native-plugin"]}`},
		{"oversized", "opencode.jsonc", strings.Repeat(" ", (1<<20)+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := openCodeV2TestHome(t)
			root := filepath.Join(home, "v2")
			path := filepath.Join(root, tt.file)
			write(t, path, tt.content)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := resolveOpenCodeV2Config(root); err == nil {
				t.Fatal("accepted unsafe configuration")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("rejected preflight changed config: %v", err)
			}
		})
	}
}

func TestOpenCodeV2RefusesSymlinkedRootsAndFiles(t *testing.T) {
	for _, kind := range []string{"root", "selected-file"} {
		t.Run(kind, func(t *testing.T) {
			home := openCodeV2TestHome(t)
			root := filepath.Join(home, "v2")
			outside := filepath.Join(home, "outside")
			write(t, outside, `{}`)
			if kind == "root" {
				if err := os.Symlink(filepath.Dir(outside), root); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "opencode.jsonc")); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := resolveOpenCodeV2Config(root); err == nil {
				t.Fatal("accepted symlinked configuration path")
			}
			if b, err := os.ReadFile(outside); err != nil || string(b) != `{}` {
				t.Fatalf("symlink target changed: %v", err)
			}
		})
	}
}
