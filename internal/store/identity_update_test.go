package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"tools-programmer/internal/core"
)

func identityChange(t *testing.T, s *Store, p core.Project, name, email string) IdentityReceipt {
	t.Helper()
	r, err := s.UpdateGitIdentity(context.Background(), p, name, email, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func changeCount(t *testing.T, s *Store, project string) int {
	t.Helper()
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM identity_changes WHERE project=?", project).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIdentityChangePreservesRegistrationAndAllHistory(t *testing.T) {
	s, _ := openTest(t)
	register(t, s, "one")
	p, _ := s.Project("one")
	review := record(t, s, "one", strings.Repeat("a", 40), "approved")
	if _, err := s.AddIssue("one", 36, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordActivity(core.ActivityEvent{ID: "binding", Runtime: "runtime", Session: "session", Segment: "segment", Kind: "bind", At: 1000, Project: "one", Issue: 36}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot("one", core.Snapshot{PR: 7, Head: review.SHA, ObservedAt: core.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(core.Plan{ID: "old", Project: p}); err != nil {
		t.Fatal(err)
	}
	confirmed := core.Operation{ID: "confirmed", PlanID: "old", Project: "one", PR: 7, SHA: review.SHA, Actor: "reviewer", CreatedAt: core.Now()}
	if err := s.Claim(confirmed); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(confirmed, core.RemoteReview{ID: 1, Actor: "reviewer", State: "APPROVED", SHA: review.SHA, SubmittedAt: core.Now()}); err != nil {
		t.Fatal(err)
	}
	record(t, s, "one", strings.Repeat("b", 40), "approved")
	unknown := confirmed
	unknown.ID, unknown.SHA = "unknown", strings.Repeat("b", 40)
	if err := s.Claim(unknown); err != nil {
		t.Fatal(err)
	}
	if err := s.Unknown(unknown.ID, 0); err != nil {
		t.Fatal(err)
	}
	// Snapshot every preexisting history table, not just counts.
	history := func() map[string]string {
		result := map[string]string{}
		rows, err := s.DB.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT IN ('projects','identity_changes','sqlite_sequence') ORDER BY name")
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, name := range names {
			rows, err := s.DB.Query("SELECT * FROM " + name + " ORDER BY rowid")
			if err != nil {
				t.Fatal(err)
			}
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			var values [][]any
			for rows.Next() {
				row := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range row {
					dest[i] = &row[i]
				}
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				values = append(values, row)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			b, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			result[name] = string(b)
		}
		return result
	}
	before := history()
	r := identityChange(t, s, p, "New User", "new@example.com")
	if r.Result != "changed" || r.AuditID <= 0 || r.IdentityRevision != 1 || r.Authorization != "native_cli_explicit_flag" || r.Validation != "origin_and_author_committer_match" {
		t.Fatal(r)
	}
	if _, err := time.Parse(time.RFC3339Nano, r.RecordedAt); err != nil {
		t.Fatal(err)
	}
	got, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	want := p
	want.GitName, want.GitEmail, want.IdentityRevision = "New User", "new@example.com", 1
	if got != want || !reflect.DeepEqual(before, history()) {
		t.Fatal("registration or history changed", got)
	}
	var nameBefore, emailBefore, nameAfter, emailAfter, authorization, recordedAt, result string
	if err := s.DB.QueryRow("SELECT name_before,email_before,name_after,email_after,authorization,recorded_at,result FROM identity_changes WHERE id=?", r.AuditID).Scan(&nameBefore, &emailBefore, &nameAfter, &emailAfter, &authorization, &recordedAt, &result); err != nil {
		t.Fatal(err)
	}
	if nameBefore != p.GitName || emailBefore != p.GitEmail || nameAfter != got.GitName || emailAfter != got.GitEmail || authorization != r.Authorization || recordedAt != r.RecordedAt || result != "success" {
		t.Fatal("audit mismatch")
	}
	for _, query := range []string{"UPDATE identity_changes SET result='success'", "DELETE FROM identity_changes"} {
		if _, err := s.DB.Exec(query); err == nil {
			t.Fatal("mutable audit", query)
		}
	}
	for range 2 {
		noop := identityChange(t, s, got, got.GitName, got.GitEmail)
		if noop.Result != "no_op" || noop.AuditID != 0 || noop.ID == "" || noop.IdentityRevision != 1 {
			t.Fatal(noop)
		}
	}
	if changeCount(t, s, "one") != 1 || !reflect.DeepEqual(before, history()) {
		t.Fatal("no-op rewrote history")
	}
	if current, _ := s.Project("one"); current != got {
		t.Fatal("no-op rewrote policy")
	}
	duplicate := got
	duplicate.Name = "other"
	duplicate.OriginURL = ""
	if _, err := s.AddProject(duplicate); err == nil {
		t.Fatal("duplicate origin accepted")
	}
	projects, err := s.Projects()
	if err != nil || len(projects) != 1 {
		t.Fatal(projects, err)
	}
}

func TestIdentityUpdateFullPolicyCASAndRollback(t *testing.T) {
	for _, column := range []string{"repo", "path", "allow_no_checks", "created_at", "expected_github", "git_name", "git_email", "origin_url", "revision"} {
		t.Run(column, func(t *testing.T) {
			s, _ := openTest(t)
			register(t, s, "one")
			p, _ := s.Project("one")
			if column == "revision" {
				identityChange(t, s, p, "Other", "other@example.com")
				current, _ := s.Project("one")
				identityChange(t, s, current, p.GitName, p.GitEmail)
			} else {
				var value any = "changed"
				if column == "allow_no_checks" {
					value = 1
				}
				if _, err := s.DB.Exec("UPDATE projects SET "+column+"=? WHERE name='one'", value); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.Project("one")
			count := changeCount(t, s, "one")
			if _, err := s.UpdateGitIdentity(context.Background(), p, "New", "new@example.com", func() error { return nil }); err == nil {
				t.Fatal("stale policy accepted")
			}
			after, _ := s.Project("one")
			if after != before || changeCount(t, s, "one") != count {
				t.Fatal("stale request mutated state")
			}
		})
	}
	for _, scenario := range []string{"audit_failure", "revalidation_failure", "no_revalidation", "invalid_tuple"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			register(t, s, "one")
			p, _ := s.Project("one")
			revalidate := func() error { return nil }
			name, email := "New", "new@example.com"
			switch scenario {
			case "audit_failure":
				if _, err := s.DB.Exec("CREATE TRIGGER fail_identity_audit BEFORE INSERT ON identity_changes BEGIN SELECT RAISE(ABORT,'fixture failure'); END;"); err != nil {
					t.Fatal(err)
				}
			case "revalidation_failure":
				revalidate = func() error { return errors.New("checkout changed") }
			case "no_revalidation":
				revalidate = nil
			case "invalid_tuple":
				email = "invalid"
			}
			if _, err := s.UpdateGitIdentity(context.Background(), p, name, email, revalidate); err == nil {
				t.Fatal("invalid update succeeded")
			}
			after, _ := s.Project("one")
			if after != p || changeCount(t, s, "one") != 0 {
				t.Fatal("failed atomic update left mutation")
			}
		})
	}
}

func TestV6IdentityMigrationKeepsLegacyPlanValidUntilChange(t *testing.T) {
	s, path := openTest(t)
	register(t, s, "one")
	p, _ := s.Project("one")
	// Omitted revision in a pre-v7 plan decodes to zero; no plan rewrite.
	if _, err := s.DB.Exec(`INSERT INTO plans VALUES('legacy','one',?); DROP TABLE identity_changes; PRAGMA user_version=6;`, `{"id":"legacy","project":{"name":"one","repository":"`+p.Repo+`","origin_url":"`+p.OriginURL+`","created_at":"`+p.CreatedAt+`","expected_github_login":"reviewer","expected_git_name":"Review User","expected_git_email":"review@example.com"}}`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if r, err := OpenReadOnly(path); err == nil {
		r.Close()
		t.Fatal("read-only access migrated v6")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	plan, err := s.Plan("legacy")
	if err != nil || plan.Project != p {
		t.Fatal(plan, err)
	}
	record(t, s, "one", strings.Repeat("a", 40), "approved")
	op := core.Operation{ID: "legacy-op", PlanID: "legacy", Project: "one", PR: 7, SHA: strings.Repeat("a", 40), Actor: "reviewer", CreatedAt: core.Now()}
	identityChange(t, s, p, p.GitName, p.GitEmail)
	if err := s.Claim(op); err != nil {
		t.Fatal("unchanged legacy bound plan blocked", err)
	}
	identityChange(t, s, p, "New", "new@example.com")
	if got, err := s.Plan("legacy"); err != nil || !reflect.DeepEqual(got, plan) {
		t.Fatal("legacy plan rewritten", got, err)
	}
	if id, err := s.Unresolved(); err != nil || id != "legacy" {
		t.Fatal("legacy intent lost", id, err)
	}
	if err := s.Confirm(op, core.RemoteReview{ID: 1, Actor: "reviewer", State: "APPROVED", SHA: op.SHA, SubmittedAt: core.Now()}); err != nil {
		t.Fatal(err)
	}
	current, err := s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	identityChange(t, s, current, p.GitName, p.GitEmail)
	// Returning to the legacy tuple still cannot revive its omitted/zero revision.
	op.ID, op.SHA = "stale-legacy", strings.Repeat("c", 40)
	record(t, s, "one", op.SHA, "approved")
	if err := s.Claim(op); err == nil {
		t.Fatal("legacy zero-revision plan revived after A -> B -> A")
	}
	current, err = s.Project("one")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(core.Plan{ID: "fresh", Project: current}); err != nil {
		t.Fatal(err)
	}
	op.ID, op.PlanID = "fresh-intent", "fresh"
	if err := s.Claim(op); err != nil {
		t.Fatal("new revision plan blocked", err)
	}
}
