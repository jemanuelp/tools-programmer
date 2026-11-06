package service

import (
	"context"
	"path/filepath"

	"tools-programmer/internal/core"
	"tools-programmer/internal/gitidentity"
	"tools-programmer/internal/store"
)

// CheckProject checks only the invoking checkout; stored paths are not executed.
// The final policy read detects concurrent authorization changes during Git I/O.
func (s *Service) CheckProject(ctx context.Context, project string) (gitidentity.Result, error) {
	return s.checkProject(ctx, project, nil)
}

func (s *Service) checkProject(ctx context.Context, project string, inspect func(context.Context, core.Project) (gitidentity.Result, error)) (gitidentity.Result, error) {
	p, err := s.Store.Project(project)
	if err != nil {
		return gitidentity.Result{}, err
	}
	roots, err := s.Store.Checkouts(project)
	if err != nil {
		return gitidentity.Result{}, err
	}
	if inspect == nil {
		inspect = func(ctx context.Context, p core.Project) (gitidentity.Result, error) {
			paths := make([]string, 0, len(roots))
			for _, c := range roots {
				paths = append(paths, c.Path)
			}
			return gitidentity.CheckAuthorized(ctx, p, paths)
		}
	}
	r, err := inspect(ctx, p)
	if err != nil {
		return r, err
	}
	// Preserve legacy pathless registration behavior until a checkout is bound.
	allowed := len(roots) == 0 && p.Path == "" && p.CheckoutRevision == 0
	for _, c := range roots {
		root, err := filepath.EvalSymlinks(c.Path)
		if err == nil && root == r.Checkout {
			allowed = true
		}
	}
	r.Matches = false
	if !allowed {
		return r, core.Fail("checkout_mismatch", "current checkout is not authorized for this project")
	}
	current, err := s.Store.Project(project)
	if err != nil {
		return r, err
	}
	if current != p {
		return r, core.Fail("project_conflict", "project policy or checkout authorization changed during validation; inspect and retry")
	}
	r.Matches = true
	return r, nil
}

func (s *Service) ChangeCheckout(ctx context.Context, project, path string, add, authorized bool) (store.CheckoutChange, error) {
	if !authorized {
		return store.CheckoutChange{}, core.Fail("authorization_required", "obtain explicit authorization for this project and checkout, then pass --authorized")
	}
	if !filepath.IsAbs(path) {
		return store.CheckoutChange{}, core.Fail("invalid_input", "checkout path must be absolute")
	}
	p, err := s.Store.Project(project)
	if err != nil {
		return store.CheckoutChange{}, err
	}
	if !add {
		// Revoke the recorded authorization key, not its current symlink target.
		// This also works after deletion or replacement of the original directory.
		return s.Store.ChangeCheckout(ctx, p, filepath.Clean(path), false, nil)
	}
	validation := p
	validation.Path = ""
	check, err := gitidentity.CheckAt(ctx, validation, path)
	if err != nil {
		return store.CheckoutChange{}, err
	}
	return s.Store.ChangeCheckout(ctx, p, check.Checkout, true, func() error {
		again, err := gitidentity.CheckAt(ctx, validation, path)
		if err != nil {
			return err
		}
		if again != check {
			return core.Fail("checkout_mismatch", "checkout changed during authorization")
		}
		return nil
	})
}
