package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tools-programmer/internal/lab"
)

func main() { os.Exit(run()) }
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: tools-programmer-lab prepare|run|list [flags]")
		return 2
	}
	action := os.Args[1]
	if action == "receive-source" {
		if lab.ReceiveSource(os.Stdin) != nil {
			fmt.Fprintln(os.Stderr, "source_transfer_failed")
			return 1
		}
		return 0
	}
	if action == "bootstrap" {
		if lab.Bootstrap(ctx, os.Args[2:]) != nil {
			fmt.Fprintln(os.Stderr, "lab_bootstrap_failed")
			return 1
		}
		return 0
	}
	f := flag.NewFlagSet("lab", flag.ContinueOnError)
	source := f.String("source", ".", "Exact checkout to build, including nonignored development files")
	output := f.String("output", "", "New private report directory (required for run)")
	image := f.String("image", lab.DefaultImage, "Explicitly prepared local image")
	scenarios := f.String("scenarios", "contracts,permission-denial,egress", "Comma-separated scenario IDs")
	interactive := f.Bool("interactive", false, "Read actual human decisions from the controlling terminal")
	timeout := f.Duration("timeout", 10*time.Minute, "Global deadline, including offline builds")
	if f.Parse(os.Args[2:]) != nil || f.NArg() != 0 {
		return 2
	}
	if action == "list" {
		for _, s := range lab.ScenarioNames() {
			fmt.Println(s)
		}
		return 0
	}
	selected, err := lab.Select(*scenarios)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid_scenarios")
		return 2
	}
	if action == "prepare" {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		if err := lab.Prepare(ctx, *source, *image); err != nil {
			fmt.Fprintln(os.Stderr, lab.PrepareDiagnostic(err))
			return 1
		}
		fmt.Println("Image prepared; runtime coverage not exercised.")
		return 0
	}
	if action == "guest" {
		return lab.Guest(ctx, selected, *interactive)
	}
	if action != "run" || *output == "" {
		fmt.Fprintln(os.Stderr, "run_requires_new_output")
		return 2
	}
	r, err := lab.RunHost(ctx, lab.HostOptions{Source: *source, Output: *output, Image: *image, Scenarios: selected, Interactive: *interactive, Timeout: *timeout})
	if r.Startup != nil && r.Startup.Status == "failed" {
		fmt.Fprintf(os.Stderr, "lab_startup_failed stage=%s cause=%s exit=%d\n", r.Startup.Stage, r.Startup.Cause, r.Startup.ExitCode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lab_run_failed")
		return 1
	}
	for _, result := range r.Results {
		fmt.Printf("%s: %s (%s)\n", result.Scenario, result.Status, strings.TrimSpace(result.Code))
	}
	return r.ExitCode()
}
