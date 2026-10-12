package mcpinstall

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"

	"tools-programmer/internal/core"
)

type openCodeV2Config struct {
	Root        string
	Destination string
}

// resolveOpenCodeV2Config selects and validates a V2 config location without
// creating directories or changing configuration. The explicit root is for a
// future CLI option; OPENCODE_CONFIG_DIR is reserved for the launched V2 host.
func resolveOpenCodeV2Config(target string) (openCodeV2Config, error) {
	var result openCodeV2Config
	base, err := configBase()
	if err != nil {
		return result, err
	}
	if target == "" {
		target = filepath.Join(base, "opencode-v2")
	} else if !filepath.IsAbs(target) {
		return result, core.Fail("invalid_input", "V2 configuration root must be absolute")
	}
	result.Root = filepath.Clean(target)

	// Use the unchanged V1 resolver, and also protect its ordinary fallback when
	// OPENCODE_CONFIG_DIR points elsewhere.
	activeV1, err := configDirectory()
	if err != nil {
		return result, err
	}
	defaultV1 := filepath.Join(base, "opencode")
	for _, root := range []string{activeV1, defaultV1} {
		if err := checkPath(root, true, false); err != nil {
			return result, err
		}
		if pathsOverlap(result.Root, root) {
			return result, core.Fail("config_conflict", "V2 configuration root overlaps the active or default V1 root; choose a distinct root; no files were changed")
		}
	}
	if err := checkPath(result.Root, true, false); err != nil {
		return result, err
	}

	destination, source, err := openCodeV2ConfigFile(result.Root)
	if err != nil {
		return result, err
	}
	result.Destination = destination
	if source != nil {
		if err := validateOpenCodeV2Config(source); err != nil {
			return result, err
		}
	}
	return result, nil
}

func configBase() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || !filepath.IsAbs(home) {
			return "", core.Fail("invalid_input", "HOME must be an absolute directory")
		}
		base = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(base) {
		return "", core.Fail("invalid_input", "XDG_CONFIG_HOME must be absolute")
	}
	return filepath.Clean(base), nil
}

func pathsOverlap(left, right string) bool {
	return containsPath(left, right) || containsPath(right, left)
}

func containsPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	return relative == "." || (relative != ".." && !filepath.IsAbs(relative) && !startsWithParent(relative))
}

func startsWithParent(path string) bool {
	return path == ".." || len(path) > 3 && path[:3] == ".."+string(filepath.Separator)
}

func openCodeV2ConfigFile(root string) (string, []byte, error) {
	for _, name := range []string{"opencode.jsonc", "opencode.json"} {
		path := filepath.Join(root, name)
		if _, err := os.Lstat(path); err == nil {
			source, err := integrationRead(path, false)
			if err != nil {
				return "", nil, err
			}
			if source == nil {
				source = []byte{}
			}
			return path, source, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", nil, core.Fail("config_io", "cannot inspect V2 configuration")
		}
	}

	legacy := filepath.Join(root, "config.json")
	if _, err := os.Lstat(legacy); err == nil {
		if err := checkPath(legacy, false, false); err != nil {
			return "", nil, err
		}
		return "", nil, core.Fail("config_manual", "legacy config.json requires manual resolution before writing native V2 configuration; no file was changed")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, core.Fail("config_io", "cannot inspect V2 configuration")
	}
	return filepath.Join(root, "opencode.jsonc"), nil, nil
}

func validateOpenCodeV2Config(source []byte) error {
	document, err := parseConfig(source)
	if err != nil {
		return err
	}
	root := document.Value.(*hujson.Object)
	plugins := configMember(root, "plugins")
	if plugins != nil {
		if _, ok := plugins.Value.(*hujson.Array); !ok {
			return core.Fail("config_invalid", "native plugins configuration must be an array; no file was changed")
		}
	}
	legacyPlugins := configMember(root, "plugin")
	if legacyPlugins != nil {
		entries, ok := legacyPlugins.Value.(*hujson.Array)
		if !ok {
			return core.Fail("config_invalid", "legacy plugin configuration must be an array; no file was changed")
		}
		if len(entries.Elements) > 0 {
			return core.Fail("config_manual", "active legacy plugin entries would be masked by native plugins; resolve them manually before V2 setup; no file was changed")
		}
	}
	if mcp := configMember(root, "mcp"); mcp != nil {
		container, ok := mcp.Value.(*hujson.Object)
		if !ok {
			return core.Fail("config_invalid", "MCP configuration must be an object; no file was changed")
		}
		if servers := configMember(container, "servers"); servers != nil {
			if _, ok := servers.Value.(*hujson.Object); !ok {
				return core.Fail("config_invalid", "native mcp.servers configuration must be an object; no file was changed")
			}
		}
	}
	return nil
}

func configMember(object *hujson.Object, name string) *hujson.Value {
	for i := range object.Members {
		member := &object.Members[i]
		if member.Name.Value.(hujson.Literal).String() == name {
			return &member.Value
		}
	}
	return nil
}
