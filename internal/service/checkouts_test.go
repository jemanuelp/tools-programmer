package service

import (
	"context"
	"path/filepath"
	"testing"

	"tools-programmer/internal/core"
	"tools-programmer/internal/gitidentity"
	"tools-programmer/internal/store"
)

func TestCheckProjectRejectsConcurrentRevocation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "private", "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := db.AddProject(core.Project{Name: "demo", Repo: "example/demo", Path: t.TempDir(), ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := db.ChangeCheckout(context.Background(), p, root, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: db}
	r, err := svc.checkProject(context.Background(), p.Name, func(context.Context, core.Project) (gitidentity.Result, error) {
		current, err := db.Project(p.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ChangeCheckout(context.Background(), current, root, false, nil); err != nil {
			t.Fatal(err)
		}
		return gitidentity.Result{Project: p.Name, Checkout: root, Matches: true}, nil
	})
	if err == nil || r.Matches {
		t.Fatal("concurrently revoked checkout accepted", r, err)
	}
}

func TestCheckProjectRejectsAuthorizationABA(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "private", "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := db.AddProject(core.Project{Name: "demo", Repo: "example/demo", ExpectedGitHub: "reviewer", GitName: "Review User", GitEmail: "review@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if _, err := db.ChangeCheckout(context.Background(), p, root, true, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: db}
	result, err := svc.checkProject(context.Background(), p.Name, func(context.Context, core.Project) (gitidentity.Result, error) {
		for _, add := range []bool{false, true} {
			current, err := db.Project(p.Name)
			if err != nil {
				t.Fatal(err)
			}
			var inspect func() error
			if add {
				inspect = func() error { return nil }
			}
			if _, err := db.ChangeCheckout(context.Background(), current, root, add, inspect); err != nil {
				t.Fatal(err)
			}
		}
		return gitidentity.Result{Checkout: root, Matches: true}, nil
	})
	failure, ok := err.(*core.Error)
	if !ok || failure.Code != "project_conflict" || result.Matches {
		t.Fatal(result, err)
	}
}
