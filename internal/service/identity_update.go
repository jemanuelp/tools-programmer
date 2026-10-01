package service

import (
	"context"

	"tools-programmer/internal/core"
	"tools-programmer/internal/gitidentity"
	"tools-programmer/internal/store"
)

// UpdateGitIdentity is a native operation in the actual committing environment.
// The explicit flag records authorization provenance, not a human attestation.
func (s *Service) UpdateGitIdentity(ctx context.Context, project, name, email, checkout string, authorized bool) (store.IdentityReceipt, error) {
	return s.updateGitIdentity(ctx, project, name, email, checkout, authorized, gitidentity.CheckAt)
}

func (s *Service) updateGitIdentity(ctx context.Context, project, name, email, checkout string, authorized bool, inspect func(context.Context, core.Project, string) (gitidentity.Result, error)) (store.IdentityReceipt, error) {
	r := store.IdentityReceipt{}
	if !authorized {
		return r, core.Fail("authorization_required", "explicit user authorization for this project and proposed Git tuple is required; pass --authorized only after obtaining it")
	}
	if !core.ValidName(project) {
		return r, core.Fail("invalid_input", "exact registered project is required")
	}
	p, err := s.Store.Project(project)
	if err != nil {
		return r, err
	}
	if err := p.ValidateIdentity(); err != nil {
		return r, err
	}
	proposed := p
	proposed.GitName, proposed.GitEmail, proposed.Path = name, email, ""
	if err := proposed.ValidateIdentity(); err != nil {
		return r, err
	}
	first, err := inspect(ctx, proposed, checkout)
	if err != nil {
		return r, err
	}
	return s.Store.UpdateGitIdentity(ctx, p, name, email, func() error {
		last, err := inspect(ctx, proposed, checkout)
		if err != nil {
			return err
		}
		if last != first {
			return core.Fail("checkout_changed", "checkout evidence changed during validation; no identity update committed")
		}
		return nil
	})
}
