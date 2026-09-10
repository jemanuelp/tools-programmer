package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"

	"tools-programmer/internal/core"
)

const issueSchema = `
CREATE UNIQUE INDEX project_repository_identity ON projects(name,repo);
CREATE TABLE issues(id INTEGER PRIMARY KEY AUTOINCREMENT, project TEXT NOT NULL, repository TEXT NOT NULL, number INTEGER NOT NULL CHECK(number>0), remote_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, UNIQUE(project,number), UNIQUE(id,project,repository), FOREIGN KEY(project,repository) REFERENCES projects(name,repo));
CREATE UNIQUE INDEX issue_remote_identity ON issues(repository,remote_id) WHERE remote_id<>'';
CREATE TABLE issue_remote_observations(issue_id INTEGER PRIMARY KEY, project TEXT NOT NULL, repository TEXT NOT NULL, remote_id TEXT NOT NULL, observed_at TEXT NOT NULL, UNIQUE(repository,remote_id), FOREIGN KEY(issue_id,project,repository) REFERENCES issues(id,project,repository));
CREATE TRIGGER issue_observed_identity BEFORE INSERT ON issues WHEN NEW.remote_id<>'' BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM issue_remote_observations WHERE repository=NEW.repository AND remote_id=NEW.remote_id) THEN RAISE(ABORT,'remote identity already observed') END;
END;
CREATE TRIGGER issue_remote_identity_consistency BEFORE INSERT ON issue_remote_observations BEGIN
 SELECT CASE WHEN EXISTS(SELECT 1 FROM issues WHERE repository=NEW.repository AND remote_id=NEW.remote_id AND id<>NEW.issue_id) OR EXISTS(SELECT 1 FROM issues WHERE id=NEW.issue_id AND remote_id<>'' AND remote_id<>NEW.remote_id) THEN RAISE(ABORT,'remote identity conflict') END;
END;
CREATE TABLE issue_prs(project TEXT NOT NULL, repository TEXT NOT NULL, pr INTEGER NOT NULL CHECK(pr>0), issue_id INTEGER NOT NULL, PRIMARY KEY(project,repository,pr), FOREIGN KEY(issue_id,project,repository) REFERENCES issues(id,project,repository));
CREATE TABLE issue_estimates(id INTEGER PRIMARY KEY AUTOINCREMENT, issue_id INTEGER NOT NULL REFERENCES issues(id), kind TEXT NOT NULL CHECK(kind IN ('initial','revision','final')), score INTEGER NOT NULL CHECK(score IN (1,2,3,5,8,13,21)), proposer TEXT NOT NULL, rationale TEXT NOT NULL, proposed_at TEXT NOT NULL);
CREATE TABLE issue_confirmations(id INTEGER PRIMARY KEY AUTOINCREMENT, proposal_id INTEGER NOT NULL UNIQUE REFERENCES issue_estimates(id), human TEXT NOT NULL, rationale TEXT NOT NULL, confirmed_at TEXT NOT NULL);
CREATE TRIGGER confirmation_sequence BEFORE INSERT ON issue_confirmations BEGIN
 SELECT CASE WHEN NEW.human=(SELECT proposer FROM issue_estimates WHERE id=NEW.proposal_id) THEN RAISE(ABORT,'distinct human required') END;
 SELECT CASE WHEN (SELECT kind FROM issue_estimates WHERE id=NEW.proposal_id)='initial' AND EXISTS(SELECT 1 FROM issue_confirmations c JOIN issue_estimates e ON e.id=c.proposal_id WHERE e.kind='initial' AND e.issue_id=(SELECT issue_id FROM issue_estimates WHERE id=NEW.proposal_id)) THEN RAISE(ABORT,'baseline already confirmed') END;
 SELECT CASE WHEN (SELECT kind FROM issue_estimates WHERE id=NEW.proposal_id)<>'initial' AND NOT EXISTS(SELECT 1 FROM issue_confirmations c JOIN issue_estimates e ON e.id=c.proposal_id WHERE e.kind='initial' AND e.issue_id=(SELECT issue_id FROM issue_estimates WHERE id=NEW.proposal_id)) THEN RAISE(ABORT,'baseline required') END;
END;
CREATE TABLE issue_outcomes(id INTEGER PRIMARY KEY AUTOINCREMENT, issue_id INTEGER NOT NULL REFERENCES issues(id), outcome TEXT NOT NULL CHECK(outcome IN ('completed','cancelled','reopened')), actor TEXT NOT NULL, rationale TEXT NOT NULL, recorded_at TEXT NOT NULL);
CREATE TABLE activity_segments(runtime TEXT NOT NULL, session TEXT NOT NULL, segment TEXT NOT NULL, at INTEGER NOT NULL, issue_id INTEGER REFERENCES issues(id), PRIMARY KEY(runtime,session,segment), UNIQUE(runtime,session,at));
CREATE TABLE activity_events(runtime TEXT NOT NULL, id TEXT NOT NULL, session TEXT NOT NULL, segment TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('bind','start','stop','gap')), activity TEXT NOT NULL, at INTEGER NOT NULL, payload TEXT NOT NULL, PRIMARY KEY(runtime,id), FOREIGN KEY(runtime,session,segment) REFERENCES activity_segments(runtime,session,segment));
CREATE UNIQUE INDEX activity_boundary ON activity_events(runtime,session,segment,activity,kind) WHERE kind IN ('start','stop');
CREATE UNIQUE INDEX segment_binding ON activity_events(runtime,session,segment) WHERE kind='bind';
CREATE INDEX activity_issue ON activity_segments(issue_id);
PRAGMA user_version=3;
`

