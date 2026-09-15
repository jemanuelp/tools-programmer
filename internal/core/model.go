package core

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string        { return e.Message }
func Fail(code, message string) error { return &Error{code, message} }
func Now() string                     { return time.Now().UTC().Format(time.RFC3339Nano) }
func NewID() string                   { return rand.Text() }

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var hostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

func ValidName(s string) bool { return namePattern.MatchString(s) && s != "." && s != ".." }
func ValidSHA(s string) bool  { return shaPattern.MatchString(s) }
func ValidPR(n int) bool      { return n > 0 && n <= 2147483647 }
func CanonicalRepo(input string) (string, error) {
	p := strings.Split(strings.ToLower(input), "/")
	if len(p) == 2 {
		p = append([]string{"github.com"}, p...)
	}
	if len(p) != 3 || !hostPattern.MatchString(p[0]) || strings.Contains(p[0], "..") || !ValidName(p[1]) || !ValidName(p[2]) || strings.HasSuffix(p[2], ".git") {
		return "", Fail("invalid_input", "repository must be [host/]owner/repo (no URL, port, or .git suffix)")
	}
	for _, label := range strings.Split(p[0], ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", Fail("invalid_input", "invalid repository host")
		}
	}
	return strings.Join(p, "/"), nil
}

type Project struct {
	Name           string `json:"name"`
	Repo           string `json:"repository"`
	OriginURL      string `json:"origin_url"`
	Path           string `json:"local_path,omitempty"`
	AllowNoChecks  bool   `json:"allow_no_checks"`
	CreatedAt      string `json:"created_at"`
	ExpectedGitHub string `json:"expected_github_login"`
	GitName        string `json:"expected_git_name"`
	GitEmail       string `json:"expected_git_email"`
}

