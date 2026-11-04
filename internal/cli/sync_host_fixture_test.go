package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func fakeSyncHost(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable fixtures require a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertSyncHomeEmpty(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected setup wrote to HOME: %v (%v)", entries, err)
	}
}
