package skillsync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreflightDoesNotCreateOrReplaceGuidance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := Preflight(); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("preflight created files: %v (%v)", entries, err)
	}
	unsafe := filepath.Join(home, ".config", "opencode", "skills", "tools-programmer", "SKILL.md")
	if err := os.MkdirAll(unsafe, 0700); err != nil {
		t.Fatal(err)
	}
	if err := Preflight(); err == nil {
		t.Fatal("directory used as guidance was accepted")
	}
	if info, err := os.Stat(unsafe); err != nil || !info.IsDir() {
		t.Fatalf("preflight replaced unsafe destination: %v", err)
	}
}
