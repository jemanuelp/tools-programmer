package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type fakeGitHub struct {
	mu          sync.Mutex
	snapshot    core.Snapshot
	posts       int
	unknown     bool
	unknownAt   int
	badReadback bool
	badResponse bool
	readError   bool
	reviews     []core.RemoteReview
	reviewPRs   map[int64]int
	last        core.Operation
}

func (f *fakeGitHub) Inspect(_ context.Context, _ core.Project, n int) (core.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.snapshot
	s.PR = n
	s.ObservedAt = core.Now()
	s.Reviews = f.forPR(n)
	return s, nil
}
func (f *fakeGitHub) Approve(_ context.Context, _ core.Project, o core.Operation) (core.RemoteReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts++
	f.last = o
	r := core.RemoteReview{ID: int64(f.posts), Actor: o.Actor, State: "APPROVED", SHA: o.SHA, SubmittedAt: core.Now(), Body: o.Body()}
	f.reviews = append(f.reviews, r)
	if f.reviewPRs == nil {
		f.reviewPRs = map[int64]int{}
	}
	f.reviewPRs[r.ID] = o.PR
	if f.unknown || f.posts == f.unknownAt {
		return core.RemoteReview{}, errors.New("connection lost after write")
	}
	if f.badResponse {
		r.SHA = strings.Repeat("f", 40)
	}
	return r, nil
}
func (f *fakeGitHub) GetReview(_ context.Context, _ core.Project, pr int, id int64) (core.RemoteReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readError {
		return core.RemoteReview{}, errors.New("read failed")
	}
	for _, r := range f.forPR(pr) {
		if r.ID == id {
			if f.badReadback {
				r.Actor = "wrong-actor"
			}
			return r, nil
		}
	}
	return core.RemoteReview{}, errors.New("not found")
}
func (f *fakeGitHub) Reviews(_ context.Context, _ core.Project, pr int) ([]core.RemoteReview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forPR(pr), nil
}

