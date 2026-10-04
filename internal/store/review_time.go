package store

import (
	"database/sql"
	"errors"

	"tools-programmer/internal/core"
)

const reviewTimeSchema = `
CREATE TABLE IF NOT EXISTS review_time_entries (
	entry_id TEXT PRIMARY KEY NOT NULL CHECK(length(entry_id) BETWEEN 1 AND 200),
	project TEXT NOT NULL REFERENCES projects(name),
	issue_number INTEGER CHECK(issue_number IS NULL OR issue_number BETWEEN 1 AND 2147483647),
	pr INTEGER CHECK(pr IS NULL OR pr BETWEEN 1 AND 2147483647),
	sha TEXT NOT NULL DEFAULT '' CHECK(sha='' OR (length(sha)=40 AND sha NOT GLOB '*[^0-9a-f]*')),
	reviewer TEXT NOT NULL CHECK(length(reviewer) BETWEEN 1 AND 200),
	duration_ms INTEGER NOT NULL CHECK(duration_ms>0),
	source TEXT NOT NULL CHECK(source='manual'),
	recorded_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS review_time_project ON review_time_entries(project, recorded_at, entry_id);
CREATE TRIGGER IF NOT EXISTS review_time_entries_no_update BEFORE UPDATE ON review_time_entries BEGIN SELECT RAISE(ABORT,'append-only history'); END;
CREATE TRIGGER IF NOT EXISTS review_time_entries_no_delete BEFORE DELETE ON review_time_entries BEGIN SELECT RAISE(ABORT,'append-only history'); END;
PRAGMA user_version=8;
`

// migrateReviewTime adds only the independent manual-duration ledger; existing
// registrations, verdicts, activity, approvals, and unresolved intents remain intact.
func (s *Store) migrateReviewTime() error {
	_, err := s.DB.Exec(reviewTimeSchema)
	return err
}

type reviewTimeScanner interface {
	Scan(...any) error
}

func scanReviewTimeEntry(scanner reviewTimeScanner) (core.ReviewTimeEntry, error) {
	var entry core.ReviewTimeEntry
	var issueNumber, pr sql.NullInt64
	err := scanner.Scan(&entry.ID, &entry.Project, &issueNumber, &pr, &entry.SHA, &entry.Reviewer, &entry.DurationMS, &entry.Source, &entry.RecordedAt)
	if issueNumber.Valid {
		value := int(issueNumber.Int64)
		entry.IssueNumber = &value
	}
	if pr.Valid {
		value := int(pr.Int64)
		entry.PR = &value
	}
	return entry, err
}

func sameReviewTimeDeclaration(a, b core.ReviewTimeEntry) bool {
	return a.ID == b.ID && a.Project == b.Project && sameReviewTimeOptionalInt(a.IssueNumber, b.IssueNumber) &&
		sameReviewTimeOptionalInt(a.PR, b.PR) && a.SHA == b.SHA && a.Reviewer == b.Reviewer &&
		a.DurationMS == b.DurationMS && a.Source == b.Source
}

func sameReviewTimeOptionalInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// RecordReviewTime appends a manual declaration or returns the existing row for
// an identical identity and payload. Changed payload reuse is an explicit conflict.
func (s *Store) RecordReviewTime(entry core.ReviewTimeEntry) (core.ReviewTimeEntry, error) {
	entry.RecordedAt = ""
	if err := entry.Validate(); err != nil {
		return entry, err
	}
	err := s.issueWrite(func(db *sql.Conn) error {
		var registered string
		err := db.QueryRowContext(s.context(), "SELECT name FROM projects WHERE name=?", entry.Project).Scan(&registered)
		if errors.Is(err, sql.ErrNoRows) {
			return core.Fail("not_found", "project is not registered")
		}
		if err != nil {
			return err
		}

		existing, err := scanReviewTimeEntry(db.QueryRowContext(s.context(), `
			SELECT entry_id,project,issue_number,pr,sha,reviewer,duration_ms,source,recorded_at
			FROM review_time_entries WHERE entry_id=?`, entry.ID))
		if err == nil {
			if !sameReviewTimeDeclaration(existing, entry) {
				return core.Fail("review_time_conflict", "review-time entry ID already exists with different declared data")
			}
			entry = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		entry.RecordedAt = core.Now()
		_, err = db.ExecContext(s.context(), `
			INSERT INTO review_time_entries(entry_id,project,issue_number,pr,sha,reviewer,duration_ms,source,recorded_at)
			VALUES(?,?,?,?,?,?,?,?,?)`, entry.ID, entry.Project, entry.IssueNumber, entry.PR, entry.SHA, entry.Reviewer, entry.DurationMS, entry.Source, entry.RecordedAt)
		return err
	})
	return entry, err
}

func (s *Store) ReviewTimeEntries(project string) ([]core.ReviewTimeEntry, error) {
	if !core.ValidName(project) {
		return nil, core.Fail("invalid_input", "registered project required")
	}
	if _, err := s.Project(project); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(s.context(), `
		SELECT entry_id,project,issue_number,pr,sha,reviewer,duration_ms,source,recorded_at
		FROM review_time_entries WHERE project=? ORDER BY recorded_at,entry_id`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []core.ReviewTimeEntry{}
	for rows.Next() {
		entry, scanErr := scanReviewTimeEntry(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
