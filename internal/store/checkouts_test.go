package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tools-programmer/internal/core"
)

func TestCheckoutAuthorizationCASAuditAndHistory(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	p, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	review := record(t, s, "one", strings.Repeat("a", 40), "approved")
	plan := core.Plan{ID: "before-checkouts", Project: p}
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	op := core.Operation{ID: "unknown-checkout", PlanID: plan.ID, Project: p.Name, PR: 7, SHA: review.SHA, Actor: "reviewer", State: "unknown", CreatedAt: core.Now()}
	if err := s.Claim(op); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := s.ChangeCheckout(context.Background(), p, root, true, func() error { return errors.New("inspection failed") }); err == nil {
		t.Fatal("failed inspection accepted")
	}
	if roots, err := s.Checkouts(p.Name); err != nil || len(roots) != 0 {
		t.Fatal("failed inspection persisted", roots, err)
	}
	change, err := s.ChangeCheckout(context.Background(), p, root, true, func() error { return nil })
	if err != nil || change.Action != "authorized" || change.Revision != 1 {
		t.Fatal(change, err)
	}
	if _, err := s.ChangeCheckout(context.Background(), p, root, false, nil); err == nil {
		t.Fatal("stale policy accepted")
	}
	current, _ := s.Project(p.Name)
	noop, err := s.ChangeCheckout(context.Background(), current, root, true, func() error { return nil })
	if err != nil || noop.Action != "no_op" || noop.Revision != 1 {
		t.Fatal(noop, err)
	}
	change, err = s.ChangeCheckout(context.Background(), current, root, false, nil)
	if err != nil || change.Action != "revoked" || change.Revision != 2 {
		t.Fatal(change, err)
	}
	current, _ = s.Project(p.Name)
	if _, err := s.ChangeCheckout(context.Background(), current, root, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Project(p.Name)
	if current.CheckoutRevision != 3 || current.IdentityRevision != p.IdentityRevision || current.Path != p.Path {
		t.Fatal(current)
	}
	if stored, err := s.Plan(plan.ID); err != nil || stored.Project != p || stored.Project == current {
		t.Fatal("plan history or freshness lost", stored, err)
	}
	if unresolved, err := s.Unresolved(); err != nil || unresolved != plan.ID {
		t.Fatal("unresolved operation lost", unresolved, err)
	}
	if reviews, err := s.Reviews(p.Name, 7); err != nil || len(reviews) != 1 || reviews[0].ID != review.ID {
		t.Fatal(reviews, err)
	}
	for _, query := range []string{"DELETE FROM checkout_changes", "UPDATE checkout_changes SET action='authorized'"} {
		if _, err := s.DB.Exec(query); err == nil {
			t.Fatal("audit modified", query)
		}
	}
}

func TestConcurrentCheckoutAuthorizationRejectsStalePolicy(t *testing.T) {
	s, dbPath := openTest(t)
	register(t, s, "one")
	p, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, db := range []*Store{s, other} {
		root := t.TempDir()
		wg.Add(1)
		go func(db *Store, root string) {
			defer wg.Done()
			<-start
			_, err := db.ChangeCheckout(context.Background(), p, root, true, func() error { return nil })
			results <- err
		}(db, root)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			var failure *core.Error
			if !errors.As(err, &failure) || failure.Code != "project_conflict" {
				t.Fatal(err)
			}
		}
	}
	if roots, err := s.Checkouts(p.Name); err != nil || len(roots) != 1 || successes != 1 {
		t.Fatal(roots, successes, err)
	}
}

