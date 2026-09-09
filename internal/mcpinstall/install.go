// Package mcpinstall registers a stdio server through explicit agent adapters.
package mcpinstall

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/tailscale/hujson"

	"tools-programmer/internal/core"
)

type Result struct {
	Target      string   `json:"target"`
	Destination string   `json:"destination,omitempty"`
	Command     []string `json:"command,omitempty"`
	Changed     bool     `json:"changed"`
	Next        string   `json:"next"`
}

type adapter interface {
	install(Result) (Result, error)
}

var adapters = map[string]adapter{"opencode": openCode{}}

func Supports(target string) bool { _, ok := adapters[target]; return ok }

// Install never opens the database, launches an agent or installs the executable.
func Install(target, database string) (Result, error) {
	r := Result{Target: target, Next: "Quit and restart OpenCode. Installation does not register projects or enforce tool use."}
	a, ok := adapters[target]
	if !ok {
		return r, core.Fail("unsupported_target", "only opencode installation is supported; configure other agents manually with the stdio command")
	}
	binary, err := os.Executable()
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err != nil || !filepath.IsAbs(binary) || !filepath.IsAbs(database) {
		return r, core.Fail("invalid_input", "installation requires resolved absolute executable and database paths")
	}
	for _, path := range []string{binary, database} {
		if strings.Contains(path, "{env:") || strings.Contains(path, "{file:") {
			return r, core.Fail("invalid_input", "paths containing OpenCode interpolation expressions are unsupported")
		}
	}
	r.Command = []string{binary, "--db", database, "mcp", "serve"}
	return a.install(r)
}

type openCode struct{}

func configDirectory() (string, error) {
	if dir := os.Getenv("OPENCODE_CONFIG_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return "", core.Fail("invalid_input", "OPENCODE_CONFIG_DIR must be absolute")
		}
		return filepath.Clean(dir), nil
	}
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
	return filepath.Join(base, "opencode"), nil
}

func (openCode) install(r Result) (Result, error) {
	dir, err := configDirectory()
	if err != nil {
		return r, err
	}
	r.Destination = filepath.Join(dir, "opencode.jsonc")
	if err := checkPath(dir, true, false); err != nil {
		return r, err
	}
	// Follow OpenCode's globalConfigFile priority; never inspect or migrate
	// lower-priority files after selecting an existing configuration.
	for _, name := range []string{"opencode.jsonc", "opencode.json", "config.json"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err == nil {
			if name == "config.json" {
				return r, core.Fail("config_manual", "legacy config.json requires manual configuration; no file was changed")
			}
			r.Destination = path
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return r, core.Fail("config_io", "cannot inspect OpenCode configuration")
		}
	}
	if err := checkPath(r.Destination, false, false); err != nil {
		return r, err
	}
	original, err := os.ReadFile(r.Destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return r, core.Fail("config_io", "cannot read OpenCode configuration")
	}
	source := original
	if source == nil {
		source = []byte("{\n}\n")
	}
	document, err := parseConfig(source)
	if err != nil {
		return r, err
	}
	entry, _ := json.Marshal(struct {
		Type    string   `json:"type"`
		Command []string `json:"command"`
		Enabled bool     `json:"enabled"`
	}{"local", r.Command, true})
	object := document.Value.(*hujson.Object)
	container := document
	key, value := "mcp", append(append([]byte(`{"tools-programmer":`), entry...), '}')
	for _, member := range object.Members {
		if member.Name.Value.(hujson.Literal).String() == "mcp" {
			var ok bool
			object, ok = member.Value.Value.(*hujson.Object)
			if !ok {
				return r, core.Fail("config_invalid", "existing mcp configuration must be an object; no file was changed")
			}
			container, key, value = member.Value, "tools-programmer", entry
			break
		}
	}
	for _, member := range object.Members {
		if key != "tools-programmer" || member.Name.Value.(hujson.Literal).String() != key {
			continue
		}
		existing := member.Value.Clone()
		existing.Standardize()
		var a, b any
		json.Unmarshal(existing.Pack(), &a)
		json.Unmarshal(entry, &b)
		if reflect.DeepEqual(a, b) {
			return r, nil
		}
		return r, core.Fail("config_conflict", "mcp.tools-programmer already differs; preserve custom arguments/environment and update manually using the returned command; no file was changed")
	}
	// Insert before existing suffix trivia and any trailing comma. Every original
	// byte remains unchanged, including permission ordering and numeric literals.
	at, prefix := container.StartOffset+1, ""
	if len(object.Members) > 0 {
		at, prefix = object.Members[len(object.Members)-1].Value.EndOffset, ","
	}
	addition := append([]byte(prefix+"\n  \""+key+"\": "), value...)
	content := append([]byte(nil), source[:at]...)
	content = append(content, addition...)
	content = append(content, source[at:]...)
	if _, err := parseConfig(content); err != nil {
		return r, err
	}
	if err := replace(r.Destination, original, content); err != nil {
		return r, err
	}
	r.Changed = true
	return r, nil
}