func (s *Store) migrateIssues() error {
	if _, err := s.DB.Exec(issueSchema); err != nil {
		return err
	}
	for _, table := range []string{"issues", "issue_remote_observations", "issue_prs", "issue_estimates", "issue_confirmations", "issue_outcomes", "activity_segments", "activity_events"} {
		for _, op := range []string{"UPDATE", "DELETE"} {
			if _, err := s.DB.Exec("CREATE TRIGGER immutable_" + table + "_" + op + " BEFORE " + op + " ON " + table + " BEGIN SELECT RAISE(ABORT,'append-only history'); END;"); err != nil {
				return err
			}
		}
	}
	return nil
}

// Serialize local state transitions across processes without holding a network
// request inside a transaction. The callback must use this connection only.
func (s *Store) issueWrite(fn func(*sql.Conn) error) error {
	ctx := s.context()
	c, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err = c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	if err = fn(c); err != nil {
		return err
	}
	_, err = c.ExecContext(ctx, "COMMIT")
	return err
}

func (s *Store) AddIssue(project string, number int, remote string) (core.Issue, error) {
	i := core.Issue{Project: project, Number: number, RemoteID: remote, CreatedAt: core.Now()}
	if remote != "" {
		i.RemoteObservedAt = i.CreatedAt
	}
	if !core.ValidName(project) || !core.ValidPR(number) || (remote != "" && !remoteIdentifier(remote)) {
		return i, core.Fail("invalid_input", "issue requires project, positive number and optional bounded remote identifier")
	}
	p, err := s.Project(project)
	if err != nil {
		return i, err
	}
	i.Repository = p.Repo
	r, err := s.DB.Exec("INSERT INTO issues(project,repository,number,remote_id,created_at) VALUES(?,?,?,?,?)", project, p.Repo, number, remote, i.CreatedAt)
	if err != nil {
		return i, core.Fail("issue_conflict", "issue identity already exists or conflicts")
	}
	i.ID, err = r.LastInsertId()
	return i, err
}

var remoteIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_+/=:-]{0,199}$`)

func remoteIdentifier(v string) bool {
	// GitHub's opaque stable IDs may use padded base64; they are not runtime IDs.
	return remoteIdentifierPattern.MatchString(v)
}

func (s *Store) ObserveIssueRemoteID(issueID int64, remote string) error {
	if issueID <= 0 || !remoteIdentifier(remote) {
		return core.Fail("invalid_input", "issue and observed stable remote ID required")
	}
	return s.issueWrite(func(db *sql.Conn) error {
		ctx := s.context()
		var current, project, repository string
		if err := db.QueryRowContext(ctx, "SELECT COALESCE((SELECT remote_id FROM issue_remote_observations WHERE issue_id=i.id),i.remote_id),project,repository FROM issues i WHERE id=?", issueID).Scan(&current, &project, &repository); err != nil {
			return err
		}
		if current == remote {
			return nil
		}
		if current != "" {
			return core.Fail("issue_conflict", "observed remote identity is immutable")
		}
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM issues WHERE repository=? AND remote_id=? AND id<>?", repository, remote, issueID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return core.Fail("issue_conflict", "remote identity belongs to another issue")
		}
		_, err := db.ExecContext(ctx, "INSERT INTO issue_remote_observations(issue_id,project,repository,remote_id,observed_at) VALUES(?,?,?,?,?)", issueID, project, repository, remote, core.Now())
		return err
	})
}

func (s *Store) LinkIssuePR(issueID int64, pr int) error {
	if issueID <= 0 || !core.ValidPR(pr) {
		return core.Fail("invalid_input", "positive issue ID and PR required")
	}
	r, err := s.DB.Exec("INSERT INTO issue_prs(project,repository,pr,issue_id) SELECT project,repository,?,id FROM issues WHERE id=?", pr, issueID)
	if err != nil {
		return core.Fail("ownership_conflict", "PR already has an issue owner")
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return core.Fail("not_found", "issue not found")
	}
	return nil
}

func (s *Store) ProposeEstimate(issueID int64, kind string, score int, proposer, rationale string) (core.Estimate, error) {
	p := core.Estimate{IssueID: issueID, Kind: kind, Score: score, Proposer: proposer, Rationale: rationale, ProposedAt: core.Now()}
	if issueID <= 0 || !core.ValidScore(score) || (kind != "initial" && kind != "revision" && kind != "final") || !core.ValidText(proposer, 200) || !core.ValidText(rationale, 8000) {
		return p, core.Fail("invalid_input", "proposal requires initial|revision|final, Fibonacci 1–21, proposer and rationale")
	}
	r, err := s.DB.Exec("INSERT INTO issue_estimates(issue_id,kind,score,proposer,rationale,proposed_at) VALUES(?,?,?,?,?,?)", issueID, kind, score, proposer, rationale, p.ProposedAt)
	if err != nil {
		return p, err
	}
	p.ID, err = r.LastInsertId()
	return p, err
}

func (s *Store) ConfirmEstimate(proposalID int64, human, rationale string) (core.EstimateConfirmation, error) {
	c := core.EstimateConfirmation{ProposalID: proposalID, Human: human, Rationale: rationale, ConfirmedAt: core.Now()}
	if proposalID <= 0 || !core.ValidText(human, 200) || !core.ValidText(rationale, 8000) {
		return c, core.Fail("invalid_input", "confirmation requires proposal, explicit human identity and rationale")
	}
	err := s.issueWrite(func(db *sql.Conn) error {
		ctx := s.context()
		var issue int64
		var kind, proposer string
		if err := db.QueryRowContext(ctx, "SELECT issue_id,kind,proposer FROM issue_estimates WHERE id=?", proposalID).Scan(&issue, &kind, &proposer); err != nil {
			return err
		}
		if human == proposer {
			return core.Fail("human_confirmation", "human confirmer must be distinct from agent proposer")
		}
		var baseline int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM issue_confirmations c JOIN issue_estimates e ON e.id=c.proposal_id WHERE e.issue_id=? AND e.kind='initial'", issue).Scan(&baseline); err != nil {
			return err
		}
		if kind == "initial" && baseline != 0 || kind != "initial" && baseline == 0 {
			return core.Fail("estimate_sequence", "confirm one initial baseline before revisions or final assessments; baseline cannot be replaced")
		}
		r, err := db.ExecContext(ctx, "INSERT INTO issue_confirmations(proposal_id,human,rationale,confirmed_at) VALUES(?,?,?,?)", proposalID, human, rationale, c.ConfirmedAt)
		if err != nil {
			return core.Fail("confirmation_conflict", "proposal already confirmed or invalid")
		}
		c.ID, err = r.LastInsertId()
		return err
	})
	return c, err
}

func (s *Store) RecordIssueOutcome(issueID int64, outcome, actor, rationale string) error {
	if issueID <= 0 || !core.ValidText(actor, 200) || !core.ValidText(rationale, 8000) || (outcome != "completed" && outcome != "cancelled" && outcome != "reopened") {
		return core.Fail("invalid_input", "outcome requires completed|cancelled|reopened, actor and rationale")
	}
	return s.issueWrite(func(db *sql.Conn) error {
		ctx := s.context()
		var state string
		err := db.QueryRowContext(ctx, "SELECT outcome FROM issue_outcomes WHERE issue_id=? ORDER BY id DESC LIMIT 1", issueID).Scan(&state)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		closed := state == "completed" || state == "cancelled"
		if outcome == "reopened" && !closed || outcome != "reopened" && closed {
			return core.Fail("outcome_sequence", "close an open issue or reopen a closed issue explicitly")
		}
		_, err = db.ExecContext(ctx, "INSERT INTO issue_outcomes(issue_id,outcome,actor,rationale,recorded_at) VALUES(?,?,?,?,?)", issueID, outcome, actor, rationale, core.Now())
		return err
	})
}

type issueReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readIssueHistory(ctx context.Context, db issueReader, project string, number int) (core.IssueHistory, error) {
	h := core.IssueHistory{PRs: []int{}, Estimates: []core.Estimate{}, Confirmations: []core.EstimateConfirmation{}, Outcomes: []core.IssueOutcome{}, State: "open"}
	i := &h.Issue
	err := db.QueryRowContext(ctx, "SELECT id,project,repository,number,COALESCE((SELECT remote_id FROM issue_remote_observations WHERE issue_id=i.id),i.remote_id),COALESCE((SELECT observed_at FROM issue_remote_observations WHERE issue_id=i.id),CASE WHEN i.remote_id<>'' THEN i.created_at ELSE '' END),created_at FROM issues i WHERE project=? AND number=?", project, number).Scan(&i.ID, &i.Project, &i.Repository, &i.Number, &i.RemoteID, &i.RemoteObservedAt, &i.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return h, core.Fail("not_found", "issue not registered")
	}
	if err != nil {
		return h, err
	}
	rows, err := db.QueryContext(ctx, "SELECT pr FROM issue_prs WHERE issue_id=? ORDER BY pr", i.ID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var pr int
		if err = rows.Scan(&pr); err != nil {
			break
		}
		h.PRs = append(h.PRs, pr)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return h, err
	}
	rows, err = db.QueryContext(ctx, "SELECT id,issue_id,kind,score,proposer,rationale,proposed_at FROM issue_estimates WHERE issue_id=? ORDER BY id", i.ID)
	if err != nil {
		return h, err
	}
	byID := map[int64]core.Estimate{}
	for rows.Next() {
		var p core.Estimate
		err = rows.Scan(&p.ID, &p.IssueID, &p.Kind, &p.Score, &p.Proposer, &p.Rationale, &p.ProposedAt)
		if err != nil {
			break
		}
		h.Estimates = append(h.Estimates, p)
		byID[p.ID] = p
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return h, err
	}
	rows, err = db.QueryContext(ctx, "SELECT c.id,c.proposal_id,c.human,c.rationale,c.confirmed_at FROM issue_confirmations c JOIN issue_estimates e ON e.id=c.proposal_id WHERE e.issue_id=? ORDER BY c.id", i.ID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var c core.EstimateConfirmation
		err = rows.Scan(&c.ID, &c.ProposalID, &c.Human, &c.Rationale, &c.ConfirmedAt)
		if err != nil {
			break
		}
		h.Confirmations = append(h.Confirmations, c)
		p := byID[c.ProposalID]
		if p.Kind == "initial" {
			h.Initial = &p
		}
		if p.Kind == "final" {
			h.Final = &p
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return h, err
	}
	rows, err = db.QueryContext(ctx, "SELECT id,outcome,actor,rationale,recorded_at FROM issue_outcomes WHERE issue_id=? ORDER BY id", i.ID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var o core.IssueOutcome
		err = rows.Scan(&o.ID, &o.Outcome, &o.Actor, &o.Rationale, &o.RecordedAt)
		if err != nil {
			break
		}
		h.Outcomes = append(h.Outcomes, o)
		h.State = o.Outcome
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return h, err
}

func (s *Store) IssueHistory(project string, number int) (core.IssueHistory, error) {
	return readIssueHistory(s.context(), s.DB, project, number)
}

func (s *Store) RecordActivity(e core.ActivityEvent) error {
	if err := e.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(e)
	return s.issueWrite(func(db *sql.Conn) error {
		ctx := s.context()
		var old string
		err := db.QueryRowContext(ctx, "SELECT payload FROM activity_events WHERE runtime=? AND id=?", e.Runtime, e.ID).Scan(&old)
		if err == nil {
			if old == string(b) {
				return nil
			}
			return core.Fail("event_conflict", "event ID reused with different normalized content")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if e.Kind == "bind" {
			var issue any
			if e.ParentSession != "" {
				var inherited sql.NullInt64
				var at int64
				if err = db.QueryRowContext(ctx, "SELECT issue_id,at FROM activity_segments WHERE runtime=? AND session=? AND segment=?", e.Runtime, e.ParentSession, e.ParentSegment).Scan(&inherited, &at); err != nil {
					return core.Fail("missing_parent", "record parent segment before dispatch")
				}
				dispatchAt := e.At
				if e.DispatchAt != nil {
					dispatchAt = *e.DispatchAt
				}
				if dispatchAt < at {
					return core.Fail("invalid_event", "dispatch predates parent segment")
				}
				if inherited.Valid {
					issue = inherited.Int64
				}
				var superseded int
				if err = db.QueryRowContext(ctx, "SELECT count(*) FROM activity_segments WHERE runtime=? AND session=? AND at>? AND at<=?", e.Runtime, e.ParentSession, at, dispatchAt).Scan(&superseded); err != nil {
					return err
				}
				if superseded != 0 {
					return core.Fail("assignment_order", "dispatch must reference the parent assignment effective at dispatch time")
				}
			} else if e.Project != "" {
				var id int64
				if err = db.QueryRowContext(ctx, "SELECT id FROM issues WHERE project=? AND number=?", e.Project, e.Issue).Scan(&id); err != nil {
					return core.Fail("not_found", "assigned issue is not registered")
				}
				issue = id
			} else if e.Resume {
				var previous sql.NullInt64
				err = db.QueryRowContext(ctx, "SELECT issue_id FROM activity_segments WHERE runtime=? AND session=? ORDER BY at DESC LIMIT 1", e.Runtime, e.Session).Scan(&previous)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if previous.Valid {
					issue = previous.Int64
				}
			}
			var latest sql.NullInt64
			if err = db.QueryRowContext(ctx, "SELECT max(at) FROM activity_segments WHERE runtime=? AND session=?", e.Runtime, e.Session).Scan(&latest); err != nil {
				return err
			}
			if latest.Valid && e.At <= latest.Int64 {
				return core.Fail("assignment_order", "assignment switches must append in timestamp order; history is not rewritten")
			}
			var observed sql.NullInt64
			if err = db.QueryRowContext(ctx, "SELECT max(at) FROM activity_events WHERE runtime=? AND session=?", e.Runtime, e.Session).Scan(&observed); err != nil {
				return err
			}
			if observed.Valid && e.At < observed.Int64 {
				return core.Fail("assignment_order", "assignment cannot rewrite already observed work")
			}
			if _, err = db.ExecContext(ctx, "INSERT INTO activity_segments(runtime,session,segment,at,issue_id) VALUES(?,?,?,?,?)", e.Runtime, e.Session, e.Segment, e.At, issue); err != nil {
				return core.Fail("segment_conflict", "execution segment already bound")
			}
		} else {
			var at int64
			if err = db.QueryRowContext(ctx, "SELECT at FROM activity_segments WHERE runtime=? AND session=? AND segment=?", e.Runtime, e.Session, e.Segment).Scan(&at); err != nil {
				return core.Fail("missing_segment", "record authoritative binding before activity")
			}
			if e.At < at {
				return core.Fail("invalid_event", "activity predates segment")
			}
			if e.Kind == "start" || e.Kind == "stop" {
				var other string
				err = db.QueryRowContext(ctx, "SELECT payload FROM activity_events WHERE runtime=? AND session=? AND segment=? AND activity=? AND kind IN ('start','stop') LIMIT 1", e.Runtime, e.Session, e.Segment, e.Activity).Scan(&other)
				if err == nil {
					var p core.ActivityEvent
					if err = json.Unmarshal([]byte(other), &p); err != nil {
						return err
					}
					if p.Category != e.Category || p.Target != e.Target || p.Kind == e.Kind || p.Kind == "start" && p.At > e.At || p.Kind == "stop" && p.At < e.At {
						return core.Fail("event_conflict", "activity boundary conflicts with immutable history")
					}
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		_, err = db.ExecContext(ctx, "INSERT INTO activity_events(runtime,id,session,segment,kind,activity,at,payload) VALUES(?,?,?,?,?,?,?,?)", e.Runtime, e.ID, e.Session, e.Segment, e.Kind, e.Activity, e.At, string(b))
		return err
	})
}

func readIssueActivity(ctx context.Context, db issueReader, issue int64) ([]core.ActivityEvent, []core.ActivitySegment, error) {
	events := []core.ActivityEvent{}
	segments := []core.ActivitySegment{}
	rows, err := db.QueryContext(ctx, `SELECT s.runtime,s.session,s.segment,s.at,s.issue_id,(SELECT min(n.at) FROM activity_segments n WHERE n.runtime=s.runtime AND n.session=s.session AND n.at>s.at) FROM activity_segments s WHERE s.issue_id=? ORDER BY s.runtime,s.session,s.at`, issue)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var s core.ActivitySegment
		err = rows.Scan(&s.Runtime, &s.Session, &s.Segment, &s.At, &s.IssueID, &s.Until)
		if err != nil {
			break
		}
		segments = append(segments, s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = db.QueryContext(ctx, "SELECT e.payload FROM activity_events e JOIN activity_segments s ON s.runtime=e.runtime AND s.session=e.session AND s.segment=e.segment WHERE s.issue_id=? ORDER BY e.at,e.id", issue)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var b string
		var e core.ActivityEvent
		if err = rows.Scan(&b); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(b), &e); err != nil {
			break
		}
		events = append(events, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return events, segments, err
}

func (s *Store) IssueActivity(project string, number int) ([]core.ActivityEvent, []core.ActivitySegment, error) {
	var id int64
	if err := s.DB.QueryRowContext(s.context(), "SELECT id FROM issues WHERE project=? AND number=?", project, number).Scan(&id); err != nil {
		return nil, nil, err
	}
	return readIssueActivity(s.context(), s.DB, id)
}

// IssueSnapshot supplies one consistent SQLite read transaction to both frontends.
func (s *Store) IssueSnapshot(project string, number int) (core.IssueHistory, []core.ActivityEvent, []core.ActivitySegment, error) {
	var h core.IssueHistory
	tx, err := s.DB.BeginTx(s.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return h, nil, nil, err
	}
	defer tx.Rollback()
	h, err = readIssueHistory(s.context(), tx, project, number)
	if err != nil {
		return h, nil, nil, err
	}
	events, segments, err := readIssueActivity(s.context(), tx, h.Issue.ID)
	if err == nil {
		err = tx.Commit()
	}
	return h, events, segments, err
}
