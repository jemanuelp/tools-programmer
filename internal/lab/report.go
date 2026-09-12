package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Check struct {
	Label    string `json:"label"`
	OK       bool   `json:"ok"`
	Value    int64  `json:"value,omitempty"`
	Expected int64  `json:"expected,omitempty"`
}
type Result struct {
	Scenario  string             `json:"scenario"`
	Status    string             `json:"status"`
	Code      string             `json:"code,omitempty"`
	Checks    []Check            `json:"checks"`
	ElapsedMS int64              `json:"elapsed_ms"`
	History   *HistoryComparison `json:"history,omitempty"`
}

type HistoryComparison struct {
	Check         string   `json:"check"`
	BeforeHash    string   `json:"before_hash"`
	AfterHash     string   `json:"after_hash"`
	ChangedFields []string `json:"changed_fields"`
}

func historyCategory(key string) string {
	switch key {
	case "issue", "prs", "proposals", "confirmations", "outcomes", "initial", "final", "state", "activity", "segments", "activity_events", "bindings":
		return key
	default:
		return "other"
	}
}
func compareHistory(r *Result, label string, before, after []byte) error {
	equal := bytes.Equal(before, after)
	if !equal {
		var a, b map[string]json.RawMessage
		categories := map[string]bool{}
		if json.Unmarshal(before, &a) != nil || json.Unmarshal(after, &b) != nil {
			categories["other"] = true
		} else {
			for k, v := range a {
				if !bytes.Equal(v, b[k]) {
					categories[historyCategory(k)] = true
				}
			}
			for k, v := range b {
				if !bytes.Equal(v, a[k]) {
					categories[historyCategory(k)] = true
				}
			}
		}
		fields := []string{}
		for category := range categories {
			fields = append(fields, category)
		}
		sort.Strings(fields)
		r.History = &HistoryComparison{Check: label, BeforeHash: digest(before), AfterHash: digest(after), ChangedFields: fields}
	}
	return r.check(label, equal, 0, 0)
}

func validHistoryComparison(r Result) bool {
	h := r.History
	if h == nil {
		return true
	}
	if r.Status != "fail" || !hash.MatchString(h.BeforeHash) || !hash.MatchString(h.AfterHash) || h.BeforeHash == h.AfterHash || len(h.ChangedFields) > 13 {
		return false
	}
	switch h.Check {
	case "issue_a_history_stable", "issue_a_records_stable", "issue_a_records_preserved_during_switch":
	default:
		return false
	}
	for _, field := range h.ChangedFields {
		if historyCategory(field) != field {
			return false
		}
	}
	for _, check := range r.Checks {
		if check.Label == h.Check && !check.OK {
			return true
		}
	}
	return false
}

type Report struct {
	Version          int      `json:"version"`
	Run              string   `json:"run"`
	Commit           string   `json:"commit"`
	SourceHash       string   `json:"source_hash"`
	InputHash        string   `json:"input_hash"`
	StartedAt        string   `json:"started_at"`
	Dirty            bool     `json:"dirty"`
	BinaryHash       string   `json:"binary_hash"`
	Image            string   `json:"image"`
	OpenCode         string   `json:"opencode"`
	Go               string   `json:"go"`
	Node             string   `json:"node"`
	VersionsVerified bool     `json:"versions_verified"`
	Remaining        []string `json:"remaining"`
	Results          []Result `json:"results"`
	Cleaned          bool     `json:"cleaned"`
	Startup          *Startup `json:"startup,omitempty"`
	ProtocolError    string   `json:"protocol_error,omitempty"`
}

// Startup is shared execution evidence, not a fabricated scenario result.
// Expected source identity comes from the host until source_verify succeeds.
type Startup struct {
	Stage           string `json:"stage"`
	Status          string `json:"status"`
	Cause           string `json:"cause,omitempty"`
	ExitCode        int    `json:"exit_code,omitempty"`
	ProcessExitCode *int   `json:"process_exit_code,omitempty"`
	ElapsedMS       int64  `json:"elapsed_ms"`
	Truncated       bool   `json:"truncated"`
}

