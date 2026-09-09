// Package skillsync installs only the explicitly owned, bundled skill files.
package skillsync

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	bundle "tools-programmer"
)

var files = []string{"SKILL.md", "references/cli.md"}

type Result struct {
	Target      string   `json:"target"`
	Destination string   `json:"destination"`
	Updated     []string `json:"updated"`
	Unchanged   []string `json:"unchanged"`
}

// Sync replaces local edits to the two owned files and preserves everything else.
// Each rename is atomic; the set of files is not a transaction. Callers must use
// a trusted directory tree without concurrent external changes during sync.
func Sync() (Result, error) {
	r := Result{Target: "opencode", Updated: []string{}, Unchanged: []string{}}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return r, fmt.Errorf("HOME must be an absolute directory")
	}
	r.Destination = filepath.Join(home, ".config", "opencode", "skills", "tools-programmer")
	// Check all existing components and owned files before creating anything.
	for _, name := range files {
		if err := checkPath(filepath.Join(r.Destination, filepath.FromSlash(name)), false, false); err != nil {
			return r, err
		}
	}
	for _, name := range files {
		path := filepath.Join(r.Destination, filepath.FromSlash(name))
		if err := checkPath(filepath.Dir(path), true, true); err != nil {
			return r, err
		}
		content, err := bundle.SkillFiles.ReadFile(".opencode/skills/tools-programmer/" + name)
		if err != nil {
			return r, err
		}
		changed, err := replace(path, content)
		if err != nil {
			return r, err
		}
		if changed {
			r.Updated = append(r.Updated, name)
		} else {
			r.Unchanged = append(r.Unchanged, name)
		}
	}
	return r, nil
}

// checkPath rejects symlinks (including dangling ones) at every component. In
// directory mode it creates missing directories one at a time, then checks them.
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
		if err = os.Mkdir(path, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	// Ancestors must be directories even when the leaf is a regular file.
	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("unsafe file type at %s", path)
	}
	return nil
}

func replace(path string, content []byte) (bool, error) {
	// OpenRoot pins the parent directory for reads, temporary creation and rename.
	// The component checks refuse existing links; concurrent tree edits are unsupported.
	if err := checkPath(filepath.Dir(path), true, true); err != nil {
		return false, err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("owned file is not regular: %s", path)
		}
		// Bound reads even if a local edit is unexpectedly large.
		if info.Size() == int64(len(content)) {
			current, err := root.ReadFile(name)
			if err != nil {
				return false, err
			}
			if bytes.Equal(current, content) {
				return false, nil
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return false, err
	}
	tmp := ".tools-programmer-" + hex.EncodeToString(token[:])
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return false, err
	}
	defer root.Remove(tmp)
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return false, err
	}
	if err := root.Rename(tmp, name); err != nil {
		return false, err
	}
	return true, nil
}
