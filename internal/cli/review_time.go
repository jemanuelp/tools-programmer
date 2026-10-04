package cli

import (
	"context"
	"flag"
	"io"
	"time"

	"tools-programmer/internal/core"
	"tools-programmer/internal/service"
	"tools-programmer/internal/store"
)

const reviewTimeHelp = `Manual review-time history:
  review-time record --project NAME --entry-id ID --reviewer LABEL --duration 15m [--issue-number N] [--pr N] [--sha FULL_SHA]
  review-time report --project NAME [--issue-number N] [--pr N]

Entry IDs are stable replay identities: identical data returns the original row;
changed data under the same ID conflicts. Reviewer labels are self-declared.
PR review entries without --sha are reported under the unknown revision bucket.
Reports show manual human duration only; automatic agent capture is pending and
agent effort and elapsed review time remain unknown, not zero or inferred.
`

func optionalReviewTimeNumber(flags *flag.FlagSet, name string, value int, label string) (*int, error) {
	selected := false
	flags.Visit(func(value *flag.Flag) {
		if value.Name == name {
			selected = true
		}
	})
	if !selected {
		return nil, nil
	}
	if !core.ValidPR(value) {
		return nil, core.Fail("invalid_input", label+" must be positive")
	}
	return &value, nil
}

func parseReviewDuration(value string) (int64, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 || duration%time.Millisecond != 0 {
		return 0, core.Fail("invalid_input", "duration must be positive and represent whole milliseconds, for example 15m or 250ms")
	}
	return int64(duration / time.Millisecond), nil
}

func runReviewTime(ctx context.Context, path, command string, args []string) (any, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var project, entryID, reviewer, durationText, sha string
	var issueNumber, pr int
	switch command {
	case "review-time record":
		flags.StringVar(&project, "project", "", "registered project")
		flags.StringVar(&entryID, "entry-id", "", "stable manual-entry replay identity")
		flags.StringVar(&reviewer, "reviewer", "", "self-declared reviewer label")
		flags.StringVar(&durationText, "duration", "", "positive duration with units, such as 15m")
		flags.IntVar(&issueNumber, "issue-number", 0, "optional issue number")
		flags.IntVar(&pr, "pr", 0, "optional PR number")
		flags.StringVar(&sha, "sha", "", "optional exact lowercase full SHA actually reviewed")
	case "review-time report":
		flags.StringVar(&project, "project", "", "registered project")
		flags.IntVar(&issueNumber, "issue-number", 0, "optional issue-number filter")
		flags.IntVar(&pr, "pr", 0, "optional PR filter")
	default:
		return nil, core.Fail("invalid_input", "unknown review-time command")
	}
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return nil, core.Fail("invalid_input", "invalid review-time arguments; use --help")
	}
	if !core.ValidName(project) {
		return nil, core.Fail("invalid_input", "registered project required")
	}
	issueFilter, err := optionalReviewTimeNumber(flags, "issue-number", issueNumber, "issue number")
	if err != nil {
		return nil, err
	}
	prFilter, err := optionalReviewTimeNumber(flags, "pr", pr, "PR number")
	if err != nil {
		return nil, err
	}
	var entry core.ReviewTimeEntry
	if command == "review-time record" {
		durationMS, err := parseReviewDuration(durationText)
		if err != nil {
			return nil, err
		}
		entry = core.ReviewTimeEntry{
			ID: entryID, Project: project, IssueNumber: issueFilter, PR: prFilter, SHA: sha,
			Reviewer: reviewer, DurationMS: durationMS, Source: core.ReviewTimeSourceManual,
		}
		if err := entry.Validate(); err != nil {
			return nil, err
		}
	}

	databasePath, err := selectedDatabase(path)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(databasePath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	dbView := db.WithReadContext(ctx)
	if command == "review-time record" {
		return dbView.RecordReviewTime(entry)
	}
	return (&service.Service{Store: dbView}).ReviewTimeReport(project, issueFilter, prFilter)
}
