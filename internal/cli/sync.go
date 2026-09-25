package cli

import (
	"errors"
	"fmt"

	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpinstall"
	"tools-programmer/internal/skillsync"
)

const (
	syncSkillStep       = "skill sync opencode"
	syncIntegrationStep = "integration sync opencode"
	syncMCPInstallStep  = "mcp install opencode"
)

// SyncResult reports each setup operation, including the result returned by a
// step that fails after making partial progress. Results for later steps remain
// empty when an earlier step fails.
type SyncResult struct {
	Skill       skillsync.Result             `json:"skill"`
	Integration mcpinstall.IntegrationResult `json:"integration"`
	MCP         mcpinstall.Result            `json:"mcp"`
	FailedStep  string                       `json:"failed_step,omitempty"`
}

func runSync(path, command string, args []string) (SyncResult, error) {
	r := SyncResult{}
	if command != "sync opencode" || len(args) != 0 {
		return r, core.Fail("invalid_input", "expected: sync opencode; no additional arguments supported")
	}
	database, err := selectedDatabase(path)
	if err != nil {
		return r, err
	}

	r.Skill, err = skillsync.Sync()
	if err != nil {
		r.FailedStep = syncSkillStep
		return r, syncFailure(syncSkillStep, err)
	}
	r.Integration, err = mcpinstall.SyncIntegration(database)
	if err != nil {
		r.FailedStep = syncIntegrationStep
		return r, syncFailure(syncIntegrationStep, err)
	}
	r.MCP, err = mcpinstall.Install("opencode", database)
	if err != nil {
		r.FailedStep = syncMCPInstallStep
		return r, syncFailure(syncMCPInstallStep, err)
	}
	return r, nil
}

func syncFailure(step string, err error) error {
	var typed *core.Error
	if errors.As(err, &typed) {
		return core.Fail(typed.Code, fmt.Sprintf("sync opencode failed at %s: %s", step, typed.Message))
	}
	return core.Fail("sync_failed", fmt.Sprintf("sync opencode failed at %s: %v", step, err))
}
