package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"tools-programmer/internal/core"
)

// ActivityContext is a bounded current-state view, not session history or authority.
type ActivityContext struct {
	Version           int           `json:"version"`
	ObservedAt        string        `json:"observed_at"`
	Runtime           string        `json:"runtime"`
	Session           string        `json:"session"`
	ProjectResolution string        `json:"project_resolution"`
	Project           string        `json:"project,omitempty"`
	Repository        string        `json:"repository,omitempty"`
	Assignment        string        `json:"assignment"`
	Segment           string        `json:"segment,omitempty"`
	At                int64         `json:"at,omitempty"`
	ParentSession     string        `json:"parent_session,omitempty"`
	Issue             *ContextIssue `json:"issue,omitempty"`
	Warnings          []string      `json:"warnings"`
}

type ContextIssue struct {
	ID      int64  `json:"id"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Outcome string `json:"outcome"`
}

// CurrentActivity resolves only approved, identity-bound checkout mappings. It
// neither probes Git origins nor interprets an absent session as unassigned.
func (s *Store) CurrentActivity(runtime, session, directory string) (ActivityContext, error) {
	r := ActivityContext{Version: 1, ObservedAt: core.Now(), Runtime: runtime, Session: session,
		ProjectResolution: "unavailable", Assignment: "unknown", Warnings: []string{}}
	if !core.ValidActivityIdentifier(runtime) || !core.ValidActivityIdentifier(session) || !filepath.IsAbs(directory) {
		return r, core.Fail("invalid_input", "runtime, native session and absolute checkout directory required")
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		r.Warnings = append(r.Warnings, "checkout_unavailable")
		return r, nil
	}
	tx, err := s.DB.BeginTx(s.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(s.context(), `SELECT name,repo,path FROM projects
		WHERE path<>'' AND expected_github<>'' AND git_name<>'' AND git_email<>''`)
	if err != nil {
		return r, err
	}
	matches := 0
	for rows.Next() {
		var name, repo, path string
		if err = rows.Scan(&name, &repo, &path); err != nil {
			break
		}
		root, e := filepath.EvalSymlinks(path)
		if e != nil {
			continue
		}
		rel, e := filepath.Rel(root, directory)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			matches++
			r.Project, r.Repository = name, repo
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return r, err
	}
	if rowErr != nil {
		return r, rowErr
	}
	if matches != 1 {
		r.Project, r.Repository = "", ""
		if matches > 1 {
			r.ProjectResolution = "ambiguous"
		}
		r.Warnings = append(r.Warnings, "project_"+r.ProjectResolution)
		return r, tx.Commit()
	}
	r.ProjectResolution = "matched"
	var id sql.NullInt64
	var project string
	issue := new(ContextIssue)
	err = tx.QueryRowContext(s.context(), `SELECT s.segment,s.at,s.issue_id,
		COALESCE(i.project,''),COALESCE(i.number,0),COALESCE(i.url,''),
		COALESCE((SELECT outcome FROM issue_outcomes WHERE issue_id=i.id ORDER BY id DESC LIMIT 1),'open'),
		COALESCE((SELECT json_extract(payload,'$.parent_session') FROM activity_events
		 WHERE runtime=s.runtime AND session=s.session AND segment=s.segment AND kind='bind'),'')
		FROM activity_segments s LEFT JOIN issues i ON i.id=s.issue_id
		WHERE s.runtime=? AND s.session=? ORDER BY s.at DESC LIMIT 1`, runtime, session).
		Scan(&r.Segment, &r.At, &id, &project, &issue.Number, &issue.URL, &issue.Outcome, &r.ParentSession)
	if errors.Is(err, sql.ErrNoRows) {
		r.Warnings = append(r.Warnings, "session_unobserved")
	} else if err != nil {
		return r, err
	} else if !id.Valid {
		r.Assignment = "unassigned"
	} else if project != r.Project {
		r.Warnings = append(r.Warnings, "assignment_project_mismatch")
	} else {
		r.Assignment = "assigned"
		issue.ID = id.Int64
		r.Issue = issue
	}
	return r, tx.Commit()
}
