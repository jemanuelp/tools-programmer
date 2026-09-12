package lab

import (
	"context"
	"reflect"
	"testing"
)

func TestSelectedCasesRespectGlobalCancellation(t *testing.T) {
	for _, mode := range []string{"before", "between", "during", "local_timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := Report{Startup: &Startup{Stage: "complete", Status: "complete"}}
			selected := []string{"contracts", "silent", "retry"}
			var dispatched []string
			if mode == "before" {
				cancel()
			}
			runSelected(ctx, &r, selected, func() {
				if mode == "between" {
					cancel()
				}
			}, func(name string) Result {
				// This callback includes fixture creation, setup and scenario dispatch.
				dispatched = append(dispatched, name)
				result := Result{Scenario: name, Status: "pass", Checks: []Check{{Label: "retained", OK: true}}}
				if mode == "during" && name == "silent" {
					cancel()
					result.Status = "blocked"
					result.Code = "human_decision_cancelled"
				}
				if mode == "local_timeout" {
					local, stop := context.WithTimeout(ctx, 0)
					defer stop()
					if local.Err() != context.DeadlineExceeded || ctx.Err() != nil {
						t.Fatal("invalid local timeout fixture")
					}
					result.Status = "fail"
					result.Code = "case_timeout"
				}
				return result
			})
			want := 1
			if mode == "before" {
				want = 0
			}
			if mode == "during" {
				want = 2
			}
			if mode == "local_timeout" {
				want = 3
			}
			if len(dispatched) != want || len(r.Results) != 3 {
				t.Fatalf("dispatch=%v results=%+v", dispatched, r.Results)
			}
			for i, result := range r.Results {
				if result.Scenario != selected[i] {
					t.Fatal(r.Results)
				}
				if i >= want {
					if result.Status != "not_run" || result.Code != "run_interrupted" || result.ElapsedMS != 0 || len(result.Checks) != 0 {
						t.Fatal(result)
					}
				} else if !reflect.DeepEqual(result.Checks, []Check{{Label: "retained", OK: true}}) {
					t.Fatal("lost current evidence", result)
				}
			}
			if mode == "during" && (r.Results[0].Status != "pass" || r.Results[1].Status != "blocked" || r.Results[1].Code != "human_decision_cancelled") {
				t.Fatal(r.Results)
			}
			if mode == "between" && r.Results[0].Status != "pass" {
				t.Fatal(r.Results)
			}
		})
	}
}
