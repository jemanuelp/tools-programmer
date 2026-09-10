package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
	"tools-programmer/internal/core"
)

type Store struct {
	DB          *sql.DB
	readContext context.Context
}

// WithReadContext borrows the same connection pool for cancellable history queries.
// The returned view must not be closed; the original Store owns the pool.
func (s *Store) WithReadContext(ctx context.Context) *Store {
	return &Store{DB: s.DB, readContext: ctx}
}

func (s *Store) context() context.Context {
	if s.readContext != nil {
		return s.readContext
	}
	return context.Background()
}

func DefaultPath() (string, error) {
	if p := os.Getenv("TOOLS_PROGRAMMER_DB"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base != "" && !filepath.IsAbs(base) {
		return "", core.Fail("invalid_input", "XDG_STATE_HOME must be absolute")
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "tools-programmer", "history.db"), nil
}

func Open(path string) (*Store, error) {
	return open(path, false)
}

// OpenReadOnly requires existing current state and performs no initialization or migration.
func OpenReadOnly(path string) (*Store, error) {
	return open(path, true)
}

func open(path string, readOnly bool) (*Store, error) {
	if path == "" || path == ":memory:" {
		return nil, core.Fail("invalid_input", "database must be a filesystem path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(abs)
	if !readOnly {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, core.Fail("storage_permissions", "database directory must be private (0700); use a dedicated directory")
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		fi, e := os.Lstat(abs + suffix)
		if e == nil && (!fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0) {
			return nil, core.Fail("storage_permissions", "database and sidecars must be regular private files (0600)")
		}
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	if !readOnly {
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			err = f.Close()
		} else if errors.Is(err, os.ErrExist) {
			err = nil
		}
		if err != nil {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	if readOnly {
		q.Set("mode", "ro")
	}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if readOnly {
		var version int
		err = db.QueryRow("PRAGMA user_version").Scan(&version)
		if err == nil && version != 3 {
			err = core.Fail("identity_unbound", "read-only access requires existing schema v3 state; migrate and explicitly bind legacy projects first")
		}
	} else {
		err = s.migrate()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) migrate() error {
	// BEGIN IMMEDIATE serializes first-open migrations across processes.
	if _, err := s.DB.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer s.DB.Exec("ROLLBACK")
	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 3 {
		return core.Fail("newer_schema", "database schema is newer than this binary; upgrade the binary")
	}
	if version == 0 {
		_, err := s.DB.Exec(schema)
		if err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err := s.DB.Exec(`ALTER TABLE projects ADD COLUMN expected_github TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN git_name TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN git_email TEXT NOT NULL DEFAULT '';
PRAGMA user_version=2;`); err != nil {
			return err
		}
	}
	if version < 3 {
		if err := s.migrateIssues(); err != nil {
			return err
		}
	}
	_, err := s.DB.Exec("COMMIT")
	return err
}

const schema = `
CREATE TABLE projects(name TEXT PRIMARY KEY, repo TEXT NOT NULL UNIQUE, path TEXT NOT NULL, allow_no_checks INTEGER NOT NULL CHECK(allow_no_checks IN (0,1)), created_at TEXT NOT NULL);
CREATE TABLE snapshots(project TEXT NOT NULL REFERENCES projects(name), pr INTEGER NOT NULL CHECK(pr>0), payload TEXT NOT NULL, PRIMARY KEY(project,pr));
CREATE TABLE local_reviews(id INTEGER PRIMARY KEY AUTOINCREMENT, project TEXT NOT NULL REFERENCES projects(name), pr INTEGER NOT NULL CHECK(pr>0), sha TEXT NOT NULL, reviewer TEXT NOT NULL, method TEXT NOT NULL, outcome TEXT NOT NULL CHECK(outcome IN ('approved','changes_requested','incomplete')), summary TEXT NOT NULL, evidence TEXT NOT NULL, recorded_at TEXT NOT NULL);
CREATE INDEX review_head ON local_reviews(project,pr,sha,id);
CREATE TABLE plans(id TEXT PRIMARY KEY, project TEXT NOT NULL REFERENCES projects(name), payload TEXT NOT NULL);
CREATE TABLE operations(id TEXT PRIMARY KEY, plan_id TEXT NOT NULL REFERENCES plans(id), project TEXT NOT NULL REFERENCES projects(name), pr INTEGER NOT NULL, sha TEXT NOT NULL, actor TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('intent','unknown','confirmed')), review_id INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(project,pr,sha,actor));
CREATE TABLE confirmations(operation_id TEXT PRIMARY KEY REFERENCES operations(id), payload TEXT NOT NULL);
PRAGMA user_version=1;
`

func (s *Store) AddProject(p core.Project) (core.Project, error) {
	if err := p.ValidateIdentity(); err != nil {
		return p, err
	}
	if !core.ValidName(p.Name) {
		return p, core.Fail("invalid_input", "invalid project name")
	}
	repo, err := core.CanonicalRepo(p.Repo)
	if err != nil {
		return p, err
	}
	p.Repo = repo
	if p.Path != "" {
		p.Path, err = filepath.Abs(p.Path)
		if err != nil {
			return p, err
		}
	}
	p.CreatedAt = core.Now()
	_, err = s.DB.Exec("INSERT INTO projects(name,repo,path,allow_no_checks,created_at,expected_github,git_name,git_email) VALUES(?,?,?,?,?,?,?,?)", p.Name, p.Repo, p.Path, p.AllowNoChecks, p.CreatedAt, p.ExpectedGitHub, p.GitName, p.GitEmail)
	if err != nil {
		return p, core.Fail("project_conflict", "cannot register project; name or repository may already exist")
	}
	return p, nil
}
func (s *Store) Project(name string) (core.Project, error) {
	var p core.Project
	err := s.DB.QueryRowContext(s.context(), "SELECT name,repo,path,allow_no_checks,created_at,expected_github,git_name,git_email FROM projects WHERE name=?", name).Scan(&p.Name, &p.Repo, &p.Path, &p.AllowNoChecks, &p.CreatedAt, &p.ExpectedGitHub, &p.GitName, &p.GitEmail)
	if errors.Is(err, sql.ErrNoRows) {
		err = core.Fail("not_found", "project not registered")
	}
	return p, err
}
func (s *Store) Projects() ([]core.Project, error) {
	rows, err := s.DB.QueryContext(s.context(), "SELECT name,repo,path,allow_no_checks,created_at,expected_github,git_name,git_email FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Project{}
	for rows.Next() {
		var p core.Project
		if err = rows.Scan(&p.Name, &p.Repo, &p.Path, &p.AllowNoChecks, &p.CreatedAt, &p.ExpectedGitHub, &p.GitName, &p.GitEmail); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SaveSnapshot(project string, snap core.Snapshot) error {
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO snapshots VALUES(?,?,?) ON CONFLICT(project,pr) DO UPDATE SET payload=excluded.payload", project, snap.PR, string(b))
	return err
}
func (s *Store) Snapshots(project string) ([]core.Snapshot, error) {
	rows, err := s.DB.QueryContext(s.context(), "SELECT payload FROM snapshots WHERE project=? ORDER BY pr", project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Snapshot{}
	for rows.Next() {
		var b string
		var v core.Snapshot
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) Record(r core.LocalReview) (core.LocalReview, error) {
	if !core.ValidPR(r.PR) || !core.ValidSHA(r.SHA) || strings.TrimSpace(r.Reviewer) == "" || strings.TrimSpace(r.Method) == "" || len(r.Reviewer) > 200 || len(r.Method) > 200 || len(r.Summary) > 8000 || len(r.Evidence) > 8000 {
		return r, core.Fail("invalid_input", "record requires PR, lowercase full 40-character SHA, reviewer and method; text fields must be bounded")
	}
	if r.Outcome != "approved" && r.Outcome != "changes_requested" && r.Outcome != "incomplete" {
		return r, core.Fail("invalid_input", "outcome must be approved, changes_requested, or incomplete")
	}
	r.RecordedAt = core.Now()
	var observation string
	if err := s.DB.QueryRow("SELECT COALESCE((SELECT payload FROM snapshots WHERE project=? AND pr=?),'')", r.Project, r.PR).Scan(&observation); err != nil {
		return r, err
	}
	r, err := reviewWithObservation(r, observation)
	if err != nil {
		return r, err
	}
	res, err := s.DB.Exec("INSERT INTO local_reviews(project,pr,sha,reviewer,method,outcome,summary,evidence,recorded_at) VALUES(?,?,?,?,?,?,?,?,?)", r.Project, r.PR, r.SHA, r.Reviewer, r.Method, r.Outcome, r.Summary, r.Evidence, r.RecordedAt)
	if err != nil {
		return r, err
	}
	r.ID, err = res.LastInsertId()
	return r, err
}
func (s *Store) Approved(project string, pr int, sha string) (bool, error) {
	var outcome string
	err := s.DB.QueryRow("SELECT outcome FROM local_reviews WHERE project=? AND pr=? AND sha=? ORDER BY id DESC LIMIT 1", project, pr, sha).Scan(&outcome)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return outcome == "approved", err
}
func (s *Store) Reviews(project string, pr int) ([]core.LocalReview, error) {
	rows, err := s.DB.QueryContext(s.context(), "SELECT id,r.project,r.pr,sha,reviewer,method,outcome,summary,evidence,recorded_at,COALESCE(s.payload,'') FROM local_reviews r LEFT JOIN snapshots s ON s.project=r.project AND s.pr=r.pr WHERE r.project=? AND (?=0 OR r.pr=?) ORDER BY id", project, pr, pr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.LocalReview{}
	for rows.Next() {
		var r core.LocalReview
		var b string
		err = rows.Scan(&r.ID, &r.Project, &r.PR, &r.SHA, &r.Reviewer, &r.Method, &r.Outcome, &r.Summary, &r.Evidence, &r.RecordedAt, &b)
		if err != nil {
			return nil, err
		}
		r, err = reviewWithObservation(r, b)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func reviewWithObservation(r core.LocalReview, observation string) (core.LocalReview, error) {
	r.Origin = "local-recorded"
	r.Freshness = "unobserved"
	r.LastObservedAt = ""
	if observation == "" {
		return r, nil
	}
	var snap core.Snapshot
	if err := json.Unmarshal([]byte(observation), &snap); err != nil {
		return r, err
	}
	r.LastObservedAt = snap.ObservedAt
	r.Freshness = "stale"
	if snap.Head == r.SHA {
		r.Freshness = "current-at-last-observation"
	}
	return r, nil
}

func (s *Store) SavePlan(p core.Plan) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec("INSERT INTO plans VALUES(?,?,?)", p.ID, p.Project.Name, string(b))
	return err
}
func (s *Store) Plan(id string) (core.Plan, error) {
	var p core.Plan
	var b string
	err := s.DB.QueryRowContext(s.context(), "SELECT payload FROM plans WHERE id=?", id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return p, core.Fail("not_found", "plan not found")
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(b), &p)
	return p, err
}

// Claim commits durable intent before any network write. The unique key spans plans.
func (s *Store) Claim(o core.Operation) error {
	res, err := s.DB.Exec(`INSERT INTO operations(id,plan_id,project,pr,sha,actor,state,created_at,updated_at)
		SELECT ?,?,?,?,?,?,'intent',?,?
		WHERE NOT EXISTS(SELECT 1 FROM operations WHERE state!='confirmed')
		AND EXISTS(SELECT 1 FROM projects WHERE name=? AND expected_github!='' AND lower(expected_github)=lower(?) AND git_name!='' AND git_email!='')
		AND (SELECT outcome FROM local_reviews WHERE project=? AND pr=? AND sha=? ORDER BY id DESC LIMIT 1)='approved'`,
		o.ID, o.PlanID, o.Project, o.PR, o.SHA, strings.ToLower(o.Actor), o.CreatedAt, o.CreatedAt, o.Project, o.Actor, o.Project, o.PR, o.SHA)
	if err != nil {
		return core.Fail("claim_blocked", "operation already claimed or storage unavailable; inspect approval show and reconcile; no retry was sent")
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return core.Fail("claim_blocked", "another intent is unresolved or the local verdict changed; no write was sent")
	}
	return nil
}
func (s *Store) Unresolved() (string, error) {
	var id string
	err := s.DB.QueryRow("SELECT plan_id FROM operations WHERE state!='confirmed' ORDER BY created_at LIMIT 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
func (s *Store) Operations(planID string) ([]core.Operation, error) {
	rows, err := s.DB.QueryContext(s.context(), "SELECT id,plan_id,project,pr,sha,actor,state,review_id,created_at,updated_at FROM operations WHERE plan_id=? ORDER BY pr", planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Operation{}
	for rows.Next() {
		var o core.Operation
		if err = rows.Scan(&o.ID, &o.PlanID, &o.Project, &o.PR, &o.SHA, &o.Actor, &o.State, &o.ReviewID, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) Existing(project string, pr int, sha, actor string) (*core.Operation, error) {
	var o core.Operation
	err := s.DB.QueryRow("SELECT id,plan_id,project,pr,sha,actor,state,review_id,created_at,updated_at FROM operations WHERE project=? AND pr=? AND sha=? AND actor=?", project, pr, sha, strings.ToLower(actor)).Scan(&o.ID, &o.PlanID, &o.Project, &o.PR, &o.SHA, &o.Actor, &o.State, &o.ReviewID, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &o, err
}
func (s *Store) Unknown(id string, reviewID int64) error {
	_, err := s.DB.Exec("UPDATE operations SET state='unknown',review_id=?,updated_at=? WHERE id=? AND state!='confirmed'", reviewID, core.Now(), id)
	return err
}
func (s *Store) Confirm(o core.Operation, r core.RemoteReview) error {
	c := core.Confirmation{OperationID: o.ID, Review: r, ConfirmedAt: core.Now(), Origin: "github-confirmed"}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO confirmations VALUES(?,?) ON CONFLICT(operation_id) DO NOTHING", o.ID, string(b)); err != nil {
		return err
	}
	if _, err = tx.Exec("UPDATE operations SET state='confirmed',review_id=?,updated_at=? WHERE id=?", r.ID, c.ConfirmedAt, o.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Confirmations(planID string) ([]core.Confirmation, error) {
	rows, err := s.DB.QueryContext(s.context(), "SELECT c.payload FROM confirmations c JOIN operations o ON o.id=c.operation_id WHERE o.plan_id=? ORDER BY o.pr", planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.Confirmation{}
	for rows.Next() {
		var b string
		var c core.Confirmation
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &c); err != nil {
			return nil, fmt.Errorf("invalid confirmation: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
