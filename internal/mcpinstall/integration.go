package mcpinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tailscale/hujson"
	bundle "tools-programmer"
	"tools-programmer/internal/core"
)

type IntegrationResult struct {
	Target        string   `json:"target"`
	Destination   string   `json:"destination"`
	Plugin        string   `json:"plugin"`
	Digest        string   `json:"digest"`
	Updated       []string `json:"updated"`
	ConfigChanged bool     `json:"config_changed"`
	Next          string   `json:"next"`
}

// SyncIntegration publishes an immutable bundle before atomically pointing the
// selected JSONC config at it. It neither opens state nor installs dependencies.
func SyncIntegration(database string) (IntegrationResult, error) {
	binary, err := os.Executable()
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err != nil {
		return IntegrationResult{}, core.Fail("integration_unavailable", "cannot resolve executable")
	}
	assets, manifest, err := bundle.IntegrationAssets()
	if err != nil {
		return IntegrationResult{}, core.Fail("integration_unavailable", "embedded bundle unavailable")
	}
	return syncIntegration(database, binary, assets, manifest)
}

func syncIntegration(database, binary string, assets map[string][]byte, manifest bundle.IntegrationManifest) (IntegrationResult, error) {
	r := IntegrationResult{Target: "opencode", Digest: manifest.Digest, Updated: []string{},
		Next: "Quit and restart OpenCode after verifying its existing plugin dependency. No dependencies, permissions, consent policy, MCP server or global AGENTS were installed. Use tools_programmer_diagnostics after restart."}
	for _, path := range []string{database, binary} {
		if !filepath.IsAbs(path) || strings.Contains(path, "{env:") || strings.Contains(path, "{file:") {
			return r, core.Fail("invalid_input", "absolute binary/database paths without interpolation are required")
		}
	}
	dir, err := configDirectory()
	if err != nil {
		return r, err
	}
	r.Destination = filepath.Join(dir, "opencode.jsonc")
	if err := checkPath(dir, true, false); err != nil {
		return r, err
	}
	for _, name := range []string{"opencode.jsonc", "opencode.json", "config.json"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err == nil {
			if name == "config.json" {
				return r, core.Fail("config_manual", "legacy config.json requires manual configuration")
			}
			r.Destination = path
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return r, core.Fail("config_io", "cannot inspect configuration")
		}
	}
	original, err := integrationRead(r.Destination, true)
	if err != nil {
		return r, err
	}
	source := original
	if source == nil {
		source = []byte("{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n")
	}
	base := filepath.Join(dir, "tools-programmer-integration")
	target := filepath.Join(base, manifest.Digest)
	r.Plugin = (&url.URL{Scheme: "file", Path: filepath.Join(target, "plugin.ts")}).String()
	content, old, err := integrationConfig(source, base, r.Plugin, binary, database)
	if err != nil {
		return r, err
	}
	if old != "" {
		if err := verifyIntegration(old); err != nil {
			return r, err
		}
	}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	owned := make(map[string][]byte, len(assets)+1)
	for name, b := range assets {
		owned[name] = b
	}
	owned["manifest.json"] = append(manifestBytes, '\n')
	names := make([]string, 0, len(owned))
	for name := range owned {
		names = append(names, name)
	}
	sort.Strings(names)
	// Preflight every owned destination before creating anything. Unknown files
	// are preserved; modified owned files are never overwritten, even on upgrades.
	before := map[string][]byte{}
	for _, name := range names {
		b, err := integrationRead(filepath.Join(target, name), true)
		if err != nil {
			return r, err
		}
		if b != nil && !bytes.Equal(b, owned[name]) {
			return r, integrationDrift()
		}
		before[name] = b
	}
	for _, name := range names {
		if before[name] != nil {
			continue
		}
		if err := replace(filepath.Join(target, name), nil, owned[name]); err != nil {
			return r, err
		}
		r.Updated = append(r.Updated, name)
	}
	if err := verifyIntegration(target); err != nil {
		return r, err
	}
	if !bytes.Equal(content, original) {
		if err := replace(r.Destination, original, content); err != nil {
			return r, err
		}
		r.ConfigChanged = true
	}
	return r, nil
}

func integrationDrift() error {
	return core.Fail("integration_drift", "owned bundle is missing, modified or unsupported; preserve customizations and resolve explicitly before sync")
}

