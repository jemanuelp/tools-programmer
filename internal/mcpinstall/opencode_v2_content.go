package mcpinstall

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tailscale/hujson"
	"tools-programmer/internal/core"
)

var openCodeV2RuntimeID = regexp.MustCompile(`^opencode-v2:[A-Za-z0-9_.:/-]+$`)

// openCodeV2Content proposes a surgical edit in memory, never a filesystem write.
// Existing owned options must agree; additional operator options stay byte-exact.
// Returning an old bundle path is not proof that its on-disk assets are intact.
func openCodeV2Content(source []byte, base, digest, binary, database, runtimeID string) ([]byte, string, error) {
	for _, path := range []string{base, binary, database} {
		if !filepath.IsAbs(path) || strings.Contains(path, "{env:") || strings.Contains(path, "{file:") || strings.ContainsRune(path, 0) {
			return nil, "", core.Fail("invalid_input", "absolute paths without interpolation are required")
		}
	}
	if len(runtimeID) > 200 || !openCodeV2RuntimeID.MatchString(runtimeID) || !digestPattern.MatchString(digest) {
		return nil, "", core.Fail("invalid_input", "a stable opencode-v2 runtime ID and valid bundle digest are required")
	}
	if len(source) == 0 {
		source = []byte("{\n  \"$schema\": \"https://opencode.ai/config.json\"\n}\n")
	}
	if len(source) > 1<<20 {
		return nil, "", core.Fail("config_invalid", "configuration exceeds size bound")
	}
	if err := validateOpenCodeV2Config(source); err != nil {
		return nil, "", err
	}
	doc, err := parseConfig(source)
	if err != nil {
		return nil, "", err
	}
	root := doc.Value.(*hujson.Object)
	container := configMember(root, "plugins")
	plugin := (&url.URL{Scheme: "file", Path: filepath.Join(base, digest, "opencode-v2", "plugin.ts")}).String()
	entry, _ := json.Marshal(map[string]any{"package": plugin, "options": map[string]string{"binary": binary, "database": database, "runtimeId": runtimeID}})
	at, prefix := doc.StartOffset+1, ""
	var addition []byte
	old := ""
	if container == nil {
		if len(root.Members) > 0 {
			at, prefix = root.Members[len(root.Members)-1].Value.EndOffset, ","
		}
		addition = append([]byte(prefix+"\n  \"plugins\": ["), entry...)
		addition = append(addition, ']')
	} else {
		array := container.Value.(*hujson.Array)
		var replacement *hujson.Value
		for i := range array.Elements {
			value := &array.Elements[i]
			literal := value
			var options *hujson.Value
			if object, ok := value.Value.(*hujson.Object); ok {
				literal, options = configMember(object, "package"), configMember(object, "options")
				if options != nil {
					if _, ok := options.Value.(*hujson.Object); !ok {
						return nil, "", core.Fail("config_invalid", "plugin options must be an object")
					}
				}
			}
			if literal == nil {
				return nil, "", core.Fail("config_invalid", "plugin package is required")
			}
			copy := literal.Clone()
			copy.Standardize()
			var name string
			if json.Unmarshal(copy.Pack(), &name) != nil || name == "" {
				return nil, "", core.Fail("config_invalid", "plugin package must be a nonempty string")
			}
			candidate, err := openCodeV2OwnedReference(name, base)
			if err != nil {
				return nil, "", err
			}
			if candidate == "" {
				continue
			}
			if old != "" || options == nil {
				return nil, "", integrationDrift()
			}
			fields := options.Clone()
			fields.Standardize()
			var configured map[string]json.RawMessage
			if json.Unmarshal(fields.Pack(), &configured) != nil {
				return nil, "", integrationDrift()
			}
			for key, want := range map[string]string{"binary": binary, "database": database, "runtimeId": runtimeID} {
				var actual string
				if json.Unmarshal(configured[key], &actual) != nil || actual != want {
					return nil, "", core.Fail("config_conflict", "owned V2 plugin options differ; resolve explicitly without overwriting")
				}
			}
			old = candidate
			replacement = literal
		}
		if replacement != nil {
			encoded, _ := json.Marshal(plugin)
			out := append(append(append([]byte{}, source[:replacement.StartOffset]...), encoded...), source[replacement.EndOffset:]...)
			if len(out) > 1<<20 {
				return nil, "", core.Fail("config_invalid", "proposed configuration exceeds size bound")
			}
			if bytes.Equal(out, source) {
				return source, old, nil
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
	if len(out) > 1<<20 {
		return nil, "", core.Fail("config_invalid", "proposed configuration exceeds size bound")
	}
	if _, err := parseConfig(out); err != nil {
		return nil, "", err
	}
	return out, old, nil
}

func openCodeV2OwnedReference(name, base string) (string, error) {
	u, err := url.Parse(name)
	if err != nil {
		return "", core.Fail("config_invalid", "invalid plugin reference")
	}
	path := u.Path
	if u.Scheme == "" {
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, ".") && !strings.HasPrefix(path, "tools-programmer-integration-v2/") {
			return "", nil
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(base), path)
		}
	} else if u.Scheme != "file" {
		return "", nil
	}
	path = filepath.Clean(path)
	if !containsPath(base, path) {
		return "", nil
	}
	candidate := filepath.Dir(filepath.Dir(path))
	if u.User != nil || u.Host != "" || strings.ContainsAny(name, "?#") || filepath.Dir(candidate) != base || !digestPattern.MatchString(filepath.Base(candidate)) || filepath.Base(filepath.Dir(path)) != "opencode-v2" || filepath.Base(path) != "plugin.ts" {
		return "", integrationDrift()
	}
	return candidate, nil
}
