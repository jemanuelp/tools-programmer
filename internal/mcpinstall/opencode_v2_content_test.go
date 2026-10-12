package mcpinstall

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeV2ContentPreservesJSONCAndOptions(t *testing.T) {
	base := filepath.Join(t.TempDir(), "tools-programmer-integration-v2")
	digest := strings.Repeat("a", 64)
	for _, source := range []string{"", `{
// keep header
"model":"fixture/model", "plugins":["other-plugin", {"package":"./other.ts","options":{"secret":"keep"}}, /* keep tail */],
"permissions":[{"action":"edit","resource":"*","effect":"deny"}], "mcp":{"servers":{}}, "number":9007199254740993,
}`} {
		t.Run(map[bool]string{true: "new", false: "existing"}[source == ""], func(t *testing.T) {
			content, old, err := openCodeV2Content([]byte(source), base, digest, "/fixture/binary", "/fixture/db", "opencode-v2:realm")
			if err != nil || old != "" {
				t.Fatal(old, err)
			}
			if source != "" {
				for _, keep := range []string{"// keep header", "/* keep tail */", `"secret":"keep"`, `"number":9007199254740993`, `"permissions":[{"action":"edit","resource":"*","effect":"deny"}]`, `"mcp":{"servers":{}}`} {
					if !bytes.Contains(content, []byte(keep)) {
						t.Fatal("lost custom bytes", keep)
					}
				}
			}
			policy := `"operatorNote": "keep", /* options comment */ `
			content = bytes.Replace(content, []byte(`"binary":`), []byte(policy+`"binary":`), 1)
			again, previous, err := openCodeV2Content(content, base, digest, "/fixture/binary", "/fixture/db", "opencode-v2:realm")
			if err != nil || !bytes.Equal(content, again) || previous != filepath.Join(base, digest) {
				t.Fatal("non-idempotent owned entry", previous, err)
			}
			nextDigest := strings.Repeat("b", 64)
			upgraded, previous, err := openCodeV2Content(content, base, nextDigest, "/fixture/binary", "/fixture/db", "opencode-v2:realm")
			if err != nil || previous != filepath.Join(base, digest) || !bytes.Contains(upgraded, []byte(policy)) || bytes.Contains(upgraded, []byte(digest)) {
				t.Fatal("owned URL upgrade lost options", previous, err)
			}
		})
	}
}

func TestOpenCodeV2ContentRefusesConflicts(t *testing.T) {
	base := filepath.Join(t.TempDir(), "tools-programmer-integration-v2")
	digest := strings.Repeat("a", 64)
	plugin := (&url.URL{Scheme: "file", Path: filepath.Join(base, digest, "opencode-v2", "plugin.ts")}).String()
	entry, _ := json.Marshal(map[string]any{"package": plugin, "options": map[string]string{"binary": "/fixture/binary", "database": "/fixture/db", "runtimeId": "opencode-v2:realm"}})
	cases := []struct {
		name, source, binary, database, runtime, digest string
	}{
		{"legacy active", `{"plugin":["legacy"]}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", digest},
		{"duplicate key", `{"plugins":[],"plugins":[]}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", digest},
		{"bad plugins", `{"plugins":null}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", digest},
		{"bad entry", `{"plugins":[null]}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", digest},
		{"bad options", `{"plugins":[{"package":"other","options":null}]}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", digest},
		{"duplicate owner", `{"plugins":[` + string(entry) + `,` + string(entry) + `]}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", digest},
		{"bare owned path", `{"plugins":["` + plugin + `"]}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", digest},
		{"changed binary", `{"plugins":[` + string(entry) + `]}`, "/other", "/fixture/db", "opencode-v2:realm", digest},
		{"changed database", `{"plugins":[` + string(entry) + `]}`, "/fixture/binary", "/other", "opencode-v2:realm", digest},
		{"changed realm", `{"plugins":[` + string(entry) + `]}`, "/fixture/binary", "/fixture/db", "opencode-v2:other", digest},
		{"relative binary", `{}`, "relative", "/fixture/db", "opencode-v2:realm", digest},
		{"interpolated database", `{}`, "/fixture/binary", "/{env:DB}", "opencode-v2:realm", digest},
		{"invalid realm", `{}`, "/fixture/binary", "/fixture/db", "opencode-v1:realm", digest},
		{"empty realm suffix", `{}`, "/fixture/binary", "/fixture/db", "opencode-v2:", digest},
		{"overlong realm", `{}`, "/fixture/binary", "/fixture/db", "opencode-v2:" + strings.Repeat("a", 200), digest},
		{"invalid digest", `{}`, "/fixture/binary", "/fixture/db", "opencode-v2:realm", "../outside"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			source := []byte(tt.source)
			before := append([]byte(nil), source...)
			content, _, err := openCodeV2Content(source, base, tt.digest, tt.binary, tt.database, tt.runtime)
			if err == nil || content != nil || !bytes.Equal(source, before) || strings.Contains(err.Error(), "keep") {
				t.Fatal("unsafe config accepted or input changed", err)
			}
		})
	}
}

func TestOpenCodeV2OwnedReferences(t *testing.T) {
	base := filepath.Join(t.TempDir(), "config with spaces", "tools-programmer-integration-v2")
	digest := strings.Repeat("a", 64)
	owned := filepath.Join(base, digest, "opencode-v2", "plugin.ts")
	file := (&url.URL{Scheme: "file", Path: owned}).String()
	for _, reference := range []string{file, owned, "./tools-programmer-integration-v2/" + digest + "/opencode-v2/plugin.ts"} {
		previous, err := openCodeV2OwnedReference(reference, base)
		if err != nil || previous != filepath.Join(base, digest) {
			t.Fatal("owned reference not recognized", reference, previous, err)
		}
	}
	for _, reference := range []string{file + "?", file + "#", file + "?x=1", strings.Replace(file, "file://", "file://remote", 1), base, filepath.Join(base, digest, "wrong.ts")} {
		if _, err := openCodeV2OwnedReference(reference, base); err == nil {
			t.Fatal("ambiguous owned reference accepted", reference)
		}
	}
	if previous, err := openCodeV2OwnedReference("other-plugin", base); err != nil || previous != "" {
		t.Fatal("unrelated package claimed", previous, err)
	}
}
