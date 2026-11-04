package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

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

func runSync(ctx context.Context, path, command string, args []string) (SyncResult, error) {
	r := SyncResult{}
	if command != "sync opencode" && command != "sync opencode-v2" {
		return r, core.Fail("invalid_input", "expected: sync opencode|opencode-v2 [--opencode-bin ABSOLUTE_PATH]")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	host := flags.String("opencode-bin", "", "absolute installed OpenCode executable")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 {
		return r, core.Fail("invalid_input", "expected: sync opencode|opencode-v2 [--opencode-bin ABSOLUTE_PATH]")
	}
	explicitHost := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "opencode-bin" {
			explicitHost = true
		}
	})
	if explicitHost && *host == "" {
		return r, core.Fail("invalid_input", "--opencode-bin requires a nonempty absolute installed executable path; no setup files were changed")
	}
	database, err := selectedDatabase(path)
	if err != nil {
		return r, err
	}
	target := "opencode"
	if command == "sync opencode-v2" {
		target = "opencode-v2"
	}
	if err := checkSyncHost(ctx, target, *host); err != nil {
		r.FailedStep = "host preflight"
		return r, err
	}
	if target == "opencode-v2" {
		r.FailedStep = "integration preflight"
		return r, core.Fail("integration_unavailable", "OpenCode V2 2.2.5 is a published candidate, but this binary has no V2 adapter; complete issue #61 and validate with #50 before V2 setup; no setup files were changed")
	}
	for _, preflight := range []func() error{
		skillsync.Preflight,
		func() error { return mcpinstall.PreflightIntegration(database) },
		func() error { return mcpinstall.Preflight(database) },
	} {
		if err := preflight(); err != nil {
			r.FailedStep = "setup preflight"
			return r, syncFailure(r.FailedStep, err)
		}
	}

	return applySync(database, skillsync.Sync, mcpinstall.SyncIntegration, func(database string) (mcpinstall.Result, error) {
		return mcpinstall.Install("opencode", database)
	})
}

// applySync retains completed steps on later I/O failure. The function inputs
// keep fault tests local without global hooks or changing the public CLI.
func applySync(database string, skill func() (skillsync.Result, error), integration func(string) (mcpinstall.IntegrationResult, error), mcp func(string) (mcpinstall.Result, error)) (SyncResult, error) {
	r := SyncResult{}
	var err error
	r.Skill, err = skill()
	if err != nil {
		r.FailedStep = syncSkillStep
		return r, syncFailure(syncSkillStep, err)
	}
	r.Integration, err = integration(database)
	if err != nil {
		r.FailedStep = syncIntegrationStep
		return r, syncFailure(syncIntegrationStep, err)
	}
	r.MCP, err = mcp(database)
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
