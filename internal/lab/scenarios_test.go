package lab

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReuseHistoryBookmarksAfterParentSwitch(t *testing.T) {
	for _, mutateA := range []bool{false, true} {
		t.Run(map[bool]string{false: "switch_additions_then_only_b", true: "b_mutates_bookmarked_a"}[mutateA], func(t *testing.T) {
			old := json.RawMessage(`{"activity":{"active_elapsed_ms":1000},"segments":[]}`)
			current := append(json.RawMessage(nil), old...)
			records := issueRecords{Events: map[string]json.RawMessage{"original": json.RawMessage(`{"at":10}`)}, Bindings: map[string]json.RawMessage{"original": json.RawMessage(`{"issue_id":1}`)}}
			read := func() (issueHistorySnapshot, error) {
				b, _ := json.Marshal(issueHistorySnapshot{Summary: current, Records: records})
				var snapshot issueHistorySnapshot
				err := json.Unmarshal(b, &snapshot)
				return snapshot, err
			}
			switchParent := func() error {
				current = json.RawMessage(`{"activity":{"active_elapsed_ms":1010,"unknown_gaps":[{"reason":"uncertain_inference"}]},"segments":[]}`)
				records.Events["switch-addition"] = json.RawMessage(`{"at":20}`)
				return nil
			}
			reuseChild := func() error {
				if mutateA {
					current = json.RawMessage(`{"activity":{"active_elapsed_ms":2010},"segments":[]}`)
				}
				return nil
			}
			r := &Result{}
			err := reuseHistory(r, read, switchParent, reuseChild)
			if mutateA {
				if err == nil {
					t.Fatal("B changed A without failure")
				}
			} else {
				if string(old) == string(current) {
					t.Fatal("fixture did not expose the old boundary error")
				}
				if err != nil {
					t.Fatal("legitimate switch-turn additions rejected", err)
				}
			}
		})
	}
}

func TestReuseHistoryPreservesRawRecordsAndBindings(t *testing.T) {
	for _, phase := range []string{"switch", "reuse"} {
		for _, kind := range []string{"event", "binding"} {
			t.Run(phase+"_"+kind, func(t *testing.T) {
				current := issueHistorySnapshot{Summary: json.RawMessage(`{"activity":{}}`), Records: issueRecords{Events: map[string]json.RawMessage{"e1": json.RawMessage(`{"at":10}`)}, Bindings: map[string]json.RawMessage{"b1": json.RawMessage(`{"issue_id":1}`)}}}
				read := func() (issueHistorySnapshot, error) {
					b, _ := json.Marshal(current)
					var copy issueHistorySnapshot
					err := json.Unmarshal(b, &copy)
					return copy, err
				}
				mutate := func() {
					if kind == "event" {
						current.Records.Events["e1"] = json.RawMessage(`{"at":20}`)
					} else {
						current.Records.Bindings["b1"] = json.RawMessage(`{"issue_id":2}`)
					}
				}
				switchParent := func() error {
					if phase == "switch" {
						mutate()
					}
					return nil
				}
				reuseChild := func() error {
					if phase == "reuse" {
						mutate()
					}
					return nil
				}
				r := &Result{}
				if err := reuseHistory(r, read, switchParent, reuseChild); err == nil {
					t.Fatal("persisted A record mutation was accepted")
				}
				if r.History == nil || r.History.BeforeHash == r.History.AfterHash {
					t.Fatal("missing mismatch hashes")
				}
			})
		}
	}
}

func TestNativeQuiescenceRequiresFullTurnNotJustBash(t *testing.T) {
	var messages []runtimeMessage
	if err := json.Unmarshal([]byte(`[{"info":{"id":"msg_user","sessionID":"ses_child","role":"user","time":{"created":10}}},{"info":{"id":"msg_assistant","sessionID":"ses_child","role":"assistant","parentID":"msg_user","finish":"tool-calls","time":{"created":11,"completed":12}},"parts":[{"type":"tool","tool":"bash","state":{"status":"completed"}}]}]`), &messages); err != nil {
		t.Fatal(err)
	}
	if nativeTurnComplete(messages, "ses_child") {
		t.Fatal("completed Bash was mistaken for native turn completion")
	}
	messages[1].Info.Finish = "stop"
	messages[1].Info.Time.Completed = 0
	if nativeTurnComplete(messages, "ses_child") {
		t.Fatal("provider stop without native completion was accepted")
	}
	messages[1].Info.Time.Completed = 12
	if !nativeTurnComplete(messages, "ses_child") {
		t.Fatal("completed native turn rejected")
	}
	messages[1].Parts[0].State.Status = "running"
	if nativeTurnComplete(messages, "ses_child") {
		t.Fatal("running tool accepted as quiescent")
	}
}

func TestHistorySettlementUsesConsecutiveQuiescentReadbacks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reads, observations := 0, 0
	quiet := func() (bool, error) { observations++; return observations > 1, nil }
	read := func() (issueHistorySnapshot, error) {
		reads++
		text := `{"activity":{"active_elapsed_ms":1}}`
		if reads > 1 {
			text = `{"activity":{"active_elapsed_ms":2}}`
		}
		return issueHistorySnapshot{Summary: json.RawMessage(text)}, nil
	}
	s, err := settleHistory(ctx, read, quiet)
	if err != nil || reads != 3 || observations != 4 || !strings.Contains(string(s.Summary), ":2") {
		t.Fatal(reads, observations, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := settleHistory(ctx, read, func() (bool, error) { return false, nil }); err == nil {
		t.Fatal("unsettled history passed")
	}
}

func TestIndependentIntervalOracle(t *testing.T) {
	for _, tt := range []struct {
		name  string
		spans []interval
		want  int64
	}{{"empty", nil, 0}, {"parallel", []interval{{10, 40}, {20, 50}}, 40}, {"disjoint", []interval{{10, 20}, {30, 40}}, 20}, {"nested", []interval{{1, 10}, {2, 3}}, 9}} {
		t.Run(tt.name, func(t *testing.T) {
			if got := unionLength(tt.spans); got != tt.want {
				t.Fatal(got, tt.want)
			}
		})
	}
}
func TestPrivateEnvironmentDoesNotInheritCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret")
	for _, e := range privateEnv(t.TempDir()) {
		if e == "OPENAI_API_KEY=secret" {
			t.Fatal("credential inherited")
		}
	}
}
func TestOwnedProcessTimeoutReapsChild(t *testing.T) {
	if testing.Short() {
		t.Skip("local subprocess integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	p, err := startProcess(ctx, t.TempDir(), []string{"PATH=" + os.Getenv("PATH")}, "sleep", "20")
	if err != nil {
		t.Fatal(err)
	}
	defer p.stop()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("orphan process")
	}
}
func TestContractsThroughRealBinaryAndPrivateMCP(t *testing.T) {
	if testing.Short() {
		t.Skip("explicit binary integration")
	}
	binary := os.Getenv("TOOLS_PROGRAMMER_TEST_BINARY")
	if binary == "" {
		t.Skip("set TOOLS_PROGRAMMER_TEST_BINARY to an explicitly built binary")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("binary path must be absolute")
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	f := &fixture{ctx: ctx, root: root, binary: binary, db: root + "/state/history.db"}
	if err := privateDirs(root); err != nil {
		t.Fatal(err)
	}
	if err := f.setup(); err != nil {
		t.Fatal(err)
	}
	r := Result{}
	if err := contracts(f, &r); err != nil {
		t.Fatal(err, r.Checks)
	}
}
