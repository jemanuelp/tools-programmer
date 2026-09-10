package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/mcpserver"
	"tools-programmer/internal/store"
)

func TestIssueCLIAndMCPSummaryShareSnapshotSemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "history.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "r@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	i, err := db.AddIssue("demo", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.ProposeEstimate(i.ID, "initial", 3, "agent", "bounded effort")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmEstimate(p.ID, "human", "accepted"); err != nil {
		t.Fatal(err)
	}
	for _, e := range []core.ActivityEvent{
		{ID: "bind", Runtime: "r", Session: "s", Segment: "x", Kind: "bind", At: 100, Project: "demo", Issue: 1},
		{ID: "start", Runtime: "r", Session: "s", Segment: "x", Kind: "start", At: 100, Activity: "t", Category: "tool"},
		{ID: "stop", Runtime: "r", Session: "s", Segment: "x", Kind: "stop", At: 700, Activity: "t", Category: "tool"},
	} {
		if err = db.RecordActivity(e); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	before, _ := os.ReadFile(path)
	var out, diagnostic bytes.Buffer
	if code := RunWithIO(context.Background(), []string{"--db", path, "issue", "summary", "--project", "demo", "--number", "1"}, io.NopCloser(strings.NewReader("")), &out, &diagnostic, forbiddenRunner{}); code != 0 {
		t.Fatal(code, out.String())
	}
	var cliResult struct {
		Data any `json:"data"`
	}
	if err = json.Unmarshal(out.Bytes(), &cliResult); err != nil {
		t.Fatal(err)
	}
	db, err = store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, b := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- mcpserver.New(db).Run(ctx, a) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "issue-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "issue_summary", Arguments: map[string]any{"project": "demo", "number": 1}})
	if err != nil || result.IsError {
		t.Fatalf("%+v %v", result, err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	var mcpResult struct {
		Data any `json:"data"`
	}
	json.Unmarshal(raw, &mcpResult)
	if !reflect.DeepEqual(cliResult.Data, mcpResult.Data) {
		t.Fatalf("CLI/MCP mismatch: %s %s", out.Bytes(), raw)
	}
	session.Close()
	cancel()
	<-done
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("summary mutated database")
	}
}

func TestActivityInputStrictPrivacyAndBounds(t *testing.T) {
	good := `{"id":"e","runtime":"r","session":"s","segment":"x","kind":"bind","at":0}`
	if _, err := decodeActivity(strings.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		strings.Replace(good, `"at":0`, `"at":null`, 1),
		strings.Replace(good, `"at":0`, `"at":1.5`, 1),
		strings.Replace(good, `"at":0`, `"at":0,"at":1`, 1),
		strings.Replace(good, `"at":0`, `"at":0,"At":1`, 1),
		strings.Replace(good, `"at":0`, `"at":0,"prompt":"secret"`, 1),
		strings.Replace(good, `,"at":0`, "", 1),
		good + good, strings.Repeat(" ", 16385),
	} {
		if _, err := decodeActivity(strings.NewReader(input)); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	path := filepath.Join(t.TempDir(), "never-created", "db")
	var out bytes.Buffer
	if code := RunWithIO(context.Background(), []string{"--db", path, "activity", "record"}, io.NopCloser(strings.NewReader(`{"prompt":"secret"}`)), &out, &out, forbiddenRunner{}); code == 0 {
		t.Fatal("accepted payload")
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("invalid input created state")
	}
}

func TestEstimateConfirmationRequiresExplicitDeclaration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "history.db")
	var out bytes.Buffer
	if code := Run(context.Background(), []string{"--db", path, "issue", "confirm", "--proposal-id", "1", "--human", "human", "--rationale", "approved"}, &out, forbiddenRunner{}); code == 0 || !strings.Contains(out.String(), "human_confirmation") {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("missing confirmation created state")
	}
}

func TestIssueCommandsPersistAcrossInvocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "history.db")
	run := func(args ...string) json.RawMessage {
		t.Helper()
		var out bytes.Buffer
		if code := Run(context.Background(), append([]string{"--db", path}, args...), &out, forbiddenRunner{}); code != 0 {
			t.Fatal(args, out.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	run("project", "add", "--name", "demo", "--repo", "example/demo", "--github-login", "reviewer", "--git-name", "Reviewer", "--git-email", "r@example.com")
	var issue core.Issue
	json.Unmarshal(run("issue", "add", "--project", "demo", "--number", "1"), &issue)
	id := strconv.FormatInt(issue.ID, 10)
	run("issue", "observe", "--issue-id", id, "--remote-id", "I_observed")
	run("issue", "link", "--issue-id", id, "--pr", "7")
	for _, kind := range []string{"initial", "revision", "final"} {
		var p core.Estimate
		json.Unmarshal(run("issue", "propose", "--issue-id", id, "--kind", kind, "--score", "5", "--proposer", "agent", "--rationale", "relative scope"), &p)
		run("issue", "confirm", "--proposal-id", strconv.FormatInt(p.ID, 10), "--human", "human", "--rationale", "accepted", "--human-confirmed")
	}
	run("issue", "outcome", "--issue-id", id, "--outcome", "cancelled", "--actor", "human", "--rationale", "scope withdrawn")
	run("issue", "outcome", "--issue-id", id, "--outcome", "reopened", "--actor", "human", "--rationale", "scope restored")
	var h core.IssueHistory
	json.Unmarshal(run("issue", "summary", "--project", "demo", "--number", "1"), &h)
	if h.Issue.RemoteID != "I_observed" || h.Issue.RemoteObservedAt == "" || len(h.Confirmations) != 3 || h.State != "reopened" || h.Initial.Score != 5 || h.Final.Score != 5 || len(h.PRs) != 1 {
		t.Fatal(h)
	}
}
