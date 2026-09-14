package core

import (
	"strconv"
	"strings"
)

type Issue struct {
	ID               int64  `json:"id"`
	Project          string `json:"project"`
	Repository       string `json:"repository"`
	Number           int    `json:"number"`
	URL              string `json:"url"`
	RemoteID         string `json:"remote_id,omitempty"`
	RemoteObservedAt string `json:"remote_observed_at,omitempty"`
	CreatedAt        string `json:"created_at"`
}

// ValidateIssueURL accepts only canonical HTTPS identities, including enterprise hosts.
// Empty URLs represent legacy registrations; callers may require a URL at their boundary.
func ValidateIssueURL(repository string, number int, value string) error {
	if value != "" && value != "https://"+repository+"/issues/"+strconv.Itoa(number) {
		return Fail("invalid_input", "url must be the canonical HTTPS issue URL matching the registered repository and number (no credentials, port, query, fragment or trailing slash)")
	}
	return nil
}

type Estimate struct {
	ID         int64  `json:"id"`
	IssueID    int64  `json:"issue_id"`
	Kind       string `json:"kind"`
	Score      int    `json:"score"`
	Proposer   string `json:"proposer"`
	Rationale  string `json:"rationale"`
	ProposedAt string `json:"proposed_at"`
}

type EstimateConfirmation struct {
	ID          int64  `json:"id"`
	ProposalID  int64  `json:"proposal_id"`
	Human       string `json:"human"`
	Rationale   string `json:"rationale"`
	ConfirmedAt string `json:"confirmed_at"`
}

type IssueOutcome struct {
	ID         int64  `json:"id"`
	Outcome    string `json:"outcome"`
	Actor      string `json:"actor"`
	Rationale  string `json:"rationale"`
	RecordedAt string `json:"recorded_at"`
}

type IssueHistory struct {
	Issue         Issue                  `json:"issue"`
	PRs           []int                  `json:"prs"`
	Estimates     []Estimate             `json:"proposals"`
	Confirmations []EstimateConfirmation `json:"confirmations"`
	Outcomes      []IssueOutcome         `json:"outcomes"`
	Initial       *Estimate              `json:"initial"`
	Final         *Estimate              `json:"final"`
	State         string                 `json:"state"`
}

func ValidScore(score int) bool {
	switch score {
	case 1, 2, 3, 5, 8, 13, 21:
		return true
	}
	return false
}

// Domain rationale is intentional text, unlike the payload-free telemetry boundary.
func ValidText(s string, limit int) bool {
	return strings.TrimSpace(s) != "" && len(s) <= limit && !strings.ContainsRune(s, '\x00')
}
