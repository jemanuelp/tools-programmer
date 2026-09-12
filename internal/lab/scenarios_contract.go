package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/service"
	"tools-programmer/internal/store"
)

func (f *fixture) cli(args ...string) (json.RawMessage, error) {
	b, err := command(f.ctx, f.root, privateEnv(f.root), f.binary, append([]string{"--db", f.db}, args...)...)
	if err != nil {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &failure) == nil && token.MatchString(failure.Error.Code) && len(args) >= 2 {
			return nil, errors.New("cli_" + args[0] + "_" + args[1] + "_" + failure.Error.Code)
		}
		return nil, err
	}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error any             `json:"error"`
	}
	if json.Unmarshal(b, &envelope) != nil || envelope.Error != nil {
		return nil, errors.New("cli_envelope")
	}
	return envelope.Data, nil
}
func (f *fixture) setup() error {
	if _, err := os.Stat(f.db); !os.IsNotExist(err) {
		return errors.New("fixture_not_fresh")
	}
	_, err := f.cli("project", "add", "--name", "lab", "--repo", "example/lab", "--github-login", "fixture", "--git-name", "Fixture", "--git-email", "fixture@example.invalid")
	if err != nil {
		return err
	}
	for _, n := range []string{"1", "2"} {
		if _, err := f.cli("issue", "add", "--project", "lab", "--number", n); err != nil {
			return err
		}
	}
	return nil
}
func (f *fixture) summary(issue int) (service.IssueSummary, error) {
	var s service.IssueSummary
	b, err := f.cli("issue", "summary", "--project", "lab", "--number", strconv.Itoa(issue))
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}
func (f *fixture) events(issue int) ([]core.ActivityEvent, []core.ActivitySegment, error) {
	db, err := store.OpenReadOnly(f.db)
	if err != nil {
		return nil, nil, errors.New("evidence_store")
	}
	defer db.Close()
	return db.WithReadContext(f.ctx).IssueActivity("lab", issue)
}
func (f *fixture) parity(result *Result, issue int) error {
	before, err := os.ReadFile(f.db)
	if err != nil {
		return errors.New("parity_database")
	}
	b, err := f.cli("issue", "summary", "--project", "lab", "--number", strconv.Itoa(issue))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
	defer cancel()
	c := exec.Command(f.binary, "--db", f.db, "mcp", "serve")
	c.Dir = f.root
	c.Env = privateEnv(f.root)
	client := mcp.NewClient(&mcp.Implementation{Name: "laboratory", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: c, TerminateDuration: time.Second}, nil)
	if err != nil {
		return errors.New("mcp_connect")
	}
	defer session.Close()
	value, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "issue_summary", Arguments: map[string]any{"project": "lab", "number": issue}})
	if err != nil || value.IsError {
		return errors.New("mcp_summary")
	}
	raw, _ := json.Marshal(value.StructuredContent)
	var envelope struct {
		Data any `json:"data"`
	}
	var cli any
	if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(b, &cli) != nil {
		return errors.New("mcp_decode")
	}
	if err := result.check("cli_mcp_parity", reflect.DeepEqual(cli, envelope.Data), 0, 0); err != nil {
		return err
	}
	if err := session.Close(); err != nil {
		return errors.New("mcp_close")
	}
	after, err := os.ReadFile(f.db)
	if err != nil {
		return errors.New("parity_database")
	}
	return result.check("read_only_summary", bytes.Equal(before, after), 0, 0)
}

func contracts(f *fixture, r *Result) error {
	s, err := f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("initial_unestimated", s.Initial == nil, 0, 0); err != nil {
		return err
	}
	id := strconv.FormatInt(s.Issue.ID, 10)
	for _, kind := range []string{"initial", "revision", "final"} {
		b, err := f.cli("issue", "propose", "--issue-id", id, "--kind", kind, "--score", "5", "--proposer", "fixture-agent", "--rationale", "Fixture persistence contract")
		if err != nil {
			return err
		}
		var proposal core.Estimate
		if json.Unmarshal(b, &proposal) != nil {
			return errors.New("estimate_decode")
		}
		args := []string{"issue", "confirm", "--proposal-id", strconv.FormatInt(proposal.ID, 10), "--human", "fixture-human-declaration", "--rationale", "Synthetic contract fixture, not human presence"}
		if _, err := f.cli(args...); err == nil {
			return errors.New("confirmation_gate_missing")
		}
		// Explicitly synthetic fixture data, never evidence of actual user consent.
		if _, err := f.cli(append(args, "--human-confirmed")...); err != nil {
			return err
		}
	}
	for _, outcome := range []string{"completed", "reopened", "cancelled", "reopened"} {
		if _, err := f.cli("issue", "outcome", "--issue-id", id, "--outcome", outcome, "--actor", "fixture", "--rationale", "Synthetic outcome contract"); err != nil {
			return err
		}
	}
	if _, err := f.cli("issue", "link", "--issue-id", id, "--pr", "7"); err != nil {
		return err
	}
	other, err := f.summary(2)
	if err != nil {
		return err
	}
	if _, err := f.cli("issue", "link", "--issue-id", strconv.FormatInt(other.Issue.ID, 10), "--pr", "7"); err == nil {
		return errors.New("pr_owner_reassigned")
	}
	s, err = f.summary(1)
	if err != nil {
		return err
	}
	if err := r.check("estimate_history", s.Initial != nil && s.Final != nil && len(s.Confirmations) == 3 && s.State == "reopened", int64(len(s.Confirmations)), 3); err != nil {
		return err
	}
	return f.parity(r, 1)
}
