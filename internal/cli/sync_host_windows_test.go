package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsSyncProbeContainsAndCleansDescendants(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a native fake host and exercises Windows Job Objects")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "fake_host.go")
	if err := os.WriteFile(source, []byte(windowsSyncProbeFixture), 0600); err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(dir, "fake_host.exe")
	build := exec.Command("go", "build", "-o", host, source)
	build.Env = append(os.Environ(), "GOPROXY=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake Windows host: %s (%v)", output, err)
	}
	for _, mode := range []string{"success", "exited wrapper", "timeout", "overflow", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			t.Setenv("USERPROFILE", home)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
			t.Setenv("OPENCODE_CONFIG_DIR", "")
			t.Setenv("TOOLS_PROGRAMMER_DB", "")
			t.Setenv("TOOLS_PROGRAMMER_TEST_WINDOWS_HOST_MODE", mode)
			t.Setenv("TOOLS_PROGRAMMER_TEST_WINDOWS_PID_FILE", pidFile)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelDone := make(chan struct{})
			if mode == "canceled" {
				go func() {
					defer close(cancelDone)
					deadline := time.NewTimer(2 * time.Second)
					defer deadline.Stop()
					tick := time.NewTicker(5 * time.Millisecond)
					defer tick.Stop()
					for {
						if _, err := os.Stat(pidFile); err == nil {
							cancel()
							return
						}
						select {
						case <-ctx.Done():
							return
						case <-deadline.C:
							cancel()
							return
						case <-tick.C:
						}
					}
				}()
			} else {
				close(cancelDone)
			}
			var output bytes.Buffer
			start := time.Now()
			code := Run(ctx, []string{"sync", "opencode", "--opencode-bin", host}, &output, forbiddenRunner{t})
			cancel()
			<-cancelDone
			if time.Since(start) > 4*time.Second {
				t.Fatalf("probe exceeded deadline: %s", time.Since(start))
			}
			var response syncResponse
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatalf("invalid public CLI response: %s (%v)", output.String(), err)
			}
			if mode == "success" {
				if code != 0 || response.Error != nil || !response.Data.MCP.Changed {
					t.Fatalf("bounded host failed: %d %s", code, output.String())
				}
			} else {
				if code != 1 || response.Error == nil || response.Error.Code != "host_probe" {
					t.Fatalf("unsafe host accepted: %d %s", code, output.String())
				}
				assertSyncHomeEmpty(t, home)
			}
			pidBytes, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.ParseUint(strings.TrimSpace(string(pidBytes)), 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
			if err == windows.ERROR_INVALID_PARAMETER {
				return // The kernel has already released the terminated process.
			}
			if err != nil {
				t.Fatal(err)
			}
			defer windows.CloseHandle(process)
			defer windows.TerminateProcess(process, 1) // Clean the fixture even on failure.
			state, err := windows.WaitForSingleObject(process, 1000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("probe descendant still running: wait=%d (%v)", state, err)
			}
		})
	}
}

const windowsSyncProbeFixture = `package main
import (
 "fmt"
 "os"
 "os/exec"
 "strings"
 "time"
)
func main() {
 if len(os.Args) == 2 && os.Args[1] == "child" {
  for { time.Sleep(time.Second) }
 }
 if len(os.Args) != 2 || os.Args[1] != "--version" { os.Exit(2) }
 mode := os.Getenv("TOOLS_PROGRAMMER_TEST_WINDOWS_HOST_MODE")
 child := exec.Command(os.Args[0], "child")
 if mode != "success" { child.Stdout = os.Stdout }
 if err := child.Start(); err != nil { os.Exit(3) }
 if err := os.WriteFile(os.Getenv("TOOLS_PROGRAMMER_TEST_WINDOWS_PID_FILE"), []byte(fmt.Sprint(child.Process.Pid)), 0600); err != nil { os.Exit(4) }
 if mode == "overflow" {
  for { fmt.Print(strings.Repeat("x", 2048)) }
 }
 fmt.Println("1.18.30")
 if mode == "timeout" || mode == "canceled" {
  for { time.Sleep(time.Second) }
 }
}
`