func validStartup(s *Startup) bool {
	if s == nil {
		return true
	}
	switch s.Stage {
	case "bootstrap", "source_wait", "source_verify", "lab_build", "lab_verify", "reexec", "guest_entry", "versions", "product_build", "product_verify", "complete":
	default:
		return false
	}
	switch s.Cause {
	case "", "nonroot_required", "configuration_invalid", "source_invalid", "source_transfer", "build_failed", "permission_denied", "read_only_filesystem", "storage_capacity", "dependency_unavailable", "deadline", "cancelled", "exec_denied", "exec_failed", "container_exit", "protocol_error", "version_mismatch":
	default:
		return false
	}
	if s.ProcessExitCode != nil && (*s.ProcessExitCode < -1 || *s.ProcessExitCode > 255) {
		return false
	}
	if s.Status == "failed" && s.Cause == "" {
		return false
	}
	if s.Status == "complete" && (s.Stage != "complete" || s.Cause != "" || s.ExitCode != 0) {
		return false
	}
	return (s.Status == "running" || s.Status == "failed" || s.Status == "complete") && s.ElapsedMS >= 0 && s.ExitCode >= -1 && s.ExitCode <= 255
}

func startupReport() Report {
	return Report{Version: 1, Run: os.Getenv("LAB_RUN"), Commit: os.Getenv("LAB_COMMIT"), SourceHash: os.Getenv("LAB_SOURCE_HASH"), InputHash: os.Getenv("LAB_INPUT_HASH"), StartedAt: time.Now().UTC().Format(time.RFC3339Nano), OpenCode: "1.18.30", Go: "go1.26.8", Node: "v24.18.0", Results: []Result{}}
}
func emitReport(out io.Writer, r Report) {
	b, _ := json.Marshal(r)
	fmt.Fprintf(out, "LAB_REPORT %s\n", b)
}
func startupStage(r *Report, stage string, started time.Time, out io.Writer) {
	r.Startup = &Startup{Stage: stage, Status: "running", ElapsedMS: time.Since(started).Milliseconds()}
	emitReport(out, *r)
}
func startupFailed(r *Report, cause string, processExit *int, truncated bool, started time.Time, out io.Writer) {
	r.Startup.Status = "failed"
	r.Startup.Cause = cause
	r.Startup.ExitCode = 1
	r.Startup.ProcessExitCode = processExit
	r.Startup.Truncated = truncated
	r.Startup.ElapsedMS = time.Since(started).Milliseconds()
	emitReport(out, *r)
}
func fillNotRun(r *Report, selected []string) {
	code := "run_interrupted"
	if r.Startup != nil && r.Startup.Status != "complete" {
		code = "startup_failed"
	}
	for len(r.Results) < len(selected) {
		r.Results = append(r.Results, Result{Scenario: selected[len(r.Results)], Status: "not_run", Code: code, Checks: []Check{}})
	}
}

func (r *Result) check(label string, ok bool, value, expected int64) error {
	r.Checks = append(r.Checks, Check{label, ok, value, expected})
	if !ok {
		return errors.New(label)
	}
	return nil
}
func (r Report) ExitCode() int {
	if r.ProtocolError != "" {
		return 1
	}
	if r.Startup != nil && r.Startup.Status != "complete" {
		return 1
	}
	if !r.Cleaned || len(r.Results) == 0 {
		return 1
	}
	for _, s := range r.Results {
		if s.Status != "pass" {
			return 1
		}
	}
	return 0
}

