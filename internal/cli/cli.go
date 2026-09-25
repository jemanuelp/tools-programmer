package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"tools-programmer/internal/core"
	"tools-programmer/internal/github"
	"tools-programmer/internal/gitidentity"
	"tools-programmer/internal/mcpinstall"
	"tools-programmer/internal/mcpserver"
	"tools-programmer/internal/service"
	"tools-programmer/internal/skillsync"
	"tools-programmer/internal/store"
)

const help = `tools-programmer — local technical review history and explicit GitHub approvals

Usage: tools-programmer [--db PATH] COMMAND SUBCOMMAND [FLAGS]

issue           add|observe|link|propose|confirm|outcome|summary (use issue summary --help)
activity record normalized local runtime event on stdin (no raw payloads)

skill sync      opencode (install/update bundled skill; no database or gh)
integration sync opencode (publish owned plugin bundle and JSONC entry; no dependencies or state)
integration info (read embedded capability/build/bundle identity; no installation or database)
sync            opencode (run skill, integration and MCP setup in order; no executable or AGENTS.md changes)
mcp serve       [--enable-project-registration] [--enable-issue-estimates] [--enable-issue-outcomes] (stdio MCP; existing database)
mcp install     opencode (register this executable; no database open or gh)
project add     --name NAME --repo [HOST/]OWNER/REPO --github-login LOGIN --git-name NAME --git-email EMAIL [--path DIR] [--allow-no-checks]
project bind    --project NAME --repo [HOST/]OWNER/REPO --github-login LOGIN --git-name NAME --git-email EMAIL
project origin  --project NAME --repo [HOST/]OWNER/REPO --origin-url URL (explicit association; never changes Git origin)
project check   --project NAME (read-only effective Git author/committer in current checkout)
project list
pr sync         --project NAME --prs 1,2
pr list         --project NAME
review record   --project NAME --pr N --sha FULL_SHA --reviewer NAME --method METHOD
                --outcome approved|changes_requested|incomplete [--summary TEXT] [--evidence TEXT]
review list     --project NAME [--pr N]
approval plan   --project NAME --prs 1,2
approval show   --id PLAN_ID
approval apply  --id PLAN_ID
approval reconcile --id PLAN_ID

--help and --version do not open a database. Each subcommand accepts --help.
Skill sync overwrites the two bundled files in ~/.config/opencode/skills/tools-programmer;
other files are preserved. Keep the binary on PATH and restart OpenCode after sync.
Database: --db, then TOOLS_PROGRAMMER_DB, then $XDG_STATE_HOME/tools-programmer/history.db
(fallback ~/.local/state/tools-programmer/history.db). Parent directory must be private.
Results and errors are JSON, except mcp serve uses the MCP protocol on stdout.
MCP installation pins absolute executable/database paths; restart OpenCode after install.
Local commands never invoke gh.
Planning is read-only on GitHub. Only explicit approval apply may publish APPROVE reviews.
Unknown writes require read-only reconciliation; never retry them blindly.
`

type envelope struct {
	Data  any         `json:"data,omitempty"`
	Error *core.Error `json:"error,omitempty"`
}

func Run(ctx context.Context, args []string, out io.Writer, runner github.Runner) int {
	return RunWithIO(ctx, args, os.Stdin, out, os.Stderr, runner)
}

