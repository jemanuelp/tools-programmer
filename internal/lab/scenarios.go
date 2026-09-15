package lab

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type scenario struct {
	name string
	run  func(*fixture, *Result) error
}

var scenarios = []scenario{
	{"advisory", advisoryTracking},
	{"contracts", contracts}, {"permission-denial", permissionDenial}, {"fresh-human", freshHuman},
	{"silent", silent}, {"wait-overlap", waitOverlap}, {"permission-overlap", permissionOverlap}, {"parallel", parallelChildren}, {"retry", retryScenario},
	{"switch-reuse", switchReuse}, {"background", backgroundScenario}, {"cancel", cancelScenario},
	{"restart", restartScenario}, {"reconnect", reconnectScenario}, {"egress", egress},
	{"missing-dependencies", missingDependencies}, {"source-control", sourceControl},
	{"contamination", contamination}, {"timeout-cleanup", timeoutCleanup},
}

func ScenarioNames() []string {
	var out []string
	for _, s := range scenarios {
		out = append(out, s.name)
	}
	return out
}
func knownScenario(name string) bool {
	for _, s := range scenarios {
		if s.name == name {
			return true
		}
	}
	return false
}
func Select(input string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, name := range strings.Split(input, ",") {
		if !knownScenario(name) || seen[name] {
			return nil, errors.New("scenario_selection")
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

type fixture struct {
	ctx              context.Context
	root, binary, db string
	interactive      bool
	runtime          *Runtime
}

func (f *fixture) start(ask bool) error {
	r, err := startRuntime(f.ctx, f.root, f.binary, f.db, ask)
	if err == nil {
		f.runtime = r
	}
	return err
}
func (f *fixture) close() {
	if f.runtime != nil {
		f.runtime.close()
	}
}

func Guest(ctx context.Context, selected []string, interactive bool) int {
	started := time.Now()
	r := startupReport()
	write := func() { emitReport(os.Stdout, r) }
	startupStage(&r, "guest_entry", started, os.Stdout)
	fail := func(cause string, exit *int, truncated bool) int {
		startupFailed(&r, cause, exit, truncated, started, os.Stdout)
		fillNotRun(&r, selected)
		write()
		return 1
	}
	s, err := loadSource()
	if err != nil {
		return fail("source_invalid", nil, false)
	}
	r.Commit = s.Commit
	r.SourceHash = s.Hash
	r.Dirty = s.Dirty
	r.Remaining = []string{"two_fresh_human_runs", "human_wait_acceptance", "cancellation_restart_acceptance", "active_queued_reuse", "paid_provider", "openchamber_gui"}
	timeout, err := time.ParseDuration(os.Getenv("LAB_TIMEOUT"))
	if err != nil || timeout > 30*time.Minute || timeout < time.Second {
		return fail("configuration_invalid", nil, false)
	}
	if r.InputHash != inputHash(selected, interactive, timeout) {
		return fail("configuration_invalid", nil, false)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startupStage(&r, "versions", started, os.Stdout)
	for _, check := range []struct{ name, arg, want string }{{"go", "version", "go version go1.26.8 linux/amd64"}, {"node", "--version", "v24.18.0"}, {"opencode", "--version", "1.18.30"}} {
		output, err := command(ctx, "/tmp", privateEnv("/tmp"), check.name, check.arg)
		if err != nil || strings.TrimSpace(string(output)) != check.want {
			return fail("version_mismatch", nil, false)
		}
	}
	r.VersionsVerified = true
	startupStage(&r, "product_build", started, os.Stdout)
	build := exec.CommandContext(ctx, "go", "build", "-p=2", "-mod=readonly", "-buildvcs=false", "-trimpath", "-o", "/tmp/tools-programmer", "./cmd/tools-programmer")
	build.Dir = sourceRoot
	build.Env = privateEnv("/tmp")
	if failure := executeStartupBuild(ctx, build); failure != nil {
		return fail(startupBuildCause(failure.Cause), &failure.ExitCode, failure.Truncated)
	}
	startupStage(&r, "product_verify", started, os.Stdout)
	if _, err := loadSource(); err != nil {
		return fail("source_invalid", nil, false)
	}
	b, err := os.ReadFile("/tmp/tools-programmer")
	if err != nil {
		return fail("build_failed", nil, false)
	}
	r.BinaryHash = digest(b)
	r.Startup = &Startup{Stage: "complete", Status: "complete", ElapsedMS: time.Since(started).Milliseconds()}
	write()
	runSelected(ctx, &r, selected, write, func(name string) Result {
		result := Result{Scenario: name, Status: "fail", Checks: []Check{}}
		start := time.Now()
		func() {
			root, err := os.MkdirTemp("/tmp", "case-")
			if err != nil {
				result.Code = "fixture_directory"
				return
			}
			defer os.RemoveAll(root)
			caseCtx, stop := context.WithTimeout(ctx, 90*time.Second)
			defer stop()
			if interactive {
				caseCtx, stop = context.WithTimeout(ctx, 4*time.Minute)
				defer stop()
			}
			f := &fixture{ctx: caseCtx, root: root, binary: "/tmp/tools-programmer", db: filepath.Join(root, "state/history.db"), interactive: interactive}
			defer f.close()
			if err := privateDirs(root); err != nil {
				result.Code = "fixture_directory"
				return
			}
			if err := f.setup(); err != nil {
				result.Code = err.Error()
				return
			}
			for _, s := range scenarios {
				if s.name == name {
					err = s.run(f, &result)
					break
				}
			}
			if err != nil {
				result.Code = err.Error()
				if !token.MatchString(result.Code) {
					result.Code = "scenario_internal"
				}
				if result.Status != "blocked" && result.Status != "unsupported" {
					result.Status = "fail"
				}
				return
			}
			result.Status = "pass"
		}()
		result.ElapsedMS = time.Since(start).Milliseconds()
		return result
	})
	r.Cleaned = true
	write()
	return r.ExitCode()
}

func runSelected(ctx context.Context, r *Report, selected []string, write func(), run func(string) Result) {
	for _, name := range selected {
		if ctx.Err() != nil {
			fillNotRun(r, selected)
			write()
			return
		}
		r.Results = append(r.Results, run(name))
		write()
	}
}
