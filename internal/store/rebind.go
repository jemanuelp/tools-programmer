package store

import (
	"context"
	"path/filepath"

	"tools-programmer/internal/core"
)

// CheckoutRebinding is a successful, append-only local checkout transition.
type CheckoutRebinding struct {
	ID         int64  `json:"id"`
	Project    string `json:"project"`
	PathBefore string `json:"path_before"`
	PathAfter  string `json:"path_after"`
	RecordedAt string `json:"recorded_at"`
	Result     string `json:"result"`
}

// RebindCheckout atomically compares every observed project policy field before
// changing its path and appending an audit entry. Inspect Git before calling it.
func (s *Store) RebindCheckout(ctx context.Context, observed core.Project, root string) (core.Project, error) {
	if !filepath.IsAbs(root) || root == "" {
		return observed, core.Fail("invalid_input", "validated checkout root must be absolute")
	}
	sameRoot := observed.Path == root
	if observed.Path != "" && !sameRoot {
		resolved, err := filepath.EvalSymlinks(observed.Path)
		sameRoot = err == nil && resolved == root
	}
	stored := root
	if sameRoot {
		stored = observed.Path
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return observed, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE projects SET path=? WHERE "+projectPolicyPredicate, append([]any{stored}, projectPolicyArgs(observed)...)...)
	if err != nil {
		return observed, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return observed, err
	}
	if n != 1 {
		return observed, core.Fail("project_conflict", "project policy or checkout changed during validation; inspect and retry")
	}
	if !sameRoot {
		if _, err := tx.ExecContext(ctx, `INSERT INTO checkout_rebindings(project,path_before,path_after,recorded_at,result) VALUES(?,?,?,?, 'success')`, observed.Name, observed.Path, root, core.Now()); err != nil {
			return observed, err
		}
	}
	if err := tx.Commit(); err != nil {
		return observed, err
	}
	observed.Path = stored
	return observed, nil
}

// CheckoutRebindings returns durable successful transitions in insertion order.
func (s *Store) CheckoutRebindings(project string) ([]CheckoutRebinding, error) {
	rows, err := s.DB.QueryContext(s.context(), `SELECT id,project,path_before,path_after,recorded_at,result FROM checkout_rebindings WHERE project=? ORDER BY id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CheckoutRebinding{}
	for rows.Next() {
		var event CheckoutRebinding
		if err := rows.Scan(&event.ID, &event.Project, &event.PathBefore, &event.PathAfter, &event.RecordedAt, &event.Result); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}
