package core

import (
	"strings"
	"testing"
)

func healthy() Snapshot {
	return Snapshot{PR: 7, Head: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40), BaseRef: "main", Actor: "reviewer", Author: "author", State: "open", CanApprove: true, Checks: []Check{{ID: 1, Name: "build", AppID: 5, Kind: "check", Status: "completed", Conclusion: "success"}}}
}
func TestEvaluationRequiresTechnicalReviewAndSafeRemoteState(t *testing.T) {
	cases := []struct {
		name                               string
		change                             func(*Snapshot)
		approved, allow, eligible, already bool
	}{
		{"healthy", func(*Snapshot) {}, true, false, true, false},
		{"green CI is not review", func(*Snapshot) {}, false, false, false, false},
		{"self authored case insensitive", func(s *Snapshot) { s.Author = "REVIEWER" }, true, false, false, false},
		{"no permission", func(s *Snapshot) { s.CanApprove = false }, true, false, false, false},
		{"draft", func(s *Snapshot) { s.Draft = true }, true, false, false, false},
		{"closed", func(s *Snapshot) { s.State = "closed" }, true, false, false, false},
		{"missing actor", func(s *Snapshot) { s.Actor = "" }, true, false, false, false},
		{"no CI default blocked", func(s *Snapshot) { s.Checks = nil }, true, false, false, false},
		{"no CI explicit policy", func(s *Snapshot) { s.Checks = nil }, true, true, true, false},
		{"allow no checks cannot ignore failure", func(s *Snapshot) { s.Checks[0].Conclusion = "failure" }, true, true, false, false},
		{"pending", func(s *Snapshot) { s.Checks[0].Status = "queued" }, true, false, false, false},
		{"unknown", func(s *Snapshot) { s.Checks[0].Conclusion = "unknown" }, true, false, false, false},
		{"skipped conservative", func(s *Snapshot) { s.Checks[0].Conclusion = "skipped" }, true, false, false, false},
		{"newer failure supersedes success", func(s *Snapshot) {
			s.Checks = append(s.Checks, Check{ID: 2, Name: "build", AppID: 5, Kind: "check", Status: "completed", Conclusion: "failure"})
		}, true, false, false, false},
		{"newer success supersedes failure", func(s *Snapshot) {
			s.Checks[0].ID = 2
			s.Checks = append(s.Checks, Check{ID: 1, Name: "build", AppID: 5, Kind: "check", Status: "completed", Conclusion: "failure"})
		}, true, false, true, false},
		{"different app failure retained", func(s *Snapshot) {
			s.Checks = append(s.Checks, Check{ID: 2, Name: "build", AppID: 6, Kind: "check", Status: "completed", Conclusion: "failure"})
		}, true, false, false, false},
		{"status newest pending", func(s *Snapshot) {
			s.Checks = append(s.Checks, Check{ID: 1, Name: "external", Kind: "status", Status: "success"}, Check{ID: 2, Name: "external", Kind: "status", Status: "pending"})
		}, true, false, false, false},
		{"old changes request blocks", func(s *Snapshot) {
			s.Reviews = []RemoteReview{{ID: 1, Actor: "other", State: "CHANGES_REQUESTED", SHA: strings.Repeat("c", 40)}}
		}, true, false, false, false},
		{"comment does not erase changes request", func(s *Snapshot) {
			s.Reviews = []RemoteReview{{ID: 1, Actor: "other", State: "CHANGES_REQUESTED"}, {ID: 2, Actor: "other", State: "COMMENTED"}}
		}, true, false, false, false},
		{"dismissed changes request", func(s *Snapshot) { s.Reviews = []RemoteReview{{ID: 1, Actor: "other", State: "DISMISSED"}} }, true, false, true, false},
		{"already approved", func(s *Snapshot) {
			s.Reviews = []RemoteReview{{ID: 1, Actor: "REVIEWER", State: "APPROVED", SHA: s.Head}}
		}, true, false, true, true},
		{"old approval not current", func(s *Snapshot) {
			s.Reviews = []RemoteReview{{ID: 1, Actor: "reviewer", State: "APPROVED", SHA: strings.Repeat("c", 40)}}
		}, true, false, true, false},
		{"dismissal supersedes approval", func(s *Snapshot) {
			s.Reviews = []RemoteReview{{ID: 1, Actor: "reviewer", State: "APPROVED", SHA: s.Head}, {ID: 2, Actor: "reviewer", State: "DISMISSED", SHA: s.Head}}
		}, true, false, true, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := healthy()
			tt.change(&s)
			got := Evaluate(s, tt.approved, tt.allow)
			if got.Eligible != tt.eligible || got.AlreadyApproved != tt.already {
				t.Fatalf("unexpected evaluation: %+v", got)
			}
		})
	}
}
func TestRepositoryValidation(t *testing.T) {
	for _, input := range []string{"https://github.com/acme/repo", "-bad/repo", "acme/../repo", "acme/repo.git", "host:443/acme/repo", "host..com/acme/repo", "host/-/repo", "acme/repo;touch", "acme/repo\n", "user@host/acme/repo", "acme/repo?x=1"} {
		t.Run(input, func(t *testing.T) {
			if _, err := CanonicalRepo(input); err == nil {
				t.Fatal("invalid repository accepted")
			}
		})
	}
	for _, input := range []string{"Acme/Repo", "GitHub.Com/Acme/Repo"} {
		got, err := CanonicalRepo(input)
		if err != nil || got != "github.com/acme/repo" {
			t.Fatalf("canonicalization: %s %v", got, err)
		}
	}
	if ValidSHA("abc") || ValidSHA(strings.Repeat("A", 40)) || ValidPR(0) || ValidPR(-1) {
		t.Fatal("invalid identifiers accepted")
	}
}
