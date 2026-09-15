package toolsprogrammer_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"

	bundle "tools-programmer"
	"tools-programmer/internal/skillsync"
)

func TestIntegrationInventoryAndIndependentSkillSync(t *testing.T) {
	assets, m, err := bundle.IntegrationAssets()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"advisory.d.mts", "advisory.mjs", "context.d.mts", "context.mjs", "diagnostics.d.mts", "diagnostics.mjs", "issue-tracking.md", "observer.d.mts", "observer.mjs", "parent-snapshot.d.mts", "parent-snapshot.mjs", "plugin.ts", "runtime-read.mjs", "session-snapshot.d.mts", "session-snapshot.mjs", "transport.d.mts", "transport.mjs"}
	var names []string
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, want) || len(m.Files) != len(want) {
		t.Fatal(names)
	}
	imports := regexp.MustCompile(`(?:from |import\()["']\./([^"']+)["']`)
	for name, b := range assets {
		for _, match := range imports.FindAllSubmatch(b, -1) {
			if _, ok := assets[string(match[1])]; !ok {
				t.Fatalf("%s imports missing %s", name, match[1])
			}
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	r, err := skillsync.Sync()
	if err != nil || len(r.Updated) != 2 {
		t.Fatal(r, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "tools-programmer-integration")); !os.IsNotExist(err) {
		t.Fatal("skill sync broadened scope")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.jsonc")); !os.IsNotExist(err) {
		t.Fatal("skill sync wrote config")
	}
}
