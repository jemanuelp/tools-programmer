package lab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sampleReport() Report {
	return Report{Version: 1, Run: strings.Repeat("a", 32), Commit: strings.Repeat("b", 40), SourceHash: strings.Repeat("c", 64), InputHash: inputHash([]string{"contracts"}, false, time.Minute), StartedAt: "2026-09-12T00:00:00Z", BinaryHash: strings.Repeat("d", 64), OpenCode: "1.18.30", Go: "go1.26.8", Node: "v24.18.0", VersionsVerified: true, Results: []Result{{Scenario: "contracts", Status: "pass", Checks: []Check{{Label: "cli_mcp_parity", OK: true}}}}, Cleaned: true}
}
func TestReportRejectsRawPayloadsAndStaleIdentity(t *testing.T) {
	r := sampleReport()
	b, _ := json.Marshal(r)
	for _, bad := range []string{strings.Replace(string(b), `"version":1`, `"prompt":"secret","version":1`, 1), strings.Replace(string(b), `"run":"`+r.Run+`"`, `"run":"old"`, 1), strings.Replace(string(b), `"cli_mcp_parity"`, `"/home/user/secret"`, 1), string(b) + `{}`} {
		if _, err := DecodeReport(strings.NewReader(bad), r.Run, r.SourceHash); err == nil {
			t.Fatal("unsafe report accepted")
		}
	}
	if _, err := DecodeReport(strings.NewReader(string(b)), r.Run, r.SourceHash); err != nil {
		t.Fatal(err)
	}
}
func TestNonpassesAndCleanupNeverCountAsCoverage(t *testing.T) {
	for _, status := range []string{"fail", "blocked", "unsupported"} {
		r := sampleReport()
		r.Results[0].Status = status
		if r.ExitCode() == 0 {
			t.Fatal(status)
		}
	}
	r := sampleReport()
	r.Cleaned = false
	if r.ExitCode() == 0 {
		t.Fatal("cleanup failure passed")
	}
}
func TestReportsArePrivateAndFirstFailureIsRetained(t *testing.T) {
	r := sampleReport()
	r.Results[0].Status = "fail"
	r.Results[0].Code = "provider_failed_tool"
	dir := t.TempDir()
	if err := WriteReport(dir, r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"report.json", "report.md"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal(st, err)
		}
	}
	if r.Results[0].Code != "provider_failed_tool" {
		t.Fatal("failure overwritten")
	}
}

func TestStartupFailurePreservesNotRunSemantics(t *testing.T) {
	r := sampleReport()
	b, _ := json.Marshal(r)
	var data map[string]any
	json.Unmarshal(b, &data)
	data["binary_hash"] = ""
	data["versions_verified"] = false
	data["startup"] = map[string]any{"stage": "reexec", "status": "failed", "cause": "exec_denied", "exit_code": 1, "elapsed_ms": 35440, "truncated": false}
	data["results"] = []any{map[string]any{"scenario": "contracts", "status": "not_run", "code": "startup_failed", "checks": []any{}, "elapsed_ms": 0}}
	b, _ = json.Marshal(data)
	got, err := DecodeReport(strings.NewReader(string(b)), r.Run, r.SourceHash)
	if err != nil {
		t.Fatal("startup diagnostic rejected", err)
	}
	if got.ExitCode() == 0 || got.Results[0].Status != "not_run" || len(got.Results[0].Checks) != 0 {
		t.Fatal("startup failure fabricated execution")
	}
}

func TestStartupReportRejectsUntrustedDiagnosticStrings(t *testing.T) {
	for _, bad := range []string{"sentinel_secret", "/private/source", "untrusted stdout"} {
		r := sampleReport()
		r.Results = nil
		r.Startup = &Startup{Stage: "lab_build", Status: "failed", Cause: bad, ExitCode: 1}
		b, _ := json.Marshal(r)
		if _, err := DecodeReport(strings.NewReader(string(b)), r.Run, r.SourceHash); err == nil {
			t.Fatal("untrusted startup cause accepted")
		}
	}
}

func TestHistoryMismatchReportsOnlyHashesAndAllowlistedCategories(t *testing.T) {
	r := sampleReport()
	result := Result{Scenario: "switch-reuse", Status: "fail", Code: "issue_a_history_stable"}
	before := []byte(`{"activity":{"unknown_gaps":[]},"SENTINEL_PRIVATE_FIELD":"/private/secret"}`)
	after := []byte(`{"activity":{"unknown_gaps":[{"secret":"SENTINEL_SECRET"}]},"SENTINEL_PRIVATE_FIELD":"changed"}`)
	if err := compareHistory(&result, "issue_a_history_stable", before, after); err == nil {
		t.Fatal("mismatch accepted")
	}
	r.Results = []Result{result}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"SENTINEL", "/private", "unknown_gaps"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("private history leaked")
		}
	}
	if result.History.BeforeHash != digest(before) || result.History.AfterHash != digest(after) || strings.Join(result.History.ChangedFields, ",") != "activity,other" {
		t.Fatal(result.History)
	}
	if _, err := DecodeReport(strings.NewReader(string(b)), r.Run, r.SourceHash); err != nil {
		t.Fatal(err)
	}
	r.Results[0].History.ChangedFields = []string{"SENTINEL_PRIVATE_FIELD"}
	b, _ = json.Marshal(r)
	if _, err := DecodeReport(strings.NewReader(string(b)), r.Run, r.SourceHash); err == nil {
		t.Fatal("untrusted category accepted")
	}
}