// Standardization is validation-only; the original source supplies output bytes.
func parseConfig(source []byte) (hujson.Value, error) {
	// HuJSON requires a newline to terminate line comments, whereas JSONC also
	// permits EOF. A parsing-only sentinel preserves all original source offsets.
	document, err := hujson.Parse(append(append([]byte(nil), source...), '\n'))
	if err == nil {
		standard := document.Clone()
		standard.Standardize()
		_, object := document.Value.(*hujson.Object)
		if object && uniqueJSON(standard.Pack()) {
			return document, nil
		}
	}
	return hujson.Value{}, core.Fail("config_invalid", "configuration must be a JSON/JSONC object without duplicate keys; no file was changed")
}

// uniqueJSON refuses duplicate object keys at any depth instead of losing them
// through encoding/json's last-key-wins decoding. It also rejects trailing data.
func uniqueJSON(data []byte) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func() bool
	value = func() bool {
		t, err := d.Token()
		if err != nil {
			return false
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return true
		}
		keys := map[string]bool{}
		for d.More() {
			if delim == '{' {
				t, err := d.Token()
				key, ok := t.(string)
				if err != nil || !ok || keys[key] {
					return false
				}
				keys[key] = true
			}
			if !value() {
				return false
			}
		}
		_, err = d.Token()
		return err == nil
	}
	if !value() {
		return false
	}
	_, err := d.Token()
	return errors.Is(err, io.EOF)
}

// Trusted local directories without concurrent external tree edits are required.
// Existing symlinks, including dangling ones, are refused at every component.
func checkPath(path string, directory, create bool) error {
	parent := filepath.Dir(path)
	if parent != path {
		if err := checkPath(parent, true, create); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil
		}
		if err = os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return core.Fail("config_io", "cannot create configuration directory")
		}
		info, err = os.Lstat(path)
	}
	if err != nil || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return core.Fail("config_path", "configuration path must contain only real directories and regular files, without symlinks")
	}
	return nil
}

func replace(path string, original, content []byte) error {
	if err := checkPath(filepath.Dir(path), true, true); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return core.Fail("config_io", "cannot open configuration directory")
	}
	defer root.Close()
	name := filepath.Base(path)
	mode := os.FileMode(0600)
	if info, err := root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return core.Fail("config_path", "configuration file is not regular")
		}
		mode = info.Mode().Perm()
		current, err := root.ReadFile(name)
		if err != nil || !bytes.Equal(current, original) {
			return core.Fail("config_conflict", "configuration changed during installation; retry after other editors finish")
		}
	} else if !errors.Is(err, os.ErrNotExist) || original != nil {
		return core.Fail("config_conflict", "configuration changed or became inaccessible during installation")
	}
	tmp := ".tools-programmer-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return core.Fail("config_io", "cannot create temporary configuration file")
	}
	defer root.Remove(tmp)
	_, writeErr := f.Write(content)
	modeErr := f.Chmod(mode)
	syncErr := f.Sync()
	closeErr := f.Close()
	if errors.Join(writeErr, modeErr, syncErr, closeErr) != nil {
		return core.Fail("config_io", "cannot write temporary configuration; original file was preserved")
	}
	if err := root.Rename(tmp, name); err != nil {
		return core.Fail("config_io", "cannot replace configuration; original file was preserved")
	}
	return nil
}
