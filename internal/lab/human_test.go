package lab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type humanTransport func(*http.Request) (*http.Response, error)

func (f humanTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHumanReplyDecisionBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, input, code, status, reply string
		cancel, expire, late, transport  bool
	}{
		{name: "allow_continues", input: "allow\n", status: "fail", reply: "once"},
		{name: "explicit_deny", input: "deny\n", status: "blocked", code: "human_declined", reply: "reject"},
		{name: "deadline_without_answer", expire: true, status: "blocked", code: "human_decision_timeout"},
		{name: "cancel_without_answer", cancel: true, status: "blocked", code: "human_decision_cancelled"},
		{name: "cancel_after_allow_before_reply", late: true, input: "allow\n", status: "blocked", code: "human_decision_cancelled"},
		{name: "transport_failure_after_allow", input: "allow\n", transport: true, status: "fail", code: "runtime_transport", reply: "once"},
		{name: "transport_failure_after_deny", input: "deny\n", transport: true, status: "fail", code: "runtime_transport", reply: "reject"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tt.expire {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
				defer cancel()
			}
			calls := 0
			client := &http.Client{Transport: humanTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if err := req.Context().Err(); err != nil {
					t.Errorf("API reply attempted with expired context: %v", err)
					return nil, err
				}
				var body map[string]string
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if req.Method != "POST" || req.URL.Path != "/permission/perm_test/reply" || body["reply"] != tt.reply {
					t.Fatalf("unexpected reply: %s %s %v", req.Method, req.URL.Path, body)
				}
				if tt.transport {
					return nil, errors.New("fixture network failure")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})}
			f := &fixture{ctx: ctx, interactive: true, runtime: &Runtime{client: client, origin: "http://fixture.invalid"}}
			r := Result{Scenario: "fresh-human", Status: "fail", Checks: []Check{}}
			decide := func(ctx context.Context) (bool, error) {
				var input io.Reader = strings.NewReader(tt.input)
				if tt.expire || tt.cancel {
					reader, writer := io.Pipe()
					defer reader.Close()
					defer writer.Close()
					input = reader
				}
				if tt.cancel {
					cancel()
				}
				allow, err := readHumanDecision(ctx, input)
				if (tt.expire || tt.cancel) && (allow || !errors.Is(err, ctx.Err())) {
					t.Fatalf("ended wait returned %v, %v; want false, %v", allow, err, ctx.Err())
				}
				if tt.late {
					cancel()
				}
				return allow, err
			}
			err := f.humanReply(permissionRequest{ID: "perm_test"}, &r, decide)
			// Guest records the scenario error code before allowing any progression.
			if err != nil {
				r.Code = err.Error()
			}
			if r.Code != tt.code || r.Status != tt.status {
				t.Errorf("result = %s/%s; want %s/%s", r.Status, r.Code, tt.status, tt.code)
			}
			wantCalls := 0
			if tt.reply != "" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Errorf("reply attempts = %d; want %d", calls, wantCalls)
			}
			report := sampleReport()
			report.Results = []Result{r}
			b, _ := json.Marshal(report)
			decoded, decodeErr := DecodeReport(strings.NewReader(string(b)), report.Run, report.SourceHash)
			if decodeErr != nil || decoded.ExitCode() == 0 {
				t.Fatalf("decision alone must never pass coverage: %v", decodeErr)
			}
		})
	}
}

func TestHumanReplyRequiresInteractiveConsent(t *testing.T) {
	f := &fixture{ctx: context.Background()}
	r := Result{Status: "fail"}
	err := f.humanReply(permissionRequest{ID: "perm_test"}, &r, func(context.Context) (bool, error) {
		t.Fatal("noninteractive fixture requested a decision")
		return true, nil
	})
	if err == nil || err.Error() != "human_decision_required" || r.Status != "blocked" {
		t.Fatalf("noninteractive result = %s, %v", r.Status, err)
	}
}

func TestHumanDecisionNeverGrantsAfterContextEnds(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		for _, input := range []string{"allow\n", "deny\n", "", "unexpected\n"} {
			t.Run(input+map[bool]string{false: "cancelled", true: "deadline"}[deadline], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				if deadline {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				}
				cancel()
				allow, err := readHumanDecision(ctx, strings.NewReader(input))
				if allow || !errors.Is(err, ctx.Err()) {
					t.Fatalf("ended context returned %v, %v; want false, %v", allow, err, ctx.Err())
				}
			})
		}
	}
}
