package lab

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHostCheckpointSurvivesInvalidFrames(t *testing.T) {
	for _, mode := range []string{"malformed", "stale", "source", "input", "selection", "oversized", "partial", "partial_only", "no_valid", "cumulative", "startup_failure", "invalid_then_valid"} {
		t.Run(mode, func(t *testing.T) {
			d := &lifecycleDocker{frames: func(out io.Writer, r Report) {
				if mode == "startup_failure" {
					r.Startup = &Startup{Stage: "reexec", Status: "failed", Cause: "exec_denied", ExitCode: 1}
					r.Results = nil
				}
				if mode == "cumulative" {
					prefix := r
					prefix.Results = nil
					prefix.Startup = &Startup{Stage: "product_build", Status: "running"}
					emitReport(out, prefix)
				}
				if mode != "no_valid" && mode != "partial_only" {
					emitReport(out, r)
				}
				bad := r
				switch mode {
				case "stale":
					bad.Run = "stale"
				case "source":
					bad.SourceHash = strings.Repeat("b", 64)
				case "input":
					bad.InputHash = strings.Repeat("b", 64)
				case "selection":
					bad.Results = []Result{{Scenario: "silent", Status: "fail", Code: "sentinel"}}
				}
				b, _ := json.Marshal(bad)
				frame := "LAB_REPORT " + string(b) + "\n"
				switch mode {
				case "malformed", "no_valid", "startup_failure", "invalid_then_valid":
					frame = "LAB_REPORT {SENTINEL_SECRET /private/path\n"
				case "oversized":
					frame = "LAB_REPORT " + string(b) + strings.Repeat(" ", 1<<20) + "SENTINEL_SECRET\n"
				case "partial", "partial_only":
					frame = "LAB_REPORT " + string(b)
				}
				// Split prefixes, JSON and terminators across writes.
				for len(frame) > 0 {
					n := min(73, len(frame))
					io.WriteString(out, frame[:n])
					frame = frame[n:]
				}
				if mode == "invalid_then_valid" {
					emitReport(out, r)
				}
			}}
			out := filepath.Join(t.TempDir(), "report")
			r, err := runHost(context.Background(), HostOptions{Source: snapshotFixture(t), Output: out, Scenarios: []string{"contracts"}, Timeout: time.Minute}, d)
			if mode == "cumulative" || mode == "partial" {
				if err != nil || r.ExitCode() != 0 {
					t.Fatalf("normal stream: %+v %v", r, err)
				}
			} else if err == nil || r.ExitCode() == 0 {
				t.Fatalf("corruption masked: %+v %v", r, err)
			}
			if mode != "no_valid" && mode != "partial_only" && mode != "startup_failure" && (len(r.Results) != 1 || r.Results[0].Status != "pass" || len(r.Results[0].Checks) == 0) {
				t.Fatalf("checkpoint lost: %+v", r)
			}
			if mode == "startup_failure" && (r.Startup.Cause != "exec_denied" || r.Results[0].Status != "not_run") {
				t.Fatal("primary failure lost", r)
			}
			if mode == "no_valid" && r.Results[0].Status != "not_run" {
				t.Fatal("invented execution", r)
			}
			b, e := os.ReadFile(filepath.Join(out, "report.json"))
			if e != nil {
				t.Fatal(e)
			}
			decoded, e := DecodeReport(strings.NewReader(string(b)), r.Run, r.SourceHash)
			if e != nil || decoded.ExitCode() != r.ExitCode() {
				t.Fatalf("persisted failure lost: %s %v", b, e)
			}
			if strings.Contains(string(b), "SENTINEL") || strings.Contains(string(b), "/private/path") {
				t.Fatal("raw output exported")
			}
			if !r.Cleaned || d.calls[len(d.calls)-1][2] != strings.Repeat("e", 64) {
				t.Fatal("cleanup changed")
			}
		})
	}
}

func TestCheckpointPrefixFillsOnlyUntouchedSelection(t *testing.T) {
	d := &lifecycleDocker{frames: func(out io.Writer, r Report) {
		r.Startup = &Startup{Stage: "complete", Status: "complete"}
		emitReport(out, r)
		io.WriteString(out, "LAB_REPORT invalid\n")
	}}
	out := filepath.Join(t.TempDir(), "report")
	r, err := runHost(context.Background(), HostOptions{Source: snapshotFixture(t), Output: out, Scenarios: []string{"contracts", "silent"}, Timeout: time.Minute}, d)
	if err == nil || len(r.Results) != 2 || r.Results[0].Status != "pass" || r.Results[1].Status != "not_run" || r.Results[1].Code != "run_interrupted" || r.Startup.Status != "complete" {
		t.Fatalf("prefix altered: %+v %v", r, err)
	}
	if r.ProtocolError != "protocol_error" || r.ExitCode() == 0 {
		t.Fatal("protocol failure not persisted", r)
	}
	b, err := os.ReadFile(filepath.Join(out, "report.md"))
	if err != nil || !strings.Contains(string(b), "protocol_error") {
		t.Fatalf("failure invisible in Markdown: %s %v", b, err)
	}
}

func TestReportStreamBoundAndDiscardRecovery(t *testing.T) {
	r := sampleReport()
	p := &promptWriter{out: io.Discard, decode: func(frame []byte) (Report, error) {
		return decodeSelectedReport(frame, r.Run, r.SourceHash, r.InputHash, []string{"contracts"})
	}}
	emitReport(p, r)
	checkpoint := string(p.report)
	io.WriteString(p, "LAB_REPORT ")
	for i := 0; i < 40; i++ {
		io.WriteString(p, strings.Repeat("x", 64<<10))
	}
	if len(p.line) > 1<<20 || len(p.report) > 1<<20 {
		t.Fatal("unbounded report memory")
	}
	io.WriteString(p, "\n")
	if string(p.report) != checkpoint || p.protocolErr == nil || len(p.line) != 0 {
		t.Fatal("oversized frame promoted or retained")
	}
	r.Results[0].Status = "fail"
	r.Results[0].Code = "later_failure"
	emitReport(p, r)
	latest, err := p.decode(p.report)
	if err != nil || latest.Results[0].Code != "later_failure" || p.protocolErr == nil {
		t.Fatal("valid recovery erased stream failure", latest, err)
	}
	latest.ProtocolError = "SENTINEL_SECRET"
	b, _ := json.Marshal(latest)
	if _, err := DecodeReport(strings.NewReader(string(b)), r.Run, r.SourceHash); err == nil {
		t.Fatal("unsafe protocol diagnostic accepted")
	}
}
