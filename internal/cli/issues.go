package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"tools-programmer/internal/core"
	"tools-programmer/internal/service"
	"tools-programmer/internal/store"
)

const issueHelp = `Local issue history (no GitHub writes):
  issue add --project NAME --number N [--remote-id ID] [--url URL]
  issue link --issue-id ID --pr N
  issue observe --issue-id ID --remote-id OBSERVED_ID
  issue propose --issue-id ID --kind initial|revision|final --score 1|2|3|5|8|13|21 --proposer AGENT --rationale TEXT
  issue confirm --proposal-id ID --human NAME --rationale TEXT --human-confirmed
  issue outcome --issue-id ID --outcome completed|cancelled|reopened --actor NAME --rationale TEXT
  issue summary --project NAME --number N
  activity record < normalized-event.json
  activity dispatch-show --runtime ID --session PARENT_ID --call CALL_ID (read-only)

Confirmation is an explicit local declaration of human approval, not proof of
human presence. Agents must never confirm their own proposals or infer consent.
Activity record is a trusted local adapter boundary, not a model-facing tool.
The observer obtains session IDs from runtime context. See docs/issues.md.
`

func runIssue(ctx context.Context, path, command string, args []string, in io.Reader) (any, error) {
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var project, remote, url, kind, proposer, rationale, human, outcome, actor string
	var number, pr, score int
	var issueID, proposalID int64
	var confirmed bool
	var runtime, session, call string
	switch command {
	case "issue add", "issue summary":
		f.StringVar(&project, "project", "", "registered project")
		f.IntVar(&number, "number", 0, "issue number")
		if command == "issue add" {
			f.StringVar(&remote, "remote-id", "", "observed remote stable ID")
			f.StringVar(&url, "url", "", "canonical HTTPS GitHub issue URL")
		}
	case "issue link":
		f.Int64Var(&issueID, "issue-id", 0, "local issue ID")
		f.IntVar(&pr, "pr", 0, "PR number")
	case "issue observe":
		f.Int64Var(&issueID, "issue-id", 0, "local issue ID")
		f.StringVar(&remote, "remote-id", "", "observed remote ID")
	case "issue propose":
		f.Int64Var(&issueID, "issue-id", 0, "local issue ID")
		f.StringVar(&kind, "kind", "", "estimate kind")
		f.IntVar(&score, "score", 0, "Fibonacci score")
		f.StringVar(&proposer, "proposer", "", "agent identity")
		f.StringVar(&rationale, "rationale", "", "proposal rationale")
	case "issue confirm":
		f.Int64Var(&proposalID, "proposal-id", 0, "proposal ID")
		f.StringVar(&human, "human", "", "human identity")
		f.StringVar(&rationale, "rationale", "", "confirmation rationale")
		f.BoolVar(&confirmed, "human-confirmed", false, "explicit human confirmation was obtained")
	case "issue outcome":
		f.Int64Var(&issueID, "issue-id", 0, "local issue ID")
		f.StringVar(&outcome, "outcome", "", "explicit outcome")
		f.StringVar(&actor, "actor", "", "actor identity")
		f.StringVar(&rationale, "rationale", "", "outcome rationale")
	case "activity record":
	case "activity dispatch-show":
		f.StringVar(&runtime, "runtime", "", "runtime namespace")
		f.StringVar(&session, "session", "", "authoritative parent session")
		f.StringVar(&call, "call", "", "authoritative task call ID")
	default:
		return nil, core.Fail("invalid_input", "unknown issue/activity command")
	}
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return nil, core.Fail("invalid_input", "invalid issue/activity arguments; use --help")
	}
	if command == "issue confirm" && !confirmed {
		return nil, core.Fail("human_confirmation", "obtain explicit human approval and pass --human-confirmed")
	}
	if (command == "issue add" || command == "issue summary") && (!core.ValidName(project) || !core.ValidPR(number)) {
		return nil, core.Fail("invalid_input", "project and positive issue number required")
	}
	if (command == "issue link" || command == "issue propose" || command == "issue outcome") && issueID <= 0 {
		return nil, core.Fail("invalid_input", "positive local issue ID required")
	}
	if command == "issue link" && !core.ValidPR(pr) {
		return nil, core.Fail("invalid_input", "positive PR required")
	}
	if command == "issue observe" && (issueID <= 0 || remote == "") {
		return nil, core.Fail("invalid_input", "issue and observed remote ID required")
	}
	if command == "issue propose" && (!core.ValidScore(score) || (kind != "initial" && kind != "revision" && kind != "final") || !core.ValidText(proposer, 200) || !core.ValidText(rationale, 8000)) {
		return nil, core.Fail("invalid_input", "valid estimate kind, Fibonacci 1–21, proposer and rationale required")
	}
	if command == "issue confirm" && (proposalID <= 0 || !core.ValidText(human, 200) || !core.ValidText(rationale, 8000)) {
		return nil, core.Fail("invalid_input", "proposal, human and rationale required")
	}
	if command == "issue outcome" && ((outcome != "completed" && outcome != "cancelled" && outcome != "reopened") || !core.ValidText(actor, 200) || !core.ValidText(rationale, 8000)) {
		return nil, core.Fail("invalid_input", "valid outcome, actor and rationale required")
	}
	var event core.ActivityEvent
	if command == "activity dispatch-show" {
		if _, err := core.DispatchMarker(runtime, session, call); err != nil {
			return nil, err
		}
	}
	if command == "activity record" {
		var err error
		event, err = decodeActivity(in)
		if err != nil {
			return nil, err
		}
	}
	path, err := selectedDatabase(path)
	if err != nil {
		return nil, err
	}
	var db *store.Store
	if command == "issue summary" || command == "activity dispatch-show" {
		db, err = store.OpenReadOnly(path)
	} else {
		db, err = store.Open(path)
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	dbView := db.WithReadContext(ctx)
	switch command {
	case "issue add":
		return dbView.AddIssueWithURL(project, number, remote, url)
	case "issue link":
		err = dbView.LinkIssuePR(issueID, pr)
	case "issue observe":
		err = dbView.ObserveIssueRemoteID(issueID, remote)
	case "issue propose":
		return dbView.ProposeEstimate(issueID, kind, score, proposer, rationale)
	case "issue confirm":
		return dbView.ConfirmEstimate(proposalID, human, rationale)
	case "issue outcome":
		err = dbView.RecordIssueOutcome(issueID, outcome, actor, rationale)
	case "issue summary":
		return (&service.Service{Store: dbView}).IssueSummary(project, number)
	case "activity record":
		err = dbView.RecordActivity(event)
	case "activity dispatch-show":
		return dbView.Dispatch(runtime, session, call)
	}
	if err != nil {
		return nil, err
	}
	return map[string]bool{"recorded": true}, nil
}

