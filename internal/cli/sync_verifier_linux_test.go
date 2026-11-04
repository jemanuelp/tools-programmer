//go:build linux

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestVerifierExplicitEmptyHostRejects(t *testing.T) {
	for _, args := range [][]string{{"sync", "opencode", "--opencode-bin="}, {"sync", "opencode", "--opencode-bin", ""}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			home := isolatedSyncHome(t)
			r, code, output := runSyncCommand(t, args...)
			if code != 1 || r.Error == nil || r.Error.Code != "invalid_input" {
				t.Fatalf("explicit empty host accepted: %d %s", code, output)
			}
			assertSyncHomeEmpty(t, home)
		})
	}
}

func TestVerifierExitedWrapperCleansDescendant(t *testing.T) {
	home := isolatedSyncHome(t)
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	host := fakeSyncHost(t, fmt.Sprintf("/bin/sleep 20 &\necho $! > %q\necho 1.18.30\nexit 0", pidFile))
	start := time.Now()
	r, code, output := runSyncCommand(t, "sync", "opencode", "--opencode-bin", host)
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGKILL)
	if time.Since(start) > 4*time.Second {
		t.Fatal("probe exceeded bound")
	}
	if code != 1 || r.Error == nil || r.Error.Code != "host_probe" {
		t.Fatalf("unexpected result: %d %s", code, output)
	}
	assertSyncHomeEmpty(t, home)
	deadline := time.Now().Add(time.Second)
	for {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		end := strings.LastIndex(string(stat), ")")
		if end >= 0 && strings.HasPrefix(string(stat[end+1:]), " Z ") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe descendant remains alive: %d", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