func (f *fakeGitHub) forPR(pr int) []core.RemoteReview {
	out := []core.RemoteReview{}
	for _, r := range f.reviews {
		if f.reviewPRs[r.ID] == 0 || f.reviewPRs[r.ID] == pr {
			out = append(out, r)
		}
	}
	return out
}
func setup(t *testing.T) (*Service, *fakeGitHub, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "history.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_, err = s.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHub{snapshot: core.Snapshot{Head: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40), BaseRef: "main", Actor: "reviewer", Author: "author", State: "open", CanApprove: true, Checks: []core.Check{{ID: 1, Kind: "status", Name: "ci", Status: "success"}}}}
	for _, n := range []int{1, 2} {
		if _, err = s.Record(core.LocalReview{Project: "demo", PR: n, SHA: f.snapshot.Head, Reviewer: "human", Method: "manual", Outcome: "approved"}); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Store: s, GitHub: f}, f, path
}
func makePlan(t *testing.T, s *Service, ns ...int) core.Plan {
	t.Helper()
	p, err := s.Plan(context.Background(), "demo", ns)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanReadOnlyAndConfirmedApprovalIdempotence(t *testing.T) {
	s, f, _ := setup(t)
	p := makePlan(t, s, 1)
	if f.posts != 0 || !p.Items[0].Eligible {
		t.Fatal("planning wrote or blocked", p)
	}
	before, err := s.Show(p.ID)
	if err != nil || len(before.Confirmations) != 0 {
		t.Fatal("local verdict became approval", err)
	}
	report, err := s.Apply(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || len(report.Confirmations) != 1 || report.Operations[0].State != "confirmed" || report.Confirmations[0].Review.SHA != f.snapshot.Head || report.Confirmations[0].Origin != "github-confirmed" {
		t.Fatalf("bad confirmation: %+v", report)
	}
	if f.last.SHA != p.Items[0].Snapshot.Head {
		t.Fatal("write not exact SHA bound")
	}
	if _, err = s.Apply(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	p2 := makePlan(t, s, 1)
	if _, err = s.Apply(context.Background(), p2.ID); err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 {
		t.Fatal("duplicate approval")
	}
}
func TestStaleAndUnsafePreflightNeverWrite(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*fakeGitHub)
	}{
		{"head", func(f *fakeGitHub) { f.snapshot.Head = strings.Repeat("c", 40) }},
		{"base ref", func(f *fakeGitHub) { f.snapshot.BaseRef = "release" }},
		{"base sha", func(f *fakeGitHub) { f.snapshot.BaseSHA = strings.Repeat("c", 40) }},
		{"actor", func(f *fakeGitHub) { f.snapshot.Actor = "other" }},
		{"self", func(f *fakeGitHub) { f.snapshot.Author = "reviewer" }},
		{"draft", func(f *fakeGitHub) { f.snapshot.Draft = true }},
		{"permission", func(f *fakeGitHub) { f.snapshot.CanApprove = false }},
		{"CI", func(f *fakeGitHub) { f.snapshot.Checks[0].Status = "pending" }},
		{"changes request", func(f *fakeGitHub) {
			f.reviews = []core.RemoteReview{{ID: 9, Actor: "other", State: "CHANGES_REQUESTED"}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, f, _ := setup(t)
			p := makePlan(t, s, 1)
			tt.change(f)
			if _, err := s.Apply(context.Background(), p.ID); err == nil {
				t.Fatal("unsafe plan applied")
			}
			if f.posts != 0 {
				t.Fatal("unsafe write")
			}
		})
	}
}
func TestLocalVerdictRevocationAndMissingReviewBlock(t *testing.T) {
	s, f, _ := setup(t)
	p := makePlan(t, s, 1)
	if _, err := s.Store.Record(core.LocalReview{Project: "demo", PR: 1, SHA: f.snapshot.Head, Reviewer: "human", Method: "manual", Outcome: "changes_requested"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(context.Background(), p.ID); err == nil {
		t.Fatal("revoked review applied")
	}
	missing := makePlan(t, s, 3)
	if missing.Items[0].Eligible || f.posts != 0 {
		t.Fatal("CI accepted as technical review")
	}
}
func TestConcurrentApplyAcrossIndependentConnections(t *testing.T) {
	s, f, path := setup(t)
	p := makePlan(t, s, 1)
	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	s2 := &Service{Store: second, GitHub: f}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, svc := range []*Service{s, s2} {
		wg.Add(1)
		go func(svc *Service) { defer wg.Done(); <-start; _, _ = svc.Apply(context.Background(), p.ID) }(svc)
	}
	close(start)
	wg.Wait()
	if f.posts != 1 {
		t.Fatalf("concurrent POST count = %d", f.posts)
	}
	report, err := s.Show(p.ID)
	if err != nil || len(report.Confirmations) != 1 {
		t.Fatal(report, err)
	}
}
func TestUnknownWriteStopsBatchAndReconcilesReadOnlyAfterRestart(t *testing.T) {
	s, f, path := setup(t)
	p := makePlan(t, s, 1, 2)
	f.unknown = true
	report, err := s.Apply(context.Background(), p.ID)
	if err == nil || f.posts != 1 || report.Operations[0].State != "unknown" || len(report.Confirmations) != 0 {
		t.Fatal("unknown not retained", report, err)
	}
	s.Store.Close()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s.Store = db
	if _, err = s.Apply(context.Background(), p.ID); err == nil || f.posts != 1 {
		t.Fatal("unknown write retried")
	}
	p2 := makePlan(t, s, 2)
	if _, err = s.Apply(context.Background(), p2.ID); err == nil || f.posts != 1 {
		t.Fatal("unknown allowed another batch")
	}
	report, err = s.Reconcile(context.Background(), p.ID)
	if err != nil || f.posts != 1 || len(report.Confirmations) != 1 {
		t.Fatal("read-only marker reconciliation failed", report, err)
	}
}
func TestUnmatchedUnknownRemainsBlocked(t *testing.T) {
	s, f, _ := setup(t)
	p := makePlan(t, s, 1)
	f.unknown = true
	_, _ = s.Apply(context.Background(), p.ID)
	f.reviews = nil
	if _, err := s.Reconcile(context.Background(), p.ID); err == nil {
		t.Fatal("missing marker silently reset")
	}
	if _, err := s.Apply(context.Background(), p.ID); err == nil || f.posts != 1 {
		t.Fatal("blind retry")
	}
}
func TestKnownIDReadbackMismatchAndReconciliation(t *testing.T) {
	for _, mode := range []string{"actor mismatch", "response mismatch", "read error"} {
		t.Run(mode, func(t *testing.T) {
			s, f, _ := setup(t)
			p := makePlan(t, s, 1)
			switch mode {
			case "actor mismatch":
				f.badReadback = true
			case "response mismatch":
				f.badResponse = true
			case "read error":
				f.readError = true
			}
			report, err := s.Apply(context.Background(), p.ID)
			if err == nil || report.Operations[0].ReviewID == 0 || len(report.Confirmations) != 0 {
				t.Fatal("mismatch confirmed", report, err)
			}
			f.badReadback = false
			f.readError = false
			report, err = s.Reconcile(context.Background(), p.ID)
			if err != nil || len(report.Confirmations) != 1 || f.posts != 1 {
				t.Fatal("known ID reconciliation failed", report, err)
			}
		})
	}
}
func TestCrashIntentRequiresReconciliation(t *testing.T) {
	s, f, _ := setup(t)
	p := makePlan(t, s, 1)
	o := core.Operation{ID: core.NewID(), PlanID: p.ID, Project: "demo", PR: 1, SHA: f.snapshot.Head, Actor: f.snapshot.Actor, CreatedAt: core.Now()}
	if err := s.Store.Claim(o); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(context.Background(), p.ID); err == nil || f.posts != 0 {
		t.Fatal("crashed intent retried")
	}
	if _, err := s.Reconcile(context.Background(), p.ID); err == nil {
		t.Fatal("absent write reset")
	}
}
func TestAlreadyApprovedDoesNotDuplicate(t *testing.T) {
	s, f, _ := setup(t)
	f.reviews = []core.RemoteReview{{ID: 25, Actor: "reviewer", State: "APPROVED", SHA: f.snapshot.Head, SubmittedAt: core.Now()}}
	p := makePlan(t, s, 1)
	if !p.Items[0].AlreadyApproved {
		t.Fatal("existing approval missed")
	}
	if _, err := s.Apply(context.Background(), p.ID); err != nil || f.posts != 0 {
		t.Fatal("already approved duplicated", err)
	}
}

func TestPartialBatchRetainsConfirmedAndUnknownOutcomes(t *testing.T) {
	s, f, _ := setup(t)
	if _, err := s.Store.Record(core.LocalReview{Project: "demo", PR: 3, SHA: f.snapshot.Head, Reviewer: "human", Method: "manual", Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	p := makePlan(t, s, 1, 2, 3)
	f.unknownAt = 2
	r, err := s.Apply(context.Background(), p.ID)
	if err == nil || f.posts != 2 || len(r.Confirmations) != 1 || len(r.Operations) != 2 || r.Operations[0].State != "confirmed" || r.Operations[1].State != "unknown" {
		t.Fatal("partial outcomes lost", r, err)
	}
}

func TestMarkerReconciliationRejectsAmbiguityAndIdentityMismatch(t *testing.T) {
	for _, mode := range []string{"duplicate", "actor", "commit", "state", "timestamp", "body"} {
		t.Run(mode, func(t *testing.T) {
			s, f, _ := setup(t)
			p := makePlan(t, s, 1)
			f.unknown = true
			_, _ = s.Apply(context.Background(), p.ID)
			switch mode {
			case "duplicate":
				f.reviews = append(f.reviews, f.reviews[0])
			case "actor":
				f.reviews[0].Actor = "other"
			case "commit":
				f.reviews[0].SHA = strings.Repeat("c", 40)
			case "state":
				f.reviews[0].State = "DISMISSED"
			case "timestamp":
				f.reviews[0].SubmittedAt = "invalid"
			case "body":
				f.reviews[0].Body = "edited"
			}
			r, err := s.Reconcile(context.Background(), p.ID)
			if err == nil || len(r.Confirmations) != 0 || f.posts != 1 {
				t.Fatal("invalid marker confirmed", r, err)
			}
		})
	}
}

func TestPlannedNoOpCannotBecomeWriteAfterDismissal(t *testing.T) {
	s, f, _ := setup(t)
	f.reviews = []core.RemoteReview{{ID: 25, Actor: "reviewer", State: "APPROVED", SHA: f.snapshot.Head, SubmittedAt: core.Now()}}
	p := makePlan(t, s, 1)
	f.reviews[0].State = "DISMISSED"
	if _, err := s.Apply(context.Background(), p.ID); err == nil || f.posts != 0 {
		t.Fatal("no-op became unauthorized write")
	}
}
