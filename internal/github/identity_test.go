package github

import (
	"context"
	"strings"
	"testing"
	"tools-programmer/internal/core"
)

func identityProject(repo string) core.Project {
	return core.Project{Repo: repo, ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"}
}

func TestAllRemoteBoundariesRejectWrongOrUnboundActor(t *testing.T) {
	for _, unbound := range []bool{false, true} {
		for _, action := range []string{"inspect", "approve", "reviews", "readback"} {
			t.Run(action+map[bool]string{true: " unbound", false: " mismatch"}[unbound], func(t *testing.T) {
				calls := 0
				c := Client{Runner: runnerFunc(func(_ context.Context, args []string, _ []byte) ([]byte, error) {
					calls++
					if endpoint(args) != "user" || argument(args, "--method") != "GET" {
						t.Fatal("repository operation before identity match", args)
					}
					return []byte(`{"login":"other"}`), nil
				})}
				p := identityProject("github.com/acme/demo")
				if unbound {
					p.ExpectedGitHub = ""
				}
				var err error
				switch action {
				case "inspect":
					_, err = c.Inspect(context.Background(), p, 1)
				case "approve":
					_, err = c.Approve(context.Background(), p, core.Operation{PR: 1, SHA: strings.Repeat("a", 40), Actor: "reviewer"})
				case "reviews":
					_, err = c.Reviews(context.Background(), p, 1)
				case "readback":
					_, err = c.GetReview(context.Background(), p, 1, 1)
				}
				if err == nil {
					t.Fatal("identity mismatch accepted")
				}
				if unbound && calls != 0 {
					t.Fatal("unbound project contacted gh")
				}
			})
		}
	}
}
