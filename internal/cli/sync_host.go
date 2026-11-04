package cli

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tools-programmer/internal/core"
)

const hostProbeDeadline = 3 * time.Second
const hostProbeOutputLimit = 8192

var hostVersionOutput = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// probeOutput enforces the cap while exec copies stdout, not after buffering it.
type probeOutput struct {
	data     []byte
	overflow bool
	cancel   context.CancelFunc
}

func (w *probeOutput) Write(p []byte) (int, error) {
	if len(p) > hostProbeOutputLimit-len(w.data) {
		w.overflow = true
		w.cancel()
		return 0, fmt.Errorf("version output exceeds bound")
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

func checkSyncHost(ctx context.Context, target, selected string) error {
	expected := "1.18.30"
	if target == "opencode-v2" {
		expected = "2.2.5"
	}
	next := "; select the matching installed executable with --opencode-bin ABSOLUTE_PATH; no setup files were changed"
	if selected == "" {
		path, err := exec.LookPath("opencode")
		if err != nil {
			return core.Fail("host_unavailable", "expected "+target+" "+expected+", detected no executable"+next)
		}
		selected = path
	}
	if !filepath.IsAbs(selected) {
		return core.Fail("invalid_input", "selected OpenCode executable must be absolute"+next)
	}
	resolved, err := filepath.EvalSymlinks(selected)
	if err != nil {
		return core.Fail("host_unavailable", "expected "+target+" "+expected+", detected unavailable executable"+next)
	}
	probeCtx, cancel := context.WithTimeout(ctx, hostProbeDeadline)
	defer cancel()
	output := &probeOutput{cancel: cancel}
	cmd := exec.CommandContext(probeCtx, resolved, "--version")
	cmd.Stdout = output
	// A nil stderr is discarded by os/exec. Never retain upstream diagnostics.
	cmd.WaitDelay = 100 * time.Millisecond
	err = runHostProbe(probeCtx, cmd)
	if err != nil || probeCtx.Err() != nil || output.overflow {
		return core.Fail("host_probe", "expected "+target+" "+expected+", detected unavailable version (probe failed, timed out or exceeded output limit)"+next)
	}
	version := strings.TrimSpace(string(output.data))
	if !hostVersionOutput.MatchString(version) {
		return core.Fail("host_probe", "expected "+target+" "+expected+", detected invalid or ambiguous version output"+next)
	}
	if version != expected {
		return core.Fail("host_version", "expected "+target+" "+expected+", detected "+version+next)
	}
	return nil
}