func integrationRead(path string, missing bool) ([]byte, error) {
	if err := checkPath(path, false, false); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && missing {
		return nil, nil
	}
	if err != nil || info.Size() > 1<<20 {
		return nil, core.Fail("integration_io", "owned file unavailable or exceeds size bound")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, core.Fail("integration_io", "cannot read owned file")
	}
	return b, nil
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var assetPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,100}$`)

func verifyIntegration(dir string) error {
	b, err := integrationRead(filepath.Join(dir, "manifest.json"), false)
	if err != nil {
		return err
	}
	var m bundle.IntegrationManifest
	if !uniqueJSON(b) || json.Unmarshal(b, &m) != nil || m.Protocol != 1 || m.Digest != filepath.Base(dir) || !digestPattern.MatchString(m.Digest) || len(m.Files) == 0 || len(m.Files) > 64 {
		return integrationDrift()
	}
	b, _ = json.Marshal(m.Files)
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != m.Digest {
		return integrationDrift()
	}
	for name, want := range m.Files {
		if !assetPattern.MatchString(name) || !digestPattern.MatchString(want) {
			return integrationDrift()
		}
		b, err := integrationRead(filepath.Join(dir, name), false)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != want {
			return integrationDrift()
		}
	}
	return nil
}

// integrationConfig makes a surgical insertion or replaces only the owned URL
// literal. Existing tuple options (including explicit policy) remain byte-exact.
func integrationConfig(source []byte, base, plugin, binary, database string) ([]byte, string, error) {
	doc, err := parseConfig(source)
	if err != nil {
		return nil, "", err
	}
	object := doc.Value.(*hujson.Object)
	var container *hujson.Value
	for i := range object.Members {
		if object.Members[i].Name.Value.(hujson.Literal).String() == "plugin" {
			container = &object.Members[i].Value
		}
	}
	entry, _ := json.Marshal([]any{plugin, map[string]string{"binary": binary, "database": database}})
	at, prefix := doc.StartOffset+1, ""
	addition := []byte{}
	old := ""
	if container == nil {
		if len(object.Members) > 0 {
			at, prefix = object.Members[len(object.Members)-1].Value.EndOffset, ","
		}
		addition = append([]byte(prefix+"\n  \"plugin\": ["), entry...)
		addition = append(addition, ']')
	} else {
		array, ok := container.Value.(*hujson.Array)
		if !ok {
			return nil, "", core.Fail("config_invalid", "plugin configuration must be an array")
		}
		var replacement *hujson.Value
		for i := range array.Elements {
			value := &array.Elements[i]
			literal := value
			tuple, tupleOK := value.Value.(*hujson.Array)
			if tupleOK {
				if len(tuple.Elements) != 2 {
					return nil, "", core.Fail("config_invalid", "plugin tuples require a path and options object")
				}
				if _, ok := tuple.Elements[1].Value.(*hujson.Object); !ok {
					return nil, "", core.Fail("config_invalid", "plugin options must be an object")
				}
				literal = &tuple.Elements[0]
			}
			copy := literal.Clone()
			copy.Standardize()
			var name string
			if json.Unmarshal(copy.Pack(), &name) != nil || name == "" {
				return nil, "", core.Fail("config_invalid", "plugin names must be nonempty strings")
			}
			u, err := url.Parse(name)
			if err != nil {
				return nil, "", core.Fail("config_invalid", "invalid plugin reference")
			}
			path := u.Path
			if u.Scheme == "" && (filepath.IsAbs(name) || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "tools-programmer-integration/")) {
				path = name
				if !filepath.IsAbs(path) {
					path = filepath.Join(filepath.Dir(base), path)
				}
			} else if u.Scheme != "file" || u.Host != "" {
				continue
			}
			if !strings.HasPrefix(filepath.Clean(path), base+string(filepath.Separator)) {
				continue
			}
			candidate := filepath.Dir(path)
			if old != "" || filepath.Dir(candidate) != base || filepath.Base(path) != "plugin.ts" || !digestPattern.MatchString(filepath.Base(candidate)) || u.RawQuery != "" || u.Fragment != "" || !tupleOK {
				return nil, "", integrationDrift()
			}
			options := tuple.Elements[1].Clone()
			options.Standardize()
			var fields map[string]json.RawMessage
			if json.Unmarshal(options.Pack(), &fields) != nil {
				return nil, "", integrationDrift()
			}
			var b, d string
			json.Unmarshal(fields["binary"], &b)
			json.Unmarshal(fields["database"], &d)
			if b != binary || d != database {
				return nil, "", core.Fail("config_conflict", "owned plugin binary/database options differ; preserve configuration and resolve explicitly")
			}
			old, replacement = candidate, literal
		}
		if replacement != nil {
			b, _ := json.Marshal(plugin)
			if bytes.Equal(source[replacement.StartOffset:replacement.EndOffset], b) {
				return source, old, nil
			}
			out := append(append(append([]byte{}, source[:replacement.StartOffset]...), b...), source[replacement.EndOffset:]...)
			if _, err := parseConfig(out); err != nil {
				return nil, "", err
			}
			return out, old, nil
		}
		at = container.StartOffset + 1
		if len(array.Elements) > 0 {
			at, prefix = array.Elements[len(array.Elements)-1].EndOffset, ","
		}
		addition = append([]byte(prefix+"\n    "), entry...)
	}
	out := append(append(append([]byte{}, source[:at]...), addition...), source[at:]...)
	if _, err := parseConfig(out); err != nil {
		return nil, "", err
	}
	return out, old, nil
}