// RunWithIO keeps the MCP protocol and diagnostics on separate streams.
func RunWithIO(ctx context.Context, args []string, in io.ReadCloser, out, stderr io.Writer, runner github.Runner) int {
	resultOut := out
	emit := func(data any, err error) int {
		e := envelope{Data: data}
		code := 0
		if err != nil {
			var typed *core.Error
			if !errors.As(err, &typed) {
				typed = &core.Error{Code: "internal_error", Message: "operation failed; verify database path, permissions, and storage availability"}
			}
			e.Error = typed
			code = 1
		}
		enc := json.NewEncoder(resultOut)
		enc.SetIndent("", "  ")
		if enc.Encode(e) != nil {
			return 1
		}
		return code
	}
	global := flag.NewFlagSet("tools-programmer", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	dbPath := global.String("db", "", "database path")
	version := global.Bool("version", false, "print version")
	if err := global.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(out, help)
		return 0
	} else if err != nil {
		return emit(nil, core.Fail("invalid_input", "invalid global flags; use --help"))
	}
	if *version {
		if len(global.Args()) != 0 {
			return emit(nil, core.Fail("invalid_input", "--version accepts no command"))
		}
		return emit(core.BuildVersion(), nil)
	}
	remaining := global.Args()
	if len(remaining) < 2 {
		if len(remaining) == 0 {
			fmt.Fprint(out, help)
			return 0
		}
		return emit(nil, core.Fail("invalid_input", "expected command and subcommand; use --help"))
	}
	command := remaining[0] + " " + remaining[1]
	if remaining[0] == "integration" {
		if len(remaining) == 3 && (remaining[2] == "--help" || remaining[2] == "-h") {
			fmt.Fprint(out, help)
			return 0
		}
		data, err := runIntegration(*dbPath, command, remaining[2:])
		return emit(data, err)
	}
	if remaining[0] == "sync" {
		if len(remaining) == 3 && (remaining[2] == "--help" || remaining[2] == "-h") {
			fmt.Fprint(out, help)
			return 0
		}
		data, err := runSync(*dbPath, command, remaining[2:])
		return emit(data, err)
	}
	if remaining[0] == "issue" || remaining[0] == "activity" {
		if len(remaining) == 3 && (remaining[2] == "--help" || remaining[2] == "-h") {
			fmt.Fprint(out, issueHelp)
			return 0
		}
		data, err := runIssue(ctx, *dbPath, command, remaining[2:], in)
		return emit(data, err)
	}
	if command == "mcp serve" || command == "mcp install" {
		if len(remaining) == 3 && (remaining[2] == "--help" || remaining[2] == "-h") {
			fmt.Fprint(out, help)
			return 0
		}
		if command == "mcp serve" {
			resultOut = stderr
			flags := flag.NewFlagSet(command, flag.ContinueOnError)
			flags.SetOutput(io.Discard)
			enabled := flags.Bool("enable-project-registration", false, "allow explicit local project declarations")
			estimates := flags.Bool("enable-issue-estimates", false, "expose local issue estimate proposals")
			outcomes := flags.Bool("enable-issue-outcomes", false, "expose explicit local issue outcomes; not assignment consent")
			if err := flags.Parse(remaining[2:]); err != nil || len(flags.Args()) != 0 {
				return emit(nil, core.Fail("invalid_input", "expected: [--db PATH] mcp serve [--enable-project-registration] [--enable-issue-estimates] [--enable-issue-outcomes]"))
			}
			if err := serveMCP(ctx, *dbPath, in, out, mcpserver.Options{EnableProjectRegistration: *enabled, EnableIssueEstimates: *estimates, EnableIssueOutcomes: *outcomes}); err != nil {
				return emit(nil, err)
			}
			return 0
		}
		if len(remaining) != 3 || !mcpinstall.Supports(remaining[2]) {
			return emit(nil, core.Fail("invalid_input", "expected: mcp install opencode; unsupported target or additional arguments"))
		}
		path, err := selectedDatabase(*dbPath)
		if err != nil {
			return emit(nil, err)
		}
		result, err := mcpinstall.Install(remaining[2], path)
		return emit(result, err)
	}
	if command == "skill sync" {
		if len(remaining) == 3 && (remaining[2] == "--help" || remaining[2] == "-h") {
			fmt.Fprint(out, help)
			return 0
		}
		if len(remaining) != 3 || remaining[2] != "opencode" {
			return emit(nil, core.Fail("invalid_input", "expected: skill sync opencode; no additional arguments supported"))
		}
		result, err := skillsync.Sync()
		if err != nil {
			return emit(result, core.Fail("skill_sync_failed", "skill sync failed (earlier files may have been updated): "+err.Error()))
		}
		return emit(result, nil)
	}
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var name, repo, path, project, prs, sha, reviewer, method, outcome, summary, evidence, id string
	var pr int
	var allowNoChecks bool
	var login, gitName, gitEmail, originURL string
	if command == "project add" || command == "project bind" {
		f.StringVar(&login, "github-login", "", "explicit expected GitHub login")
		f.StringVar(&gitName, "git-name", "", "expected Git author and committer name")
		f.StringVar(&gitEmail, "git-email", "", "expected Git author and committer email")
	}
	switch command {
	case "project add":
		f.StringVar(&name, "name", "", "project name")
		f.StringVar(&repo, "repo", "", "canonical repository")
		f.StringVar(&path, "path", "", "optional local path")
		f.BoolVar(&allowNoChecks, "allow-no-checks", false, "explicitly allow no observed checks")
	case "project list":
	case "project origin":
		f.StringVar(&project, "project", "", "registered project")
		f.StringVar(&repo, "repo", "", "confirm canonical repository")
		f.StringVar(&originURL, "origin-url", "", "explicit credential-free associated origin URL")
	case "project bind":
		f.StringVar(&project, "project", "", "unbound registered project")
		f.StringVar(&repo, "repo", "", "confirm registered repository")
	case "project check":
		f.StringVar(&project, "project", "", "registered project")
	case "pr sync", "approval plan":
		f.StringVar(&project, "project", "", "registered project")
		f.StringVar(&prs, "prs", "", "explicit comma-separated PRs")
	case "pr list":
		f.StringVar(&project, "project", "", "registered project")
	case "review record":
		f.StringVar(&project, "project", "", "registered project")
		f.IntVar(&pr, "pr", 0, "PR number")
		f.StringVar(&sha, "sha", "", "full lowercase head SHA")
		f.StringVar(&reviewer, "reviewer", "", "technical reviewer")
		f.StringVar(&method, "method", "", "review method")
		f.StringVar(&outcome, "outcome", "", "local verdict")
		f.StringVar(&summary, "summary", "", "summary without secrets")
		f.StringVar(&evidence, "evidence", "", "evidence references without secrets")
	case "review list":
		f.StringVar(&project, "project", "", "registered project")
		f.IntVar(&pr, "pr", 0, "optional PR filter")
	case "approval show", "approval apply", "approval reconcile":
		f.StringVar(&id, "id", "", "stored plan ID")
	default:
		return emit(nil, core.Fail("invalid_input", "unknown command; use --help"))
	}
	if err := f.Parse(remaining[2:]); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(out, help)
		return 0
	} else if err != nil || len(f.Args()) != 0 {
		return emit(nil, core.Fail("invalid_input", "invalid command flags or unexpected positional arguments; use --help"))
	}
	if strings.HasPrefix(command, "approval ") && command != "approval plan" && id == "" {
		return emit(nil, core.Fail("invalid_input", "--id is required"))
	}
	if command != "project add" && command != "project list" && !strings.HasPrefix(command, "approval show") && command != "approval apply" && command != "approval reconcile" && project == "" {
		return emit(nil, core.Fail("invalid_input", "--project is required"))
	}
	var numbers []int
	if command == "pr sync" || command == "approval plan" {
		for _, n := range strings.Split(prs, ",") {
			value, err := strconv.Atoi(n)
			if err != nil {
				return emit(nil, core.Fail("invalid_input", "--prs must contain positive comma-separated integers"))
			}
			numbers = append(numbers, value)
		}
		if err := service.Numbers(numbers); err != nil {
			return emit(nil, err)
		}
	}
	if command == "review list" && pr < 0 {
		return emit(nil, core.Fail("invalid_input", "invalid PR filter"))
	}
	if *dbPath == "" {
		var err error
		*dbPath, err = store.DefaultPath()
		if err != nil {
			return emit(nil, err)
		}
	}
	var db *store.Store
	var err error
	if command == "project check" {
		db, err = store.OpenReadOnly(*dbPath)
	} else {
		db, err = store.Open(*dbPath)
	}
	if err != nil {
		return emit(nil, err)
	}
	defer db.Close()
	if project != "" {
		if _, err = db.Project(project); err != nil {
			return emit(nil, err)
		}
	}
	if runner == nil {
		runner = github.ExecRunner{}
	}
	svc := service.Service{Store: db, GitHub: &github.Client{Runner: runner}}
	var data any
	switch command {
	case "project add":
		data, err = db.AddProject(core.Project{Name: name, Repo: repo, Path: path, AllowNoChecks: allowNoChecks, ExpectedGitHub: login, GitName: gitName, GitEmail: gitEmail})
	case "project bind":
		data, err = db.BindProject(project, repo, login, gitName, gitEmail)
	case "project origin":
		data, err = db.SetProjectOrigin(project, repo, originURL)
	case "project check":
		var p core.Project
		p, err = db.Project(project)
		if err == nil {
			data, err = gitidentity.Check(ctx, p)
		}
	case "project list":
		data, err = db.Projects()
	case "pr sync":
		data, err = svc.Sync(ctx, project, numbers)
	case "pr list":
		data, err = db.Snapshots(project)
	case "review record":
		data, err = db.Record(core.LocalReview{Project: project, PR: pr, SHA: sha, Reviewer: reviewer, Method: method, Outcome: outcome, Summary: summary, Evidence: evidence})
	case "review list":
		data, err = db.Reviews(project, pr)
	case "approval plan":
		data, err = svc.Plan(ctx, project, numbers)
	case "approval show":
		data, err = svc.Show(id)
	case "approval apply":
		data, err = svc.Apply(ctx, id)
	case "approval reconcile":
		data, err = svc.Reconcile(ctx, id)
	}
	return emit(data, err)
}