func (p Project) ValidateIdentity() error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,99}$`).MatchString(p.ExpectedGitHub) {
		return Fail("identity_unbound", "explicit expected GitHub login, Git name and email are required; use project bind for legacy projects")
	}
	for _, value := range []string{p.GitName, p.GitEmail} {
		if value == "" || len(value) > 200 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "<>\r\n\x00") {
			return Fail("identity_unbound", "expected Git identity must be nonempty, bounded and contain no identity delimiters or control characters")
		}
		for _, r := range value {
			if r < 32 || r == 127 {
				return Fail("identity_unbound", "invalid Git identity control character")
			}
		}
	}
	if strings.ContainsAny(p.GitEmail, " \t") || !strings.Contains(p.GitEmail, "@") {
		return Fail("identity_unbound", "expected Git email must be an email address")
	}
	return nil
}

func (p Project) CheckActor(actor string) error {
	if err := p.ValidateIdentity(); err != nil {
		return err
	}
	if !strings.EqualFold(actor, p.ExpectedGitHub) {
		return Fail("identity_mismatch", "authenticated GitHub actor does not match the registered expected login; no account switching is performed")
	}
	return nil
}

func (p Project) Host() string { return strings.Split(p.Repo, "/")[0] }
func (p Project) Slug() string { return strings.Join(strings.Split(p.Repo, "/")[1:], "/") }

type RemoteReview struct {
	ID          int64  `json:"id"`
	Actor       string `json:"actor"`
	State       string `json:"state"`
	SHA         string `json:"commit_id"`
	SubmittedAt string `json:"submitted_at"`
	Body        string `json:"-"`
}
type Check struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	AppID      int64  `json:"app_id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}
type Snapshot struct {
	PR         int            `json:"pr"`
	Head       string         `json:"head_sha"`
	BaseRef    string         `json:"base_ref"`
	BaseSHA    string         `json:"base_sha"`
	Author     string         `json:"author"`
	State      string         `json:"state"`
	Draft      bool           `json:"draft"`
	Actor      string         `json:"actor"`
	CanApprove bool           `json:"can_approve"`
	ObservedAt string         `json:"observed_at"`
	Reviews    []RemoteReview `json:"reviews"`
	Checks     []Check        `json:"checks"`
}
type LocalReview struct {
	ID             int64  `json:"id"`
	Project        string `json:"project"`
	PR             int    `json:"pr"`
	SHA            string `json:"head_sha"`
	Reviewer       string `json:"reviewer"`
	Method         string `json:"method"`
	Outcome        string `json:"outcome"`
	Summary        string `json:"summary,omitempty"`
	Evidence       string `json:"evidence,omitempty"`
	RecordedAt     string `json:"recorded_at"`
	Origin         string `json:"origin"`
	Freshness      string `json:"freshness"`
	LastObservedAt string `json:"last_observed_at,omitempty"`
}
type PlanItem struct {
	Snapshot        Snapshot `json:"snapshot"`
	Eligible        bool     `json:"eligible"`
	AlreadyApproved bool     `json:"already_approved"`
	Reasons         []string `json:"reasons"`
}
type Plan struct {
	ID        string     `json:"id"`
	Project   Project    `json:"project"`
	CreatedAt string     `json:"created_at"`
	Items     []PlanItem `json:"items"`
}
type Operation struct {
	ID        string `json:"id"`
	PlanID    string `json:"plan_id"`
	Project   string `json:"project"`
	PR        int    `json:"pr"`
	SHA       string `json:"head_sha"`
	Actor     string `json:"actor"`
	State     string `json:"attempt_state"`
	ReviewID  int64  `json:"review_id,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (o Operation) Body() string {
	return "Technical review recorded locally for " + o.SHA + ".\n\n<!-- tools-programmer-operation:" + o.ID + " -->"
}

type Confirmation struct {
	OperationID string       `json:"operation_id"`
	Review      RemoteReview `json:"review"`
	ConfirmedAt string       `json:"confirmed_at"`
	Origin      string       `json:"origin"`
}

func Evaluate(s Snapshot, localApproved, allowNoChecks bool) PlanItem {
	i := PlanItem{Snapshot: s, Reasons: []string{}}
	block := func(reason string) { i.Reasons = append(i.Reasons, reason) }
	if !ValidPR(s.PR) || !ValidSHA(s.Head) || !ValidSHA(s.BaseSHA) || s.BaseRef == "" || s.Actor == "" || s.Author == "" {
		block("incomplete remote metadata")
	}
	if s.State != "open" || s.Draft {
		block("PR must be open and non-draft")
	}
	if strings.EqualFold(s.Actor, s.Author) {
		block("self-approval is forbidden")
	}
	if !s.CanApprove {
		block("repository write/maintain/admin permission required")
	}
	if !localApproved {
		block("latest local technical verdict for this head must be approved")
	}
	latest := map[string]RemoteReview{}
	for _, r := range s.Reviews {
		if r.Actor == "" || r.ID <= 0 {
			block("incomplete remote review metadata")
			continue
		}
		if r.State == "COMMENTED" {
			continue
		}
		if r.State == "PENDING" {
			block("pending remote review")
			continue
		}
		if r.State != "APPROVED" && r.State != "CHANGES_REQUESTED" && r.State != "DISMISSED" {
			block("unknown remote review state")
			continue
		}
		key := strings.ToLower(r.Actor)
		if r.ID > latest[key].ID {
			latest[key] = r
		}
	}
	for actor, r := range latest {
		if r.State == "CHANGES_REQUESTED" {
			block("unresolved changes requested by " + actor)
		}
		if actor == strings.ToLower(s.Actor) && r.State == "APPROVED" && r.SHA == s.Head {
			i.AlreadyApproved = true
		}
	}
	checks := map[string]Check{}
	for _, c := range s.Checks {
		if c.ID <= 0 || c.Name == "" || (c.Kind != "check" && c.Kind != "status") || (c.Kind == "check" && c.AppID <= 0) {
			block("incomplete check metadata")
			continue
		}
		key := fmt.Sprintf("%s/%d/%s", c.Kind, c.AppID, c.Name)
		if c.ID > checks[key].ID {
			checks[key] = c
		}
	}
	if len(checks) == 0 && !allowNoChecks {
		block("no observed checks; project does not allow no checks")
	}
	for _, c := range checks {
		if (c.Kind == "check" && (c.Status != "completed" || c.Conclusion != "success")) || (c.Kind == "status" && c.Status != "success") {
			block("check not successful: " + c.Name)
		}
	}
	sort.Strings(i.Reasons)
	i.Eligible = len(i.Reasons) == 0
	return i
}
