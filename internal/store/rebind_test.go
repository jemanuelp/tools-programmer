package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tools-programmer/internal/core"
)

func TestRebindCASAuditAndHistory(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	previous, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	review := record(t, s, "one", strings.Repeat("a", 40), "approved")
	plan := core.Plan{ID: "old-plan", Project: previous}
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	operation := core.Operation{ID: "unresolved", PlanID: plan.ID, Project: "one", PR: 7, SHA: review.SHA, Actor: "reviewer", State: "intent", CreatedAt: core.Now()}
	if err := s.Claim(operation); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "new")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{"path", "repo", "origin_url", "expected_github", "git_name", "git_email", "allow_no_checks", "created_at"} {
		t.Run(policy, func(t *testing.T) {
			// Simulate a concurrent writer changing exactly one observed field.
			value := any("changed")
			if policy == "allow_no_checks" {
				value = 1
			}
			if _, err := s.DB.Exec("UPDATE projects SET "+policy+"=? WHERE name='one'", value); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RebindCheckout(context.Background(), previous, root); err == nil {
				t.Fatal("stale policy accepted")
			}
			if _, err := s.DB.Exec("UPDATE projects SET "+policy+"=? WHERE name='one'", func() any {
				switch policy {
				case "path":
					return previous.Path
				case "repo":
					return previous.Repo
				case "origin_url":
					return previous.OriginURL
				case "expected_github":
					return previous.ExpectedGitHub
				case "git_name":
					return previous.GitName
				case "git_email":
					return previous.GitEmail
				case "created_at":
					return previous.CreatedAt
				default:
					return 0
				}
			}()); err != nil {
				t.Fatal(err)
			}
		})
	}
	updated, err := s.RebindCheckout(context.Background(), previous, root)
	if err != nil || updated.Path != root || updated.CreatedAt != previous.CreatedAt {
		t.Fatal(updated, err)
	}
	if _, err := s.RebindCheckout(context.Background(), previous, filepath.Join(t.TempDir(), "other")); err == nil {
		t.Fatal("stale competing checkout won")
	}
	if _, err := s.RebindCheckout(context.Background(), updated, root); err != nil {
		t.Fatal(err)
	}
	events, err := s.CheckoutRebindings("one")
	if err != nil || len(events) != 1 || events[0].ID <= 0 || events[0].PathBefore != "" || events[0].PathAfter != root || events[0].Result != "success" {
		t.Fatal(events, err)
	}
	if _, err := time.Parse(time.RFC3339Nano, events[0].RecordedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DELETE FROM checkout_rebindings"); err == nil {
		t.Fatal("audit deletable")
	}
	if _, err := s.DB.Exec("UPDATE checkout_rebindings SET result='success'"); err == nil {
		t.Fatal("audit mutable")
	}
	actualPlan, err := s.Plan(plan.ID)
	if err != nil || actualPlan.Project != previous {
		t.Fatal(actualPlan, err)
	}
	if unresolved, err := s.Unresolved(); err != nil || unresolved != plan.ID {
		t.Fatal(unresolved, err)
	}
	reviews, err := s.Reviews("one", 7)
	if err != nil || len(reviews) != 1 || reviews[0].ID != review.ID {
		t.Fatal(reviews, err)
	}
	ctx, err := s.CurrentActivity("runtime", "session", root)
	if err != nil || ctx.ProjectResolution != "matched" || ctx.Project != "one" {
		t.Fatal(ctx, err)
	}
	secondRoot := filepath.Join(t.TempDir(), "second")
	if err := os.Mkdir(secondRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RebindCheckout(context.Background(), updated, secondRoot); err != nil {
		t.Fatal(err)
	}
	ctx, err = s.CurrentActivity("runtime", "session", root)
	if err != nil || ctx.ProjectResolution == "matched" {
		t.Fatal(ctx, err)
	}
	ctx, err = s.CurrentActivity("runtime", "session", secondRoot)
	if err != nil || ctx.ProjectResolution != "matched" {
		t.Fatal(ctx, err)
	}
}

func TestV5ToV6PreservesProjectAndPlan(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	project, _ := s.Project("one")
	if err := s.SavePlan(core.Plan{ID: "legacy", Project: project}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("DROP TABLE identity_changes; DROP TABLE checkout_rebindings; PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if other, err := OpenReadOnly(path); err == nil {
		other.Close()
		t.Fatal("read-only open migrated v5")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Project("one")
	if err != nil || got != project {
		t.Fatal(got, err)
	}
	plan, err := s.Plan("legacy")
	if err != nil || plan.Project != project {
		t.Fatal(plan, err)
	}
	if events, err := s.CheckoutRebindings("one"); err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
}

func TestRebindSameCanonicalRootLeavesExistingAliasUntouched(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE projects SET path=? WHERE name='one'", alias); err != nil {
		t.Fatal(err)
	}
	before, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.RebindCheckout(context.Background(), before, root)
	if err != nil || after != before {
		t.Fatal(after, err)
	}
	events, err := s.CheckoutRebindings("one")
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
}

func TestConcurrentRebindOnlyOneWins(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	old, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []struct {
		db   *Store
		root string
	}{{s, t.TempDir()}, {second, t.TempDir()}} {
		wg.Add(1)
		go func(db *Store, root string) {
			defer wg.Done()
			<-start
			_, err := db.RebindCheckout(context.Background(), old, root)
			results <- err
		}(candidate.db, candidate.root)
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		var typed *core.Error
		switch {
		case err == nil:
			successes++
		case errors.As(err, &typed) && typed.Code == "project_conflict":
			conflicts++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	events, err := s.CheckoutRebindings("one")
	if err != nil || successes != 1 || conflicts != 1 || len(events) != 1 {
		t.Fatal(successes, conflicts, events, err)
	}
}
