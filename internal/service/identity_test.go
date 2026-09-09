package service

import (
	"context"
	"encoding/json"
	"testing"
	"tools-programmer/internal/core"
)

type identityGitHub struct {
	*fakeGitHub
	actor       string
	inspections int
	switchAt    int
}

func (f *identityGitHub) Inspect(ctx context.Context, p core.Project, n int) (core.Snapshot, error) {
	f.inspections++
	s, err := f.fakeGitHub.Inspect(ctx, p, n)
	if f.switchAt > 0 && f.inspections >= f.switchAt {
		s.Actor = "other"
	}
	return s, err
}
func (f *identityGitHub) Reviews(ctx context.Context, p core.Project, n int) ([]core.RemoteReview, error) {
	if err := p.CheckActor(f.actor); err != nil {
		return nil, err
	}
	return f.fakeGitHub.Reviews(ctx, p, n)
}
func (f *identityGitHub) GetReview(ctx context.Context, p core.Project, n int, id int64) (core.RemoteReview, error) {
	if err := p.CheckActor(f.actor); err != nil {
		return core.RemoteReview{}, err
	}
	return f.fakeGitHub.GetReview(ctx, p, n, id)
}

func TestActorMismatchBlocksPlanningAndFreshApply(t *testing.T) {
	for _, switchAt := range []int{1, 2, 3} {
		t.Run(map[int]string{1: "planning", 2: "batch preflight", 3: "fresh item preflight"}[switchAt], func(t *testing.T) {
			s, base, _ := setup(t)
			f := &identityGitHub{fakeGitHub: base, actor: "reviewer", switchAt: switchAt}
			s.GitHub = f
			p, err := s.Plan(context.Background(), "demo", []int{1})
			if switchAt != 1 {
				if err != nil {
					t.Fatal(err)
				}
				_, err = s.Apply(context.Background(), p.ID)
			}
			if err == nil || base.posts != 0 {
				t.Fatal("wrong identity accepted", err, base.posts)
			}
			if id, err := s.Store.Unresolved(); err != nil || id != "" {
				t.Fatal("preflight left intent", id, err)
			}
		})
	}
}

func TestLegacyPlanCannotApplyButBoundIdentityCanReconcile(t *testing.T) {
	s, base, _ := setup(t)
	f := &identityGitHub{fakeGitHub: base, actor: "reviewer"}
	s.GitHub = f
	p, err := s.Plan(context.Background(), "demo", []int{1})
	if err != nil {
		t.Fatal(err)
	}
	base.unknown = true
	if _, err = s.Apply(context.Background(), p.ID); err == nil || base.posts != 1 {
		t.Fatal("expected ambiguous fake write", err)
	}
	// Emulate a pre-migration project and immutable v1 plan with an unknown write.
	p.Project.ExpectedGitHub = ""
	p.Project.GitName = ""
	p.Project.GitEmail = ""
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec("UPDATE plans SET payload=? WHERE id=?", string(b), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.DB.Exec("UPDATE projects SET expected_github='',git_name='',git_email='' WHERE name='demo'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Reconcile(context.Background(), p.ID); err == nil {
		t.Fatal("unbound recovery accepted")
	}
	if _, err = s.Store.BindProject("demo", "example/demo", "reviewer", "Review User", "review@example.com"); err != nil {
		t.Fatal(err)
	}
	f.actor = "other"
	r, err := s.Reconcile(context.Background(), p.ID)
	if err == nil || len(r.Confirmations) != 0 || len(r.Operations) != 1 || r.Operations[0].State != "unknown" {
		t.Fatal("wrong actor lost unknown state", r, err)
	}
	f.actor = "reviewer"
	r, err = s.Reconcile(context.Background(), p.ID)
	if err != nil || len(r.Confirmations) != 1 || base.posts != 1 {
		t.Fatal("safe recovery failed", r, err)
	}
	if _, err = s.Apply(context.Background(), p.ID); err == nil || base.posts != 1 {
		t.Fatal("legacy plan became executable")
	}
	stored, err := s.Store.Plan(p.ID)
	if err != nil || stored.Project.ExpectedGitHub != "" {
		t.Fatal("legacy plan rewritten", err)
	}
}
