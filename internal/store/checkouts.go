package store

import (
	"context"
	"os"
	"path/filepath"

	"tools-programmer/internal/core"
)

type Checkout struct {
	Path    string `json:"path"`
	Primary bool   `json:"primary"`
}

type CheckoutChange struct {
	Project  string `json:"project"`
	Path     string `json:"path"`
	Action   string `json:"action"`
	Revision int64  `json:"checkout_revision"`
}

// Checkouts includes the original registration and explicitly added checkouts.
// It never probes stored paths with Git or infers authorization from an origin.
func (s *Store) Checkouts(project string) ([]Checkout, error) {
	rows, err := s.DB.QueryContext(s.context(), `SELECT path, max(is_primary) FROM (
		SELECT path,1 AS is_primary FROM projects WHERE name=? AND path<>''
		UNION ALL SELECT path,0 FROM project_checkouts WHERE project=?
	) GROUP BY path ORDER BY path`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Checkout{}
	for rows.Next() {
		var c Checkout
		if err := rows.Scan(&c.Path, &c.Primary); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChangeCheckout serializes a full-policy CAS, membership change and audit.
// Adding requires native local Git revalidation; revoking works offline too.
// No network I/O or alteration of project identity/history is performed.
func (s *Store) ChangeCheckout(ctx context.Context, observed core.Project, path string, add bool, revalidate func() error) (CheckoutChange, error) {
	r := CheckoutChange{Project: observed.Name, Path: path, Action: "no_op", Revision: observed.CheckoutRevision}
	if err := observed.ValidateIdentity(); err != nil {
		return r, err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return r, core.Fail("invalid_input", "checkout path must be absolute and canonical")
	}
	if add && revalidate == nil {
		return r, core.Fail("invalid_input", "native checkout validation is required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	// Reserve the write before reading policy, including no-op requests.
	result, err := tx.ExecContext(ctx, "UPDATE projects SET path=path WHERE "+projectPolicyPredicate, projectPolicyArgs(observed)...)
	if err != nil {
		return r, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return r, core.Fail("project_conflict", "project policy or checkout authorization changed; inspect and retry")
	}
	if add {
		if err := revalidate(); err != nil {
			return r, err
		}
		root, err := filepath.EvalSymlinks(path)
		info, statErr := os.Stat(path)
		if err != nil || root != path || statErr != nil || !info.IsDir() {
			return r, core.Fail("checkout_mismatch", "validated checkout root changed")
		}
	}
	primary := observed.Path
	if resolved, err := filepath.EvalSymlinks(primary); err == nil {
		primary = resolved
	}
	if primary != "" && primary == path {
		if !add {
			return r, core.Fail("invalid_input", "cannot revoke the primary checkout; use project rebind-checkout first")
		}
		return r, tx.Commit()
	}
	if add {
		result, err = tx.ExecContext(ctx, "INSERT INTO project_checkouts(project,path) VALUES(?,?) ON CONFLICT DO NOTHING", observed.Name, path)
		r.Action = "authorized"
	} else {
		result, err = tx.ExecContext(ctx, "DELETE FROM project_checkouts WHERE project=? AND path=?", observed.Name, path)
		r.Action = "revoked"
	}
	if err != nil {
		return CheckoutChange{}, err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return CheckoutChange{}, err
	}
	if n == 0 {
		r.Action = "no_op"
	} else {
		if _, err := tx.ExecContext(ctx, "INSERT INTO checkout_changes(project,path,action,recorded_at) VALUES(?,?,?,?)", observed.Name, path, r.Action, core.Now()); err != nil {
			return CheckoutChange{}, err
		}
		r.Revision++
	}
	return r, tx.Commit()
}
