package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpinstall"
	"tools-programmer/internal/skillsync"
)

const (
	syncOpenCodePreflightStep = "OpenCode version preflight"
	syncSkillStep             = "skill sync opencode"
	syncIntegrationStep       = "integration sync opencode"
	syncMCPInstallStep        = "mcp install opencode"
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
	if command != "sync opencode" {
		return r, core.Fail("invalid_input", "expected: sync opencode [--opencode-bin PATH]")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	opencodeBin := flags.String("opencode-bin", "", "OpenCode executable for version preflight")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return r, core.Fail("invalid_input", "expected: sync opencode [--opencode-bin PATH]; no additional arguments supported")
	}
	opencodeBinSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "opencode-bin" {
			opencodeBinSet = true
		}
	})
	if opencodeBinSet && *opencodeBin == "" {
		return r, core.Fail("invalid_input", "--opencode-bin requires a non-empty executable path")
	}
	database, err := selectedDatabase(path)
	if err != nil {
		return r, err
	}
	if err := preflightOpenCode(*opencodeBin); err != nil {
		r.FailedStep = syncOpenCodePreflightStep
		return r, syncFailure(syncOpenCodePreflightStep, err)
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
