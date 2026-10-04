package store

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