func TestCheckoutMigrationPreservesVersion7Project(t *testing.T) {
	s, dbPath := openTest(t)
	register(t, s, "one")
	p, _ := s.Project("one")
	p, err := s.RebindCheckout(context.Background(), p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DROP TABLE project_checkouts; DROP TABLE checkout_changes; PRAGMA user_version=7"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if db, err := OpenReadOnly(dbPath); err == nil {
		db.Close()
		t.Fatal("read-only access migrated old schema")
	}
	s, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Project(p.Name); err != nil || got != p {
		t.Fatal(got, err)
	}
	if roots, err := s.Checkouts(p.Name); err != nil || len(roots) != 1 || roots[0].Path != p.Path || !roots[0].Primary {
		t.Fatal(roots, err)
	}
	if _, err := s.ChangeCheckout(context.Background(), p, filepath.Join(t.TempDir(), "missing"), false, nil); err != nil {
		t.Fatal("offline revocation must be idempotent", err)
	}
}

func TestCheckoutDiscoveryDeduplicatesProjectAndRejectsAmbiguity(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	register(t, s, "two")
	root := t.TempDir()
	p, _ := s.Project("one")
	p, err := s.RebindCheckout(context.Background(), p, root)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeCheckout(context.Background(), p, child, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	activity, err := s.CurrentActivity("opencode", "native", child)
	if err != nil || activity.ProjectResolution != "matched" || activity.Project != "one" {
		t.Fatal("same project roots were ambiguous", activity, err)
	}
	other, _ := s.Project("two")
	if _, err := s.ChangeCheckout(context.Background(), other, child, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	activity, err = s.CurrentActivity("opencode", "native", child)
	if err != nil || activity.ProjectResolution != "ambiguous" || activity.Project != "" {
		t.Fatal("cross-project ambiguity accepted", activity, err)
	}
}

func TestCheckoutAuditRollbackAndStaleClaimLeaveDatabaseUnchanged(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	p, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	review := record(t, s, "one", strings.Repeat("b", 40), "approved")
	if err := s.SavePlan(core.Plan{ID: "old", Project: p}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, add := range []bool{true, false, true} {
		current, err := s.Project("one")
		if err != nil {
			t.Fatal(err)
		}
		var validate func() error
		if add {
			validate = func() error { return nil }
		}
		if _, err := s.ChangeCheckout(context.Background(), current, root, add, validate); err != nil {
			t.Fatal(err)
		}
	}
	hash := func() [32]byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(data)
	}
	before := hash()
	if err := s.Claim(core.Operation{ID: "stale", PlanID: "old", Project: "one", PR: 7, SHA: review.SHA, Actor: p.ExpectedGitHub, CreatedAt: core.Now()}); err == nil {
		t.Fatal("stale plan claimed after revoke/re-authorize")
	}
	if hash() != before {
		t.Fatal("rejected claim changed database")
	}
	if _, err := s.DB.Exec("CREATE TRIGGER reject_checkout_audit BEFORE INSERT ON checkout_changes BEGIN SELECT RAISE(ABORT,'audit unavailable'); END"); err != nil {
		t.Fatal(err)
	}
	current, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	before = hash()
	if _, err := s.ChangeCheckout(context.Background(), current, t.TempDir(), true, func() error { return nil }); err == nil {
		t.Fatal("audit failure accepted")
	}
	if hash() != before {
		t.Fatal("audit rollback changed database")
	}
}

func TestAuthorizedCheckoutSessionsShareAssignments(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	root, other := t.TempDir(), t.TempDir()
	p, _ := s.Project("one")
	p, err := s.RebindCheckout(context.Background(), p, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeCheckout(context.Background(), p, other, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	issue, err := s.AddIssue("one", 9, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"session-one", "session-two"} {
		event := core.ActivityEvent{ID: "bind-" + session, Runtime: "opencode", Session: session, Segment: "segment-" + session, At: 1, Kind: "bind", Project: "one", Issue: 9}
		if err := s.RecordActivity(event); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{root, other} {
			activity, err := s.CurrentActivity("opencode", session, dir)
			if err != nil || activity.Project != "one" || activity.Assignment != "assigned" || activity.Issue == nil || activity.Issue.ID != issue.ID {
				t.Fatal(activity, err)
			}
		}
	}
}
