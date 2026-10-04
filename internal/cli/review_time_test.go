package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type reviewTimeNoRemote struct{}

func (reviewTimeNoRemote) Run(context.Context, []string, []byte) ([]byte, error) {
	return nil, errors.New("unexpected GitHub access")
}

type reviewTimeEnvelope struct {
	Data  json.RawMessage `json:"data"`
	Error *core.Error     `json:"error"`
}

func runReviewTimeCLI(t *testing.T, path string, args ...string) reviewTimeEnvelope {
	t.Helper()
	fullArgs := append([]string{"--db", path}, args...)
	var output bytes.Buffer
	if code := Run(context.Background(), fullArgs, &output, reviewTimeNoRemote{}); code != 0 {
		var response reviewTimeEnvelope
		if err := json.Unmarshal(output.Bytes(), &response); err != nil {
			t.Fatalf("CLI output is not an envelope (exit %d): %s: %v", code, output.String(), err)
		}
		return response
	}
	var response reviewTimeEnvelope
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("CLI output is not an envelope: %s: %v", output.String(), err)
	}
	return response
}

func TestReviewTimeCLIRecordsReplaysAndReportsPublicHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "history.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddProject(core.Project{Name: "reviewcli", Repo: "example/reviewcli", ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	knownArgs := []string{"review-time", "record", "--project", "reviewcli", "--entry-id", "known-one", "--reviewer", "Casey", "--duration", "25m", "--issue-number", "42", "--pr", "7", "--sha", sha}
	firstResponse := runReviewTimeCLI(t, path, knownArgs...)
	if firstResponse.Error != nil {
		t.Fatalf("manual review-time entry failed: %+v", firstResponse.Error)
	}
	var first core.ReviewTimeEntry
	if err := json.Unmarshal(firstResponse.Data, &first); err != nil {
		t.Fatal(err)
	}
	if first.DurationMS != 1500000 || first.Source != "manual" || first.RecordedAt == "" {
		t.Fatalf("recorded entry lost declared duration or provenance: %+v", first)
	}
	replayResponse := runReviewTimeCLI(t, path, knownArgs...)
	var replay core.ReviewTimeEntry
	if replayResponse.Error != nil || json.Unmarshal(replayResponse.Data, &replay) != nil || replay.RecordedAt != first.RecordedAt {
		t.Fatalf("public replay did not return the original entry: %+v %+v", replay, replayResponse.Error)
	}
	conflictArgs := append([]string(nil), knownArgs...)
	for i := range conflictArgs {
		if conflictArgs[i] == "25m" {
			conflictArgs[i] = "26m"
			break
		}
	}
	conflict := runReviewTimeCLI(t, path, conflictArgs...)
	if conflict.Error == nil || conflict.Error.Code != "review_time_conflict" {
		t.Fatalf("changed payload did not return an identity conflict: %+v", conflict.Error)
	}

	// Distinct IDs with equal attributes both count; omitted issue/PR/SHA remains
	// project-only rather than being inferred from another entry.
	for _, args := range [][]string{
		{"review-time", "record", "--project", "reviewcli", "--entry-id", "known-two", "--reviewer", "Casey", "--duration", "25m", "--issue-number", "42", "--pr", "7", "--sha", sha},
		{"review-time", "record", "--project", "reviewcli", "--entry-id", "unknown-sha", "--reviewer", "Casey", "--duration", "7m", "--issue-number", "42", "--pr", "7"},
		{"review-time", "record", "--project", "reviewcli", "--entry-id", "project-only", "--reviewer", "Casey", "--duration", "3m"},
		{"review-time", "record", "--project", "reviewcli", "--entry-id", "local-sha", "--reviewer", "Casey", "--duration", "5m", "--issue-number", "43", "--sha", strings.Repeat("b", 40)},
	} {
		response := runReviewTimeCLI(t, path, args...)
		if response.Error != nil {
			t.Fatalf("additional manual record failed: %+v", response.Error)
		}
	}

	response := runReviewTimeCLI(t, path, "review-time", "report", "--project", "reviewcli")
	if response.Error != nil {
		t.Fatalf("project report failed: %+v", response.Error)
	}
	var report core.ReviewTimeReport
	if err := json.Unmarshal(response.Data, &report); err != nil {
		t.Fatal(err)
	}
	if report.HumanReviewMS != 3900000 || report.LocalHumanReviewMS != 480000 || report.PRHumanReviewMS != 3420000 || report.AgentReviewMS != nil || report.ElapsedReviewMS != nil || report.AutomaticCoverage != "pending" || len(report.Buckets) != 4 {
		t.Fatalf("public report combined or overstated separate measures: %+v", report)
	}
	var known, unknown, projectOnly, localKnown bool
	for _, bucket := range report.Buckets {
		switch {
		case bucket.ReviewType == "pr" && bucket.SHA == sha:
			known = bucket.HumanReviewMS == 3000000 && bucket.EntryCount == 2
		case bucket.ReviewType == "pr" && bucket.SHA == "unknown":
			unknown = bucket.HumanReviewMS == 420000
		case bucket.ReviewType == "local" && bucket.IssueNumber == nil && bucket.PR == nil:
			projectOnly = bucket.HumanReviewMS == 180000
		case bucket.ReviewType == "local" && bucket.IssueNumber != nil && *bucket.IssueNumber == 43 && bucket.SHA == strings.Repeat("b", 40):
			localKnown = bucket.HumanReviewMS == 300000
		}
	}
	if !known || !unknown || !projectOnly || !localKnown {
		t.Fatalf("public report lost known, unknown, local, or unassigned scope: %+v", report.Buckets)
	}

	filtered := runReviewTimeCLI(t, path, "review-time", "report", "--project", "reviewcli", "--pr", "7")
	if filtered.Error != nil {
		t.Fatal(filtered.Error)
	}
	var prReport core.ReviewTimeReport
	if err := json.Unmarshal(filtered.Data, &prReport); err != nil {
		t.Fatal(err)
	}
	if prReport.HumanReviewMS != 3420000 || len(prReport.Buckets) != 2 {
		t.Fatalf("PR report included unrelated or lost revision effort: %+v", prReport)
	}
	badDuration := runReviewTimeCLI(t, path, "review-time", "record", "--project", "reviewcli", "--entry-id", "sub-ms", "--reviewer", "Casey", "--duration", "1500ns")
	if badDuration.Error == nil || badDuration.Error.Code != "invalid_input" {
		t.Fatalf("sub-millisecond duration was accepted: %+v", badDuration.Error)
	}
	zeroFilter := runReviewTimeCLI(t, path, "review-time", "report", "--project", "reviewcli", "--issue-number", "0")
	if zeroFilter.Error == nil || zeroFilter.Error.Code != "invalid_input" {
		t.Fatalf("explicit zero issue filter was accepted: %+v", zeroFilter.Error)
	}
}
