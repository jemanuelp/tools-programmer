package github

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"tools-programmer/internal/core"
)

type runnerFunc func(context.Context, []string, []byte) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, args []string, b []byte) ([]byte, error) {
	return f(ctx, args, b)
}
func argument(args []string, key string) string {
	for i, s := range args {
		if s == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
func endpoint(args []string) string { return args[9] }
func TestApproveRequestExactSHAAndSafeArguments(t *testing.T) {
	p := identityProject("enterprise.example/acme/demo")
	o := core.Operation{ID: "stable-operation", PR: 8, SHA: strings.Repeat("a", 40), Actor: "reviewer"}
	c := Client{Runner: runnerFunc(func(_ context.Context, args []string, b []byte) ([]byte, error) {
		if endpoint(args) == "user" {
			return []byte(`{"login":"reviewer"}`), nil
		}
		if argument(args, "--hostname") != "enterprise.example" || argument(args, "--method") != "POST" || argument(args, "--input") != "-" {
			t.Fatal(args)
		}
		if endpoint(args) != "repos/acme/demo/pulls/8/reviews" {
			t.Fatal(args)
		}
		var body map[string]string
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatal(err)
		}
		if body["event"] != "APPROVE" || body["commit_id"] != o.SHA || body["body"] != o.Body() || len(body) != 3 {
			t.Fatal(body)
		}
		return []byte(`{"id":77,"user":{"login":"reviewer"},"state":"APPROVED","commit_id":"` + o.SHA + `","submitted_at":"2026-09-09T00:00:00Z"}`), nil
	})}
	r, err := c.Approve(context.Background(), p, o)
	if err != nil || r.ID != 77 || r.SHA != o.SHA {
		t.Fatal(r, err)
	}
}
func TestPaginationAndOutputBounds(t *testing.T) {
	t.Run("reviews beyond first page", func(t *testing.T) {
		calls := 0
		c := Client{Runner: runnerFunc(func(_ context.Context, args []string, _ []byte) ([]byte, error) {
			if endpoint(args) == "user" {
				return []byte(`{"login":"reviewer"}`), nil
			}
			calls++
			if argument(args, "--method") != "GET" {
				t.Fatal("write in pagination")
			}
			if calls == 1 {
				items := make([]map[string]any, 100)
				for i := range items {
					items[i] = map[string]any{"id": i + 1, "state": "COMMENTED", "user": map[string]string{"login": "reviewer"}}
				}
				return json.Marshal(items)
			}
			if !strings.Contains(endpoint(args), "page=2") {
				t.Fatal(args)
			}
			return []byte(`[{"id":101,"state":"CHANGES_REQUESTED","user":{"login":"other"}}]`), nil
		})}
		r, err := c.Reviews(context.Background(), identityProject("github.com/acme/demo"), 1)
		if err != nil || len(r) != 101 || calls != 2 || r[100].State != "CHANGES_REQUESTED" {
			t.Fatal(len(r), calls, err)
		}
	})
	t.Run("output cap", func(t *testing.T) {
		c := Client{Runner: runnerFunc(func(context.Context, []string, []byte) ([]byte, error) { return make([]byte, outputLimit+1), nil })}
		if _, err := c.Reviews(context.Background(), identityProject("github.com/acme/demo"), 1); err == nil {
			t.Fatal("oversized output accepted")
		}
	})
	t.Run("malformed response", func(t *testing.T) {
		c := Client{Runner: runnerFunc(func(_ context.Context, args []string, _ []byte) ([]byte, error) {
			if endpoint(args) == "user" {
				return []byte(`{"login":"reviewer"}`), nil
			}
			return []byte(`null`), nil
		})}
		if _, err := c.Reviews(context.Background(), identityProject("github.com/acme/demo"), 1); err == nil {
			t.Fatal("null page accepted")
		}
	})
}
func TestInspectReadOnlyIncludesAllCheckAndStatusPages(t *testing.T) {
	sha := strings.Repeat("a", 40)
	base := strings.Repeat("b", 40)
	checks, statuses := 0, 0
	c := Client{Runner: runnerFunc(func(_ context.Context, args []string, _ []byte) ([]byte, error) {
		if argument(args, "--method") != "GET" {
			t.Fatal("inspection wrote")
		}
		path := endpoint(args)
		switch {
		case path == "user":
			return []byte(`{"login":"reviewer"}`), nil
		case path == "repos/acme/demo":
			return []byte(`{"full_name":"acme/demo","permissions":{"push":true}}`), nil
		case path == "repos/acme/demo/pulls/1":
			return []byte(`{"number":1,"state":"open","draft":false,"user":{"login":"author"},"head":{"sha":"` + sha + `"},"base":{"sha":"` + base + `","ref":"main","repo":{"full_name":"acme/demo"}}}`), nil
		case strings.Contains(path, "/reviews?"):
			return []byte(`[]`), nil
		case strings.Contains(path, "/check-runs?"):
			checks++
			if !strings.Contains(path, "filter=all") {
				t.Fatal("historical check filter missing")
			}
			if checks == 1 {
				items := make([]map[string]any, 100)
				for i := range items {
					items[i] = map[string]any{"id": i + 1, "name": "build", "status": "completed", "conclusion": "success", "app": map[string]int{"id": 1}}
				}
				return json.Marshal(map[string]any{"check_runs": items})
			}
			return []byte(`{"check_runs":[{"id":101,"name":"build","status":"queued","app":{"id":1}}]}`), nil
		case strings.Contains(path, "/statuses?"):
			statuses++
			if statuses == 1 {
				items := make([]map[string]any, 100)
				for i := range items {
					items[i] = map[string]any{"id": i + 1, "context": "external", "state": "success"}
				}
				return json.Marshal(items)
			}
			return []byte(`[{"id":101,"context":"external","state":"failure"}]`), nil
		}
		return nil, errors.New("unexpected route")
	})}
	s, err := c.Inspect(context.Background(), identityProject("github.com/acme/demo"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if checks != 2 || statuses != 2 || len(s.Checks) != 202 || s.ObservedAt == "" {
		t.Fatal(checks, statuses, s)
	}
	if core.Evaluate(s, true, false).Eligible {
		t.Fatal("new pending/failure accepted")
	}
}
func TestLimitedBufferDoesNotGrowBeyondCap(t *testing.T) {
	var b limitedBuffer
	if _, err := b.Write(make([]byte, outputLimit)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("x")); err == nil || !b.exceeded || b.Len() != outputLimit {
		t.Fatal("buffer exceeded bound")
	}
}
