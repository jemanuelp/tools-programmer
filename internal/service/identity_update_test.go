package service

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"tools-programmer/internal/core"
	"tools-programmer/internal/gitidentity"
	"tools-programmer/internal/store"
)

func updateIdentity(t *testing.T, db *store.Store, name, email string) store.IdentityReceipt {
	t.Helper()
	p, err := db.Project("demo")
	if err != nil {
		t.Fatal(err)
	}
	r, err := db.UpdateGitIdentity(context.Background(), p, name, email, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestIdentityAlternationNeverRevivesPlansAndNoOpPreservesEligibility(t *testing.T) {
	s, f, _ := setup(t)
	var old []core.Plan
	for _, tuple := range [][2]string{{"New User", "new@example.com"}, {"Review User", "review@example.com"}, {"New User", "new@example.com"}, {"Review User", "review@example.com"}} {
		p := makePlan(t, s, 1)
		if !p.Items[0].Eligible {
			t.Fatal("new plan not eligible", p)
		}
		old = append(old, p)
		r := updateIdentity(t, s.Store, tuple[0], tuple[1])
		if r.Result != "changed" || r.IdentityRevision != int64(len(old)) {
			t.Fatal(r)
		}
		for _, prior := range old {
			if _, err := s.Apply(context.Background(), prior.ID); err == nil {
				t.Fatal("old plan revived", prior.ID)
			}
			stored, err := s.Store.Plan(prior.ID)
			if err != nil || !reflect.DeepEqual(stored, prior) {
				t.Fatal("plan snapshot rewritten", err)
			}
		}
		projects, err := s.Store.Projects()
		if err != nil || len(projects) != 1 || projects[0].GitName != tuple[0] || projects[0].GitEmail != tuple[1] || projects[0].ExpectedGitHub != "reviewer" {
			t.Fatal(projects, err)
		}
	}
	if f.posts != 0 {
		t.Fatal("stale plans wrote", f.posts)
	}
	fresh := makePlan(t, s, 1)
	before, _ := s.Store.Project("demo")
	for range 2 {
		r := updateIdentity(t, s.Store, before.GitName, before.GitEmail)
		if r.Result != "no_op" || r.IdentityRevision != before.IdentityRevision || r.AuditID != 0 {
			t.Fatal(r)
		}
	}
	after, _ := s.Store.Project("demo")
	if after != before {
		t.Fatal("no-op changed policy")
	}
	var count int
	if err := s.Store.DB.QueryRow("SELECT count(*) FROM identity_changes").Scan(&count); err != nil || count != 4 {
		t.Fatal(count, err)
	}
	if report, err := s.Apply(context.Background(), fresh.ID); err != nil || len(report.Confirmations) != 1 || f.posts != 1 {
		t.Fatal("no-op invalidated current plan", report, err)
	}
}

type identityRaceGitHub struct {
	GitHub
	inspect func()
	approve func()
}

func (g *identityRaceGitHub) Inspect(ctx context.Context, p core.Project, n int) (core.Snapshot, error) {
	if g.inspect != nil {
		g.inspect()
	}
	return g.GitHub.Inspect(ctx, p, n)
}
func (g *identityRaceGitHub) Approve(ctx context.Context, p core.Project, o core.Operation) (core.RemoteReview, error) {
	if g.approve != nil {
		g.approve()
	}
	return g.GitHub.Approve(ctx, p, o)
}

func waitIdentityBarrier(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("approval never reached barrier")
	}
}

func TestIdentityUpdateAfterApplyPolicyCheckBeforeClaimBlocksIntent(t *testing.T) {
	s, f, path := setup(t)
	plan := makePlan(t, s, 1)
	writer, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ready, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.GitHub = &identityRaceGitHub{GitHub: f, inspect: func() {
		once.Do(func() {
			close(ready)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}}
	done := make(chan error, 1)
	go func() { _, err := s.Apply(ctx, plan.ID); done <- err }()
	waitIdentityBarrier(t, ready) // Inspect starts only after Apply's policy equality check.
	r := updateIdentity(t, writer, "New User", "new@example.com")
	if r.Result != "changed" {
		t.Fatal(r)
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stale apply succeeded")
		}
	case <-ctx.Done():
		t.Fatal("apply did not finish")
	}
	ops, err := s.Store.Operations(plan.ID)
	if err != nil || len(ops) != 0 || f.posts != 0 {
		t.Fatal("stale plan acquired intent or sent write", ops, err, f.posts)
	}
}

func TestClaimBeforeIdentityUpdateRetainsSentUnknownAndConfirmedOperations(t *testing.T) {
	for _, scenario := range []string{"confirmed", "unknown_marker", "unknown_known_id"} {
		t.Run(scenario, func(t *testing.T) {
			s, f, path := setup(t)
			plan := makePlan(t, s, 1)
			f.unknown = scenario == "unknown_marker"
			f.readError = scenario == "unknown_known_id"
			writer, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			ready, release := make(chan struct{}), make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			s.GitHub = &identityRaceGitHub{GitHub: f, approve: func() {
				close(ready)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}}
			done := make(chan error, 1)
			go func() { _, err := s.Apply(ctx, plan.ID); done <- err }()
			waitIdentityBarrier(t, ready) // Claim committed before Approve is invoked.
			before, err := writer.Operations(plan.ID)
			if err != nil || len(before) != 1 || before[0].State != "intent" {
				t.Fatal(before, err)
			}
			updateIdentity(t, writer, "New User", "new@example.com")
			after, err := writer.Operations(plan.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("identity change reset acquired intent", after, err)
			}
			if _, err := s.Reconcile(ctx, plan.ID); err == nil {
				t.Fatal("unsent acquired intent incorrectly reconciled")
			}
			if still, err := writer.Operations(plan.ID); err != nil || !reflect.DeepEqual(still, before) {
				t.Fatal("read-only reconciliation reset unsent intent", still, err)
			}
			close(release)
			select {
			case err := <-done:
				if (err == nil) != (scenario == "confirmed") {
					t.Fatal("unexpected apply result", err)
				}
			case <-ctx.Done():
				t.Fatal("apply did not finish")
			}
			report, err := s.Show(plan.ID)
			if err != nil || f.posts != 1 {
				t.Fatal(report, err)
			}
			if scenario != "confirmed" {
				if len(report.Operations) != 1 || report.Operations[0].State != "unknown" || len(report.Confirmations) != 0 {
					t.Fatal("unknown write reset", report)
				}
				fresh := makePlan(t, s, 2)
				if _, err := s.Apply(ctx, fresh.ID); err == nil {
					t.Fatal("unknown did not block new writes")
				}
				if _, err := s.Reconcile(ctx, plan.ID); scenario == "unknown_known_id" && err == nil {
					t.Fatal("failed readback cleared unknown")
				}
				f.readError = false
			}
			if report, err = s.Reconcile(ctx, plan.ID); err != nil || len(report.Confirmations) != 1 || report.Operations[0].State != "confirmed" || f.posts != 1 {
				t.Fatal("read-only recovery failed", report, err)
			}
			stored, err := s.Store.Plan(plan.ID)
			if err != nil || !reflect.DeepEqual(stored, plan) {
				t.Fatal("old plan changed", err)
			}
			// Confirmations also survive later changes and reconciliation does no POST.
			updateIdentity(t, s.Store, "Review User", "review@example.com")
			if got, err := s.Reconcile(ctx, plan.ID); err != nil || !reflect.DeepEqual(got.Confirmations, report.Confirmations) || f.posts != 1 {
				t.Fatal(got, err)
			}
		})
	}
}

func TestNativeIdentityRevalidatesEvidenceAndPolicy(t *testing.T) {
	for _, scenario := range []string{"no_consent", "origin_changed", "checkout_changed", "author_changed", "committer_changed", "policy_changed", "second_validation_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, path := setup(t)
			writer, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			p, _ := s.Store.Project("demo")
			calls := 0
			inspect := func(ctx context.Context, proposed core.Project, path string) (gitidentity.Result, error) {
				calls++
				if proposed.Path != "" || proposed.GitName != "New" || proposed.GitEmail != "new@example.com" {
					t.Fatal("wrong validation policy", proposed)
				}
				result := gitidentity.Result{Project: "demo", Checkout: "/checkout", Origin: "https://github.com/example/demo", Author: "New <new@example.com>", Committer: "New <new@example.com>", Matches: true}
				if calls == 1 && scenario == "policy_changed" {
					if _, err := writer.DB.Exec("UPDATE projects SET path='/changed' WHERE name='demo'"); err != nil {
						t.Fatal(err)
					}
				}
				if calls == 2 {
					switch scenario {
					case "origin_changed":
						result.Origin += ".git"
					case "checkout_changed":
						result.Checkout = "/different"
					case "author_changed":
						result.Author = "Other"
					case "committer_changed":
						result.Committer = "Other"
					case "second_validation_failure":
						return result, core.Fail("identity_mismatch", "fixture")
					}
				}
				return result, nil
			}
			if _, err := s.updateGitIdentity(context.Background(), "demo", "New", "new@example.com", "/checkout", scenario != "no_consent", inspect); err == nil {
				t.Fatal("invalid request succeeded")
			}
			if scenario == "no_consent" && calls != 0 {
				t.Fatal("inspected before consent")
			}
			after, _ := s.Store.Project("demo")
			if scenario == "policy_changed" {
				p.Path = "/changed"
			}
			if after != p {
				t.Fatal("rejection changed policy", after)
			}
			var count int
			if err := s.Store.DB.QueryRow("SELECT count(*) FROM identity_changes").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}
