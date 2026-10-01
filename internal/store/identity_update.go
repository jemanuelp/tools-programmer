package store

import (
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