func decodeActivity(in io.Reader) (core.ActivityEvent, error) {
	var e core.ActivityEvent
	bad := func() (core.ActivityEvent, error) {
		return e, core.Fail("invalid_event", "expected one normalized JSON object, unique known fields, maximum 16384 bytes")
	}
	b, err := io.ReadAll(io.LimitReader(in, 16385))
	if err != nil || len(b) > 16384 {
		return bad()
	}
	// encoding/json normally accepts duplicate keys. Reject them before decoding.
	d := json.NewDecoder(bytes.NewReader(b))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return bad()
	}
	seen := map[string]bool{}
	allowed := map[string]bool{}
	for _, name := range []string{"id", "runtime", "session", "segment", "kind", "at", "activity", "category", "target", "project", "issue", "parent_session", "parent_segment", "dispatch_at", "resume", "reason"} {
		allowed[name] = true
	}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || !allowed[key] {
			return bad()
		}
		seen[key] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return bad()
		}
	}
	if _, err = d.Token(); err != nil {
		return bad()
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return bad()
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&e); err != nil {
		return bad()
	}
	for _, key := range []string{"id", "runtime", "session", "segment", "kind", "at"} {
		if !seen[key] {
			return bad()
		}
	}
	if err = e.Validate(); err != nil {
		return e, err
	}
	return e, nil
}
