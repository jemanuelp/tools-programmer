package cli

import (
	"flag"
	"io"
	"os"
	"path/filepath"

	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpinstall"
)

func runSyncOpenCodeV2(path string, args []string) (mcpinstall.IntegrationResult, error) {
	result := mcpinstall.IntegrationResult{Target: "opencode-v2", Updated: []string{}}
	flags := flag.NewFlagSet("sync opencode-v2", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	host := flags.String("opencode-bin", "", "selected V2 executable; otherwise PATH")
	root := flags.String("config-root", "", "distinct absolute V2 global config root")
	runtimeID := flags.String("runtime-id", "", "stable explicit opencode-v2 runtime namespace")
	if err := flags.Parse(args); err != nil || len(flags.Args()) != 0 || *runtimeID == "" {
		return result, core.Fail("invalid_input", "expected: sync opencode-v2 --runtime-id opencode-v2:ID [--opencode-bin PATH] [--config-root DIR]")
	}
	empty := false
	flags.Visit(func(f *flag.Flag) {
		if f.Value.String() == "" {
			empty = true
		}
	})
	if empty {
		return result, core.Fail("invalid_input", "explicit V2 options must not be empty")
	}
	database, err := selectedDatabase(path)
	if err != nil {
		return result, err
	}
	if _, err := preflightOpenCodeV2(*host); err != nil {
		return result, err
	}
	binary, err := os.Executable()
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if err != nil {
		return result, core.Fail("integration_unavailable", "cannot resolve this executable")
	}
	return mcpinstall.SyncOpenCodeV2(*root, binary, database, *runtimeID)
}
