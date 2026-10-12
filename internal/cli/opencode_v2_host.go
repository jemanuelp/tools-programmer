package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tools-programmer/internal/core"
)

const expectedOpenCodeV2Version = "2.0.26"

// This private preflight is exercised with fake executables only. It is not a
// public host diagnostic or a claim that a real --version invocation is inert.
func preflightOpenCodeV2(explicit string) (string, error) {
	binary, err := selectOpenCodeBinary(explicit)
	if err != nil {
		return "", openCodeV2ProbeFailure("opencode_not_found", "no selected executable")
	}
	// Released V2 initializes global directories and logging even for --version.
	// Give it private roots and cwd, never operator configuration or credentials.
	// This is state separation for the supported CLI, not an arbitrary-code sandbox.
	scratch, err := os.MkdirTemp("", "tools-programmer-v2-version-")
	if err != nil {
		return "", openCodeV2ProbeFailure("opencode_probe_failed", "unavailable private probe directory")
	}
	defer os.RemoveAll(scratch)
	environment := []string{"NO_COLOR=1", "PATH=" + os.Getenv("PATH"), "HOME=" + scratch}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"} {
		root := filepath.Join(scratch, key)
		if err := os.Mkdir(root, 0700); err != nil {
			return "", openCodeV2ProbeFailure("opencode_probe_failed", "unavailable private probe roots")
		}
		environment = append(environment, key+"="+root)
	}
	version, err := probeOpenCodeVersionIn(binary, 3*time.Second, environment, scratch, parseOpenCodeV2Version)
	if err != nil {
		code, detected := "opencode_probe_failed", "a failed --version probe"
		switch {
		case errors.Is(err, errOpenCodeProbeTimeout):
			code, detected = "opencode_probe_timeout", "a probe exceeding 3 seconds"
		case errors.Is(err, errOpenCodeOutputLarge):
			code, detected = "opencode_version_output_too_large", "output exceeding 1024 bytes"
		case errors.Is(err, errOpenCodeVersionText):
			code, detected = "opencode_version_invalid", "unrecognized --version output"
		}
		return "", openCodeV2ProbeFailure(code, detected)
	}
	if version != expectedOpenCodeV2Version {
		return "", openCodeV2ProbeFailure("opencode_version_mismatch", "version "+version)
	}
	return binary, nil
}

func openCodeV2ProbeFailure(code, detected string) error {
	return core.Fail(code, fmt.Sprintf("OpenCode V2 preflight failed: expected pinned version %s; detected %s. Select its executable explicitly; no integration configuration was written.", expectedOpenCodeV2Version, detected))
}

func parseOpenCodeV2Version(output []byte) (string, bool) {
	const prefix = "opencode v"
	if !strings.HasPrefix(string(output), prefix) {
		return "", false
	}
	return parseOpenCodeVersion(output[len(prefix):])
}
