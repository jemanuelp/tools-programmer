package store

import (
	"context"

	"tools-programmer/internal/core"
)

// IdentityReceipt contains no raw checkout or Git diagnostics. No-op receipts
// are returned to the caller, not persisted as identity-change history.
type IdentityReceipt struct {
	ID               string `json:"receipt_id"`
	AuditID          int64  `json:"audit_id,omitempty"`
	Project          string `json:"project"`
	Result           string `json:"result"`
	Authorization    string `json:"authorization"`
	RecordedAt       string `json:"recorded_at"`
	Validation       string `json:"validation"`
	IdentityRevision int64  `json:"identity_revision"`
}

const projectPolicyPredicate = `name=? AND repo=? AND path=? AND allow_no_checks=? AND created_at=? AND expected_github=? AND git_name=? AND git_email=? AND origin_url=? AND (SELECT count(*) FROM identity_changes WHERE project=projects.name)=?`

func projectPolicyArgs(p core.Project) []any {
	return []any{p.Name, p.Repo, p.Path, p.AllowNoChecks, p.CreatedAt, p.ExpectedGitHub, p.GitName, p.GitEmail, p.OriginURL, p.IdentityRevision}
}

// UpdateGitIdentity performs a full-policy CAS and append-only audit atomically.
// revalidate must repeat local checkout inspection immediately before the CAS;
// it must never perform network I/O. Existing intents are intentionally untouched.
func (s *Store) UpdateGitIdentity(ctx context.Context, observed core.Project, name, email string, revalidate func() error) (IdentityReceipt, error) {
	r := IdentityReceipt{}
	if err := observed.ValidateIdentity(); err != nil {
		return r, err
	}
	proposed := observed
	proposed.GitName, proposed.GitEmail = name, email
	if err := proposed.ValidateIdentity(); err != nil {
		return r, err
	}
	if revalidate == nil {
		return r, core.Fail("invalid_input", "native checkout revalidation is required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	if err := revalidate(); err != nil {
		return r, err
	}
	args := projectPolicyArgs(observed)
	var matches int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE "+projectPolicyPredicate, args...).Scan(&matches); err != nil {
		return r, err
	}
	if matches != 1 {
		return r, core.Fail("project_conflict", "project policy or checkout mapping changed during validation; inspect and retry")
	}
	r = IdentityReceipt{ID: core.NewID(), Project: observed.Name, Result: "no_op", Authorization: "native_cli_explicit_flag", RecordedAt: core.Now(), Validation: "origin_and_author_committer_match", IdentityRevision: observed.IdentityRevision}
	if observed.GitName != name || observed.GitEmail != email {
		result, err := tx.ExecContext(ctx, "UPDATE projects SET git_name=?,git_email=? WHERE "+projectPolicyPredicate, append([]any{name, email}, args...)...)
		if err != nil {
			return IdentityReceipt{}, err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return IdentityReceipt{}, core.Fail("project_conflict", "identity policy changed; no update committed")
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO identity_changes(project,name_before,email_before,name_after,email_after,authorization,recorded_at,result) VALUES(?,?,?,?,?,?,?,'success')`, observed.Name, observed.GitName, observed.GitEmail, name, email, r.Authorization, r.RecordedAt)
		if err != nil {
			return IdentityReceipt{}, err
		}
		r.AuditID, err = result.LastInsertId()
		if err != nil {
			return IdentityReceipt{}, err
		}
		r.Result, r.IdentityRevision = "changed", observed.IdentityRevision+1
	}
	if err := tx.Commit(); err != nil {
		return IdentityReceipt{}, err
	}
	return r, nil
}
