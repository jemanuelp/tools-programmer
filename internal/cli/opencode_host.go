package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"tools-programmer/internal/core"
)

const (
	expectedOpenCodeVersion = "1.18.30"
	openCodeVersionLimit    = 1024
	openCodeProbeWaitDelay  = 100 * time.Millisecond
)

var openCodeVersionLine = regexp.MustCompile(`^((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$`)

var (
	errOpenCodeProbeTimeout = errors.New("version probe timed out")
	errOpenCodeProbeFailed  = errors.New("version probe failed")
	errOpenCodeOutputLarge  = errors.New("version output exceeded limit")
	errOpenCodeVersionText  = errors.New("unrecognized version output")
)

func preflightOpenCode(explicit string) error {
	binary, err := selectOpenCodeBinary(explicit)
	if err != nil {
		detected := "no `opencode` executable on PATH"
		if explicit != "" {
			detected = "no executable at the --opencode-bin path"
		}
		return core.Fail("opencode_not_found", fmt.Sprintf("OpenCode preflight failed: expected version %s; detected %s. Install OpenCode %s or provide its executable with --opencode-bin PATH.", expectedOpenCodeVersion, detected, expectedOpenCodeVersion))
	}

	version, err := probeOpenCodeVersion(binary, 3*time.Second)
	if err != nil {
		switch {
		case errors.Is(err, errOpenCodeProbeTimeout):
			return core.Fail("opencode_probe_timeout", fmt.Sprintf("OpenCode preflight failed: expected version %s; detected a --version probe that exceeded 3 seconds. Verify the selected executable or pass --opencode-bin PATH.", expectedOpenCodeVersion))
		case errors.Is(err, errOpenCodeOutputLarge):
			return core.Fail("opencode_version_output_too_large", fmt.Sprintf("OpenCode preflight failed: expected version %s; detected --version output larger than %d bytes. Verify the selected executable.", expectedOpenCodeVersion, openCodeVersionLimit))
		case errors.Is(err, errOpenCodeVersionText):
			return core.Fail("opencode_version_invalid", fmt.Sprintf("OpenCode preflight failed: expected version %s; detected unrecognized --version output. Ensure the selected executable prints one version line, then pass --opencode-bin PATH if needed.", expectedOpenCodeVersion))
		default:
			return core.Fail("opencode_probe_failed", fmt.Sprintf("OpenCode preflight failed: expected version %s; detected that the --version probe could not run successfully. Verify the executable or pass --opencode-bin PATH.", expectedOpenCodeVersion))
		}
	}
	if version != expectedOpenCodeVersion {
		return core.Fail("opencode_version_mismatch", fmt.Sprintf("OpenCode preflight failed: expected version %s; detected version %s. Select a V1 %s executable with --opencode-bin PATH.", expectedOpenCodeVersion, version, expectedOpenCodeVersion))
	}
	return nil
}

func selectOpenCodeBinary(explicit string) (string, error) {
	var path string
	if explicit != "" {
		var err error
		path, err = filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
	} else {
		var err error
		path, err = exec.LookPath("opencode")
		if err != nil {
			return "", err
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("selected OpenCode path is not a regular file")
	}
	return path, nil
}

func probeOpenCodeVersion(binary string, timeout time.Duration) (string, error) {
	return probeOpenCodeVersionWith(binary, timeout, nil, parseOpenCodeVersion)
}

// The V1 wrapper retains inherited environment and raw-version parsing. Private
// V2 preparation supplies its own environment and source-backed labelled parser.
func probeOpenCodeVersionWith(binary string, timeout time.Duration, environment []string, parse func([]byte) (string, bool)) (string, error) {
	return probeOpenCodeVersionIn(binary, timeout, environment, "", parse)
}

func probeOpenCodeVersionIn(binary string, timeout time.Duration, environment []string, directory string, parse func([]byte) (string, bool)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output := &boundedOutput{limit: openCodeVersionLimit, cancel: cancel}
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Env = environment
	cmd.Dir = directory
	cmd.Stdout = output
	cmd.Stderr = output
	// Bound copy goroutines if a spawned child inherits either output pipe.
	cmd.WaitDelay = openCodeProbeWaitDelay
	runErr := cmd.Run()
	data, truncated := output.snapshot()
	if truncated {
		return "", errOpenCodeOutputLarge
	}
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errOpenCodeProbeTimeout
		}
		return "", errOpenCodeProbeFailed
	}
	version, ok := parse(data)
	if !ok {
		return "", errOpenCodeVersionText
	}
	return version, nil
}

func parseOpenCodeVersion(output []byte) (string, bool) {
	line := string(output)
	if strings.HasSuffix(line, "\r\n") {
		line = strings.TrimSuffix(line, "\r\n")
	} else if strings.HasSuffix(line, "\n") {
		line = strings.TrimSuffix(line, "\n")
	}
	if strings.ContainsAny(line, "\r\n") {
		return "", false
	}
	match := openCodeVersionLine.FindStringSubmatch(line)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}

type boundedOutput struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
	cancel    context.CancelFunc
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - len(w.data)
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > 0 {
		w.data = append(w.data, p[:remaining]...)
	}
	if remaining < len(p) {
		w.truncated = true
		w.cancel()
		return remaining, errOpenCodeOutputLarge
	}
	return len(p), nil
}

func (w *boundedOutput) snapshot() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.data...), w.truncated
}