var token = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,79}$`)
var hash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commitHash = regexp.MustCompile(`^[0-9a-f]{40}$`)

func inputHash(scenarios []string, interactive bool, timeout time.Duration) string {
	b, _ := json.Marshal(struct {
		Scenarios   []string
		Interactive bool
		TimeoutMS   int64
	}{scenarios, interactive, timeout.Milliseconds()})
	return digest(b)
}

// DecodeReport rejects unknown fields rather than transporting runtime logs.
// All diagnostic strings must be lab-owned symbolic codes, not error messages.
func DecodeReport(in io.Reader, expectedRun, expectedSource string) (Report, error) {
	var r Report
	d := json.NewDecoder(io.LimitReader(in, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return r, errors.New("report_schema")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return r, errors.New("report_trailing")
	}
	if r.Version != 1 || r.Run != expectedRun || r.SourceHash != expectedSource || !hash.MatchString(r.SourceHash) || !commitHash.MatchString(r.Commit) || (r.BinaryHash != "" && !hash.MatchString(r.BinaryHash)) {
		return r, errors.New("report_identity")
	}
	if !hash.MatchString(r.InputHash) {
		return r, errors.New("report_input_identity")
	}
	if _, err := time.Parse(time.RFC3339Nano, r.StartedAt); err != nil {
		return r, errors.New("report_timestamp")
	}
	if r.OpenCode != "1.18.30" || r.Go != "go1.26.8" || r.Node != "v24.18.0" {
		return r, errors.New("report_versions")
	}
	if len(r.Results) > 32 {
		return r, errors.New("report_bound")
	}
	if !validStartup(r.Startup) {
		return r, errors.New("report_startup")
	}
	if r.ProtocolError != "" && r.ProtocolError != "protocol_error" {
		return r, errors.New("report_protocol_error")
	}
	for _, s := range r.Results {
		if r.Startup != nil && r.Startup.Status != "complete" && s.Status != "not_run" {
			return r, errors.New("report_execution_before_startup")
		}
		if !validHistoryComparison(s) {
			return r, errors.New("report_history_comparison")
		}
		if !knownScenario(s.Scenario) || !token.MatchString(s.Code) && s.Code != "" || len(s.Checks) > 128 {
			return r, errors.New("report_result")
		}
		if s.Status != "pass" && s.Status != "fail" && s.Status != "blocked" && s.Status != "unsupported" && s.Status != "not_run" {
			return r, errors.New("report_status")
		}
		if s.Status == "not_run" && (len(s.Checks) != 0 || s.ElapsedMS != 0) {
			return r, errors.New("report_not_run")
		}
		if s.Status == "pass" && r.Startup != nil && r.Startup.Status != "complete" {
			return r, errors.New("report_pass_during_startup")
		}
		if s.Status == "pass" && (len(s.Checks) == 0 || !hash.MatchString(r.BinaryHash) || !r.VersionsVerified) {
			return r, errors.New("report_pass_without_evidence")
		}
		for _, c := range s.Checks {
			if !token.MatchString(c.Label) || s.Status == "pass" && !c.OK {
				return r, errors.New("report_check")
			}
		}
	}
	for _, gap := range r.Remaining {
		switch gap {
		case "two_fresh_human_runs", "human_wait_acceptance", "cancellation_restart_acceptance", "active_queued_reuse", "paid_provider", "openchamber_gui":
		default:
			return r, errors.New("report_gap")
		}
	}
	return r, nil
}

func decodeSelectedReport(frame []byte, run, source, input string, selected []string) (Report, error) {
	r, err := DecodeReport(bytes.NewReader(frame), run, source)
	if err != nil {
		return r, err
	}
	if r.InputHash != input {
		return r, errors.New("report_input_identity")
	}
	if len(r.Results) > len(selected) {
		return r, errors.New("report_selection")
	}
	for i, result := range r.Results {
		if result.Scenario != selected[i] {
			return r, errors.New("report_selection")
		}
	}
	return r, nil
}

func WriteReport(dir string, r Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(b, '\n'), 0600); err != nil {
		return err
	}
	text := fmt.Sprintf("# Integration laboratory\n\nRun `%s`; source `%s`.\n\n", r.Run, r.SourceHash)
	if r.ProtocolError != "" {
		text += fmt.Sprintf("Report stream: **failed**, cause `%s`.\n\n", r.ProtocolError)
	}
	if r.Startup != nil {
		text += fmt.Sprintf("Startup: **%s**, stage `%s`, cause `%s`, exit %d, elapsed %d ms.\n\n", r.Startup.Status, r.Startup.Stage, r.Startup.Cause, r.Startup.ExitCode, r.Startup.ElapsedMS)
	}
	for _, s := range r.Results {
		text += fmt.Sprintf("- **%s**: %s (%s), %d ms\n", s.Scenario, s.Status, s.Code, s.ElapsedMS)
		if s.History != nil {
			text += fmt.Sprintf("  - %s: `%s` → `%s`; changed categories: %s (not a cause determination)\n", s.History.Check, s.History.BeforeHash, s.History.AfterHash, strings.Join(s.History.ChangedFields, ", "))
		}
		for _, c := range s.Checks {
			text += fmt.Sprintf("  - %s: %t (%d; expected %d)\n", c.Label, c.OK, c.Value, c.Expected)
		}
	}
	text += "\nFixture policy is not human consent. Server evidence does not certify OpenChamber. Issues #3–#5 remain separately accepted.\n"
	for _, gap := range r.Remaining {
		text += "- Remaining: `" + gap + "`\n"
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(text), 0600)
}
