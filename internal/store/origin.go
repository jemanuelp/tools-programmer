package store

import (
	"context"
	"database/sql"
	"tools-programmer/internal/core"
)

// SetProjectOrigin replaces only an explicit project-scoped remote association.
// It is a trusted local declaration, not evidence of where SSH connects.
func (s *Store) SetProjectOrigin(name, repository, raw string) (core.Project, error) {
	p, err := s.Project(name)
	if err != nil {
		return p, err
	}
	canonical, err := core.CanonicalRepo(repository)
	if err != nil || canonical != p.Repo {
		return p, core.Fail("project_conflict", "confirm the registered canonical repository; origin cannot rebind it")
	}
	if err := p.ValidateIdentity(); err != nil {
		return p, err
	}
	if err := p.ValidateOrigin(raw); err != nil {
		return p, err
	}
	origin, _ := core.OriginRepo(raw)
	ctx := s.context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if err := checkOriginAvailable(ctx, tx, name, origin); err != nil {
		return p, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE projects SET origin_url=? WHERE name=? AND repo=?", raw, name, canonical); err != nil {
		return p, err
	}
	if err := tx.Commit(); err != nil {
		return p, err
	}
	p.OriginURL = raw
	return p, nil
}

func checkOriginAvailable(ctx context.Context, tx *sql.Tx, name, origin string) error {
	rows, err := tx.QueryContext(ctx, "SELECT repo,origin_url FROM projects WHERE name<>?", name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var repo, raw string
		if err := rows.Scan(&repo, &raw); err != nil {
			return err
		}
		associated, _ := core.OriginRepo(raw)
		if origin == repo || origin == associated {
			return core.Fail("project_conflict", "origin identity is already associated with another project")
		}
	}
	return rows.Err()
}
