package service

import (
	"context"
	"strings"
	"time"

	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

type GitHub interface {
	Inspect(context.Context, core.Project, int) (core.Snapshot, error)
	Approve(context.Context, core.Project, core.Operation) (core.RemoteReview, error)
	GetReview(context.Context, core.Project, int, int64) (core.RemoteReview, error)
	Reviews(context.Context, core.Project, int) ([]core.RemoteReview, error)
}
type Service struct {
	Store  *store.Store
	GitHub GitHub
}
type Report struct {
	Plan          core.Plan           `json:"plan"`
	Operations    []core.Operation    `json:"operations"`
	Confirmations []core.Confirmation `json:"confirmations"`
}

func Numbers(numbers []int) error {
	if len(numbers) == 0 || len(numbers) > 50 {
		return core.Fail("invalid_input", "explicit list of 1 to 50 PR numbers required")
	}
	seen := map[int]bool{}
	for _, n := range numbers {
		if !core.ValidPR(n) || seen[n] {
			return core.Fail("invalid_input", "PR numbers must be positive and unique")
		}
		seen[n] = true
	}
	return nil
}
func (s *Service) Sync(ctx context.Context, name string, numbers []int) ([]core.Snapshot, error) {
	if err := Numbers(numbers); err != nil {
		return nil, err
	}
	p, err := s.Store.Project(name)
	if err != nil {
		return nil, err
	}
	out := []core.Snapshot{}
	if err := p.ValidateIdentity(); err != nil {
		return out, err
	}
	for _, n := range numbers {
		snap, err := s.GitHub.Inspect(ctx, p, n)
		if err != nil {
			return out, err
		}
		if err = p.CheckActor(snap.Actor); err != nil {
			return out, err
		}
		if err = s.Store.SaveSnapshot(name, snap); err != nil {
			return out, err
		}
		out = append(out, snap)
	}
	return out, nil
}
func (s *Service) Plan(ctx context.Context, name string, numbers []int) (core.Plan, error) {
	p := core.Plan{ID: core.NewID(), CreatedAt: core.Now(), Items: []core.PlanItem{}}
	if err := Numbers(numbers); err != nil {
		return p, err
	}
	project, err := s.Store.Project(name)
	if err != nil {
		return p, err
	}
	p.Project = project
	if err := project.ValidateIdentity(); err != nil {
		return p, err
	}
	for _, n := range numbers {
		snap, err := s.GitHub.Inspect(ctx, project, n)
		if err != nil {
			return p, err
		}
		if err = project.CheckActor(snap.Actor); err != nil {
			return p, err
		}
		if err = s.Store.SaveSnapshot(name, snap); err != nil {
			return p, err
		}
		approved, err := s.Store.Approved(name, n, snap.Head)
		if err != nil {
			return p, err
		}
		item := core.Evaluate(snap, approved, project.AllowNoChecks)
		op, err := s.Store.Existing(name, n, snap.Head, snap.Actor)
		if err != nil {
			return p, err
		}
		if op != nil && op.State != "confirmed" {
			item.Eligible = false
			item.Reasons = append(item.Reasons, "unresolved operation "+op.ID+" from plan "+op.PlanID+" requires reconciliation")
		}
		p.Items = append(p.Items, item)
	}
	return p, s.Store.SavePlan(p)
}
func (s *Service) Show(id string) (Report, error) {
	r := Report{}
	var err error
	r.Plan, err = s.Store.Plan(id)
	if err != nil {
		return r, err
	}
	r.Operations, err = s.Store.Operations(id)
	if err != nil {
		return r, err
	}
	r.Confirmations, err = s.Store.Confirmations(id)
	return r, err
}
func sameTarget(a, b core.Snapshot) bool {
	return a.PR == b.PR && a.Head == b.Head && a.BaseSHA == b.BaseSHA && a.BaseRef == b.BaseRef && strings.EqualFold(a.Actor, b.Actor) && strings.EqualFold(a.Author, b.Author)
}
func matches(o core.Operation, r core.RemoteReview) bool {
	_, err := time.Parse(time.RFC3339Nano, r.SubmittedAt)
	return err == nil && r.ID > 0 && strings.EqualFold(r.Actor, o.Actor) && r.State == "APPROVED" && r.SHA == o.SHA
}

func (s *Service) Apply(ctx context.Context, id string) (Report, error) {
	p, err := s.Store.Plan(id)
	if err != nil {
		return Report{}, err
	}
	current, err := s.Store.Project(p.Project.Name)
	if err != nil {
		return s.reportError(id, err)
	}
	if err = p.Project.ValidateIdentity(); err != nil {
		return s.reportError(id, core.Fail("identity_unbound", "legacy plan cannot be applied; bind the project and create a new plan"))
	}
	if current != p.Project {
		return s.reportError(id, core.Fail("identity_mismatch", "stored plan project policy differs from registration; create a new plan"))
	}
	blocked, err := s.Store.Unresolved()
	if err != nil {
		return s.reportError(id, err)
	}
	if blocked != "" {
		return s.reportError(id, core.Fail("reconciliation_required", "unresolved intent blocks all writes; reconcile plan "+blocked))
	}
	// Inspect the entire batch before its first write. Each item is inspected again
	// immediately before claiming because previous writes may take time.
	for _, item := range p.Items {
		if !item.Eligible {
			return s.reportError(id, core.Fail("plan_blocked", "plan contains ineligible items; inspect reasons and create a new plan"))
		}
		if _, err = s.preflight(ctx, p, item); err != nil {
			return s.reportError(id, err)
		}
	}
	for _, item := range p.Items {
		fresh, err := s.preflight(ctx, p, item)
		if err != nil {
			return s.reportError(id, err)
		}
		op, err := s.Store.Existing(p.Project.Name, fresh.Snapshot.PR, fresh.Snapshot.Head, fresh.Snapshot.Actor)
		if err != nil {
			return s.reportError(id, err)
		}
		if op != nil {
			if op.State != "confirmed" {
				return s.reportError(id, core.Fail("reconciliation_required", "durable intent exists; reconcile its owning plan before any further writes"))
			}
			continue
		}
		if fresh.AlreadyApproved {
			continue
		}
		o := core.Operation{ID: core.NewID(), PlanID: id, Project: p.Project.Name, PR: fresh.Snapshot.PR, SHA: fresh.Snapshot.Head, Actor: strings.ToLower(fresh.Snapshot.Actor), State: "intent", CreatedAt: core.Now()}
		if err = s.Store.Claim(o); err != nil {
			return s.reportError(id, err)
		}
		r, writeErr := s.GitHub.Approve(ctx, p.Project, o)
		if writeErr != nil || !matches(o, r) {
			return s.unknown(id, o, r.ID)
		}
		// Save known ID even if readback, process, or storage fails next.
		if err = s.Store.Unknown(o.ID, r.ID); err != nil {
			return s.reportError(id, core.Fail("unknown_outcome", "write attempted; durable intent remains; reconcile before continuing"))
		}
		readback, err := s.GitHub.GetReview(ctx, p.Project, o.PR, r.ID)
		if err != nil || readback.ID != r.ID || !matches(o, readback) {
			return s.unknown(id, o, r.ID)
		}
		if err = s.Store.Confirm(o, readback); err != nil {
			return s.reportError(id, core.Fail("unknown_outcome", "approval readback matched but persistence failed; reconcile before continuing"))
		}
	}
	return s.Show(id)
}
func (s *Service) preflight(ctx context.Context, p core.Plan, item core.PlanItem) (core.PlanItem, error) {
	fresh, err := s.GitHub.Inspect(ctx, p.Project, item.Snapshot.PR)
	if err != nil {
		return core.PlanItem{}, err
	}
	if err = p.Project.CheckActor(fresh.Actor); err != nil {
		return core.PlanItem{}, err
	}
	if err = s.Store.SaveSnapshot(p.Project.Name, fresh); err != nil {
		return core.PlanItem{}, err
	}
	if !sameTarget(item.Snapshot, fresh) {
		return core.PlanItem{}, core.Fail("stale_plan", "head, base, author, or actor changed; review the new head and create a new plan")
	}
	approved, err := s.Store.Approved(p.Project.Name, fresh.PR, fresh.Head)
	if err != nil {
		return core.PlanItem{}, err
	}
	evaluated := core.Evaluate(fresh, approved, p.Project.AllowNoChecks)
	if item.AlreadyApproved && !evaluated.AlreadyApproved {
		return evaluated, core.Fail("stale_plan", "an existing approval changed; create a new plan instead of turning a planned no-op into a write")
	}
	if !evaluated.Eligible {
		return evaluated, core.Fail("preflight_blocked", "fresh inspection is ineligible: "+strings.Join(evaluated.Reasons, "; "))
	}
	op, err := s.Store.Existing(p.Project.Name, fresh.PR, fresh.Head, fresh.Actor)
	if err != nil {
		return evaluated, err
	}
	if op != nil && op.State != "confirmed" {
		return evaluated, core.Fail("reconciliation_required", "operation "+op.ID+" in plan "+op.PlanID+" requires read-only reconciliation")
	}
	return evaluated, nil
}
func (s *Service) reportError(id string, cause error) (Report, error) {
	r, err := s.Show(id)
	if err != nil {
		return r, core.Fail("storage_error", "cannot read action state; inspect database before further actions")
	}
	return r, cause
}
func (s *Service) unknown(id string, o core.Operation, reviewID int64) (Report, error) {
	if reviewID < 0 {
		reviewID = 0
	}
	if err := s.Store.Unknown(o.ID, reviewID); err != nil {
		return s.reportError(id, core.Fail("unknown_outcome", "write attempted and state update failed; durable intent requires reconciliation"))
	}
	return s.reportError(id, core.Fail("unknown_outcome", "approval may have been published; stopped batch; run approval reconcile for this plan (read-only)"))
}
func (s *Service) Reconcile(ctx context.Context, id string) (Report, error) {
	p, err := s.Store.Plan(id)
	if err != nil {
		return Report{}, err
	}
	ops, err := s.Store.Operations(id)
	if err != nil {
		return Report{}, err
	}
	// Recovery uses explicit current policy, including for immutable legacy plans.
	current, err := s.Store.Project(p.Project.Name)
	if err != nil {
		return s.reportError(id, err)
	}
	if current.Repo != p.Project.Repo {
		return s.reportError(id, core.Fail("identity_mismatch", "reconciliation repository differs from stored plan"))
	}
	if err = current.ValidateIdentity(); err != nil {
		return s.reportError(id, err)
	}
	p.Project = current
	for _, o := range ops {
		if o.State == "confirmed" {
			continue
		}
		if err = current.CheckActor(o.Actor); err != nil {
			return s.reportError(id, err)
		}
		var found core.RemoteReview
		if o.ReviewID > 0 {
			found, err = s.GitHub.GetReview(ctx, p.Project, o.PR, o.ReviewID)
			if err != nil {
				return s.reportError(id, core.Fail("reconciliation_required", "known review could not be read; operation remains blocked"))
			}
			if found.ID != o.ReviewID {
				return s.reportError(id, core.Fail("reconciliation_required", "review ID mismatch; operation remains blocked"))
			}
		} else {
			reviews, readErr := s.GitHub.Reviews(ctx, p.Project, o.PR)
			if readErr != nil {
				return s.reportError(id, readErr)
			}
			count := 0
			for _, r := range reviews {
				if r.Body == o.Body() && matches(o, r) {
					found = r
					count++
				}
			}
			if count != 1 {
				return s.reportError(id, core.Fail("reconciliation_required", "no unique matching operation marker found; no retry is permitted"))
			}
		}
		if !matches(o, found) {
			return s.reportError(id, core.Fail("reconciliation_required", "review actor, state, or commit does not match; operation remains blocked"))
		}
		if err = s.Store.Confirm(o, found); err != nil {
			return s.reportError(id, err)
		}
	}
	return s.Show(id)
}
