package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"tools-programmer/internal/core"
)

const outputLimit = 2 << 20
const maxPages = 100

type Runner interface {
	Run(context.Context, []string, []byte) ([]byte, error)
}
type ExecRunner struct{}
type limitedBuffer struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *limitedBuffer) Len() int      { return b.buffer.Len() }
func (b *limitedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > outputLimit-b.Len() {
		b.exceeded = true
		return 0, fmt.Errorf("output limit exceeded")
	}
	return b.buffer.Write(p)
}

func (ExecRunner) Run(ctx context.Context, args []string, input []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Use gh's configured identity, never tokens injected into this process.
	for _, env := range os.Environ() {
		key := strings.SplitN(env, "=", 2)[0]
		switch key {
		case "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_DEBUG", "GH_HOST", "GH_PROMPT_DISABLED", "GH_PAGER", "PAGER":
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	cmd.Env = append(cmd.Env, "GH_PROMPT_DISABLED=1", "GH_PAGER=cat", "PAGER=cat")
	err := cmd.Run()
	if err != nil || stdout.exceeded || stderr.exceeded {
		return nil, core.Fail("github_io", "gh request failed, timed out, or exceeded output bound; check gh authentication/access separately (stderr suppressed)")
	}
	return stdout.Bytes(), nil
}

type Client struct{ Runner Runner }

func (c *Client) api(ctx context.Context, p core.Project, method, path string, body any, out any) error {
	if err := p.ValidateIdentity(); err != nil {
		return err
	}
	var actor user
	if err := c.request(ctx, p, "GET", "user", nil, &actor); err != nil {
		return err
	}
	if err := p.CheckActor(actor.Login); err != nil {
		return err
	}
	if method == "GET" && path == "user" {
		if target, ok := out.(*user); ok {
			*target = actor
			return nil
		}
	}
	return c.request(ctx, p, method, path, body, out)
}

func (c *Client) request(ctx context.Context, p core.Project, method, path string, body any, out any) error {
	canonical, err := core.CanonicalRepo(p.Repo)
	if err != nil || canonical != p.Repo {
		return core.Fail("invalid_input", "invalid stored repository")
	}
	args := []string{"api", "--hostname", p.Host(), "--method", method, "-H", "Accept: application/vnd.github+json", "-H", "X-GitHub-Api-Version: 2022-11-28", path}
	var input []byte
	if body != nil {
		input, err = json.Marshal(body)
		if err != nil {
			return err
		}
		args = append(args, "--input", "-")
	}
	b, err := c.Runner.Run(ctx, args, input)
	if err != nil {
		return err
	}
	if len(b) > outputLimit {
		return core.Fail("github_io", "GitHub response exceeded output bound")
	}
	if err = json.Unmarshal(b, out); err != nil {
		return core.Fail("github_response", "GitHub returned invalid JSON")
	}
	return nil
}
func route(p core.Project, pr int) string { return fmt.Sprintf("repos/%s/pulls/%d", p.Slug(), pr) }

type user struct {
	Login string `json:"login"`
}
type review struct {
	ID        int64  `json:"id"`
	User      user   `json:"user"`
	State     string `json:"state"`
	Commit    string `json:"commit_id"`
	Submitted string `json:"submitted_at"`
	Body      string `json:"body"`
}

func (r review) model() core.RemoteReview {
	if submitted, err := time.Parse(time.RFC3339Nano, r.Submitted); err == nil {
		r.Submitted = submitted.UTC().Format(time.RFC3339Nano)
	}
	return core.RemoteReview{ID: r.ID, Actor: r.User.Login, State: r.State, SHA: r.Commit, SubmittedAt: r.Submitted, Body: r.Body}
}

func (c *Client) pages(ctx context.Context, p core.Project, path, key string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	for page := 1; page <= maxPages; page++ {
		var raw json.RawMessage
		if err := c.api(ctx, p, "GET", fmt.Sprintf("%s%sper_page=100&page=%d", path, separator, page), nil, &raw); err != nil {
			return nil, err
		}
		if key != "" {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				return nil, core.Fail("github_response", "invalid paginated object")
			}
			var exists bool
			raw, exists = object[key]
			if !exists {
				return nil, core.Fail("github_response", "missing paginated result field")
			}
		}
		var items []json.RawMessage
		if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &items) != nil {
			return nil, core.Fail("github_response", "invalid paginated list")
		}
		out = append(out, items...)
		if len(items) < 100 {
			return out, nil
		}
	}
	return nil, core.Fail("pagination_limit", "GitHub pagination limit reached; inspection is incomplete")
}
func (c *Client) Reviews(ctx context.Context, p core.Project, pr int) ([]core.RemoteReview, error) {
	if !core.ValidPR(pr) {
		return nil, core.Fail("invalid_input", "invalid PR number")
	}
	items, err := c.pages(ctx, p, route(p, pr)+"/reviews", "")
	if err != nil {
		return nil, err
	}
	out := []core.RemoteReview{}
	for _, raw := range items {
		var r review
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, core.Fail("github_response", "invalid review")
		}
		out = append(out, r.model())
	}
	return out, nil
}
func (c *Client) GetReview(ctx context.Context, p core.Project, pr int, id int64) (core.RemoteReview, error) {
	var r review
	if !core.ValidPR(pr) || id <= 0 {
		return r.model(), core.Fail("invalid_input", "invalid PR or review ID")
	}
	err := c.api(ctx, p, "GET", fmt.Sprintf("%s/reviews/%d", route(p, pr), id), nil, &r)
	return r.model(), err
}
func (c *Client) Approve(ctx context.Context, p core.Project, o core.Operation) (core.RemoteReview, error) {
	var r review
	if err := p.CheckActor(o.Actor); err != nil {
		return r.model(), err
	}
	if !core.ValidPR(o.PR) || !core.ValidSHA(o.SHA) {
		return r.model(), core.Fail("invalid_input", "approval requires exact full SHA and PR")
	}
	body := map[string]string{"commit_id": o.SHA, "event": "APPROVE", "body": o.Body()}
	err := c.api(ctx, p, "POST", route(p, o.PR)+"/reviews", body, &r)
	return r.model(), err
}
func (c *Client) Inspect(ctx context.Context, p core.Project, pr int) (core.Snapshot, error) {
	s := core.Snapshot{PR: pr}
	if !core.ValidPR(pr) {
		return s, core.Fail("invalid_input", "invalid PR number")
	}
	var actor user
	if err := c.api(ctx, p, "GET", "user", nil, &actor); err != nil {
		return s, err
	}
	s.Actor = actor.Login
	var repo struct {
		FullName    string          `json:"full_name"`
		Permissions map[string]bool `json:"permissions"`
	}
	if err := c.api(ctx, p, "GET", "repos/"+p.Slug(), nil, &repo); err != nil {
		return s, err
	}
	if !strings.EqualFold(repo.FullName, p.Slug()) {
		return s, core.Fail("repository_changed", "repository redirect/rename detected; register its canonical identity explicitly")
	}
	s.CanApprove = repo.Permissions["push"] || repo.Permissions["maintain"] || repo.Permissions["admin"]
	var pull struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Draft  *bool  `json:"draft"`
		User   user   `json:"user"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			SHA  string `json:"sha"`
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"base"`
	}
	if err := c.api(ctx, p, "GET", route(p, pr), nil, &pull); err != nil {
		return s, err
	}
	if pull.Number != pr || pull.Draft == nil || !strings.EqualFold(pull.Base.Repo.FullName, p.Slug()) || !core.ValidSHA(pull.Head.SHA) || !core.ValidSHA(pull.Base.SHA) || pull.Base.Ref == "" || actor.Login == "" || pull.User.Login == "" {
		return s, core.Fail("github_response", "incomplete or mismatched PR metadata")
	}
	s.Head = pull.Head.SHA
	s.BaseSHA = pull.Base.SHA
	s.BaseRef = pull.Base.Ref
	s.State = pull.State
	s.Draft = *pull.Draft
	s.Author = pull.User.Login
	var err error
	s.Reviews, err = c.Reviews(ctx, p, pr)
	if err != nil {
		return s, err
	}
	checks, err := c.pages(ctx, p, "repos/"+p.Slug()+"/commits/"+s.Head+"/check-runs?filter=all", "check_runs")
	if err != nil {
		return s, err
	}
	s.Checks = []core.Check{}
	for _, raw := range checks {
		var r struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			App        struct {
				ID int64 `json:"id"`
			} `json:"app"`
		}
		if json.Unmarshal(raw, &r) != nil {
			return s, core.Fail("github_response", "invalid check run")
		}
		s.Checks = append(s.Checks, core.Check{ID: r.ID, Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, AppID: r.App.ID, Kind: "check"})
	}
	statuses, err := c.pages(ctx, p, "repos/"+p.Slug()+"/commits/"+s.Head+"/statuses", "")
	if err != nil {
		return s, err
	}
	for _, raw := range statuses {
		var r struct {
			ID      int64  `json:"id"`
			Context string `json:"context"`
			State   string `json:"state"`
		}
		if json.Unmarshal(raw, &r) != nil {
			return s, core.Fail("github_response", "invalid commit status")
		}
		s.Checks = append(s.Checks, core.Check{ID: r.ID, Name: r.Context, Status: r.State, Kind: "status"})
	}
	// Re-read identity and PR after pagination to reject a moving inspection.
	var endActor user
	if err = c.api(ctx, p, "GET", "user", nil, &endActor); err != nil {
		return s, err
	}
	var endPull struct {
		State string `json:"state"`
		Draft *bool  `json:"draft"`
		Head  struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err = c.api(ctx, p, "GET", route(p, pr), nil, &endPull); err != nil {
		return s, err
	}
	if !strings.EqualFold(endActor.Login, s.Actor) || endPull.Head.SHA != s.Head || endPull.Base.SHA != s.BaseSHA || endPull.Base.Ref != s.BaseRef || endPull.State != s.State || endPull.Draft == nil || *endPull.Draft != s.Draft {
		return s, core.Fail("inspection_changed", "actor or PR changed during inspection; plan again")
	}
	s.ObservedAt = core.Now()
	return s, nil
}

var _ io.Writer = (*limitedBuffer)(nil)
