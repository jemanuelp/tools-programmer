package cli

import (
	"os"
	"path/filepath"

	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpinstall"
)

// prepareOpenCodeV2 is deliberately not dispatched by the public CLI. Tests
// supply a fake host; real diagnostic/installation authorization is separate.
func prepareOpenCodeV2(database, host, root, runtimeID string) (mcpinstall.OpenCodeV2ConfigPlan, error) {
	var plan mcpinstall.OpenCodeV2ConfigPlan
	database, err := selectedDatabase(database)
	if err != nil {
		return plan, err
	}
	if _, err := preflightOpenCodeV2(host); err != nil {
		return plan, err
	}
	binary, err := os.Executable()
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err != nil {
		return plan, core.Fail("integration_unavailable", "cannot resolve executable")
	}
	return mcpinstall.PrepareOpenCodeV2Config(root, binary, database, runtimeID)
}
