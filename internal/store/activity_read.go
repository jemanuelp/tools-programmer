package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"tools-programmer/internal/core"
)

// ActivityFilters describe matching events, not whole-session history.
type ActivityFilters struct {
	Runtime    string `json:"runtime,omitempty" jsonschema:"Optional bounded runtime identifier"`
	From       string `json:"from,omitempty" jsonschema:"Inclusive UTC RFC3339 bound; rounded up to Unix milliseconds"`
	To         string `json:"to,omitempty" jsonschema:"Exclusive UTC RFC3339 bound; rounded up to Unix milliseconds"`
	Assignment string `json:"assignment,omitempty" jsonschema:"all (default), assigned, or unassigned persisted segment bindings"`
}

type SessionQuery struct {
	ActivityFilters
	Limit  *int   `json:"limit,omitempty" jsonschema:"Page size, default 20, minimum 1, maximum 100"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from the same normalized filters"`
}

// ActivityDigestQuery selects observed activity shape for unassigned segments.
// Assignment is intentionally not an input: digests never expose assigned rows.
type ActivityDigestQuery struct {
	Runtime string `json:"runtime,omitempty" jsonschema:"Optional bounded runtime identifier"`
	From    string `json:"from,omitempty" jsonschema:"Inclusive UTC RFC3339 bound; rounded up to Unix milliseconds"`
	To      string `json:"to,omitempty" jsonschema:"Exclusive UTC RFC3339 bound; rounded up to Unix milliseconds"`
	Limit   *int   `json:"limit,omitempty" jsonschema:"Page size, default 20, minimum 1, maximum 100"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from the same normalized filters"`
}

type ActivityQuery struct {
	Runtime    string `json:"runtime" jsonschema:"Runtime from session_list"`
	Session    string `json:"session" jsonschema:"Native session ID from session_list"`
	Segment    string `json:"segment,omitempty" jsonschema:"Optional bounded segment identifier"`
	From       string `json:"from,omitempty" jsonschema:"Inclusive UTC RFC3339 bound"`
	To         string `json:"to,omitempty" jsonschema:"Exclusive UTC RFC3339 bound"`
	Kind       string `json:"kind,omitempty" jsonschema:"bind, start, stop, or gap"`
	Category   string `json:"category,omitempty" jsonschema:"tool, inference, retry, permission, question, or delegation"`
	Reason     string `json:"reason,omitempty" jsonschema:"observer_restart, event_loss, uncertain_inference, uncorrelated_child, cancelled, reconnected, or unsupported_lifecycle"`
	Assignment string `json:"assignment,omitempty" jsonschema:"all (default), assigned, or unassigned"`
	Limit      *int   `json:"limit,omitempty" jsonschema:"Page size, default 20, minimum 1, maximum 100"`
	Cursor     string `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from the same normalized filters"`
}

type AppliedActivityFilters struct {
	ActivityFilters
	Session  string `json:"session,omitempty"`
	Segment  string `json:"segment,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Category string `json:"category,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ActivitySnapshot struct {
	ReadAt      string                 `json:"read_at"`
	Consistency string                 `json:"consistency"`
	Filters     AppliedActivityFilters `json:"filters"`
}

type RecordedCounts struct {
	Events             int64  `json:"events"`
	Segments           int64  `json:"segments"`
	Gaps               int64  `json:"gaps"`
	AssignedEvents     int64  `json:"assigned_events"`
	UnassignedEvents   int64  `json:"unassigned_events"`
	AssignedSegments   int64  `json:"assigned_segments"`
	UnassignedSegments int64  `json:"unassigned_segments"`
	FirstAt            *int64 `json:"first_at"`
	LastAt             *int64 `json:"last_at"`
}

type ActivitySummary struct {
	RecordedCounts
	Runtimes            int64            `json:"runtimes"`
	Sessions            int64            `json:"sessions"`
	ByKind              map[string]int64 `json:"by_kind"`
	ByReason            map[string]int64 `json:"by_reason"`
	BreakdownsTruncated bool             `json:"breakdowns_truncated"`
}

type ActivitySummaryResult struct {
	Data     ActivitySummary  `json:"data"`
	Snapshot ActivitySnapshot `json:"snapshot"`
}

type RecordedSession struct {
	Runtime string `json:"runtime"`
	Session string `json:"session"`
	RecordedCounts
	Assignment string `json:"assignment"`
}

// ActivityDigest is a bounded shape summary of one session's unassigned
// persisted activity. It deliberately contains no semantic or payload data.
type ActivityDigest struct {
	Runtime     string           `json:"runtime"`
	Session     string           `json:"session"`
	Events      int64            `json:"events"`
	Segments    int64            `json:"segments"`
	Gaps        int64            `json:"gaps"`
	FirstAt     *int64           `json:"first_at"`
	LastAt      *int64           `json:"last_at"`
	ByCategory  map[string]int64 `json:"by_category"`
	ByGapReason map[string]int64 `json:"by_gap_reason"`
	Description string           `json:"description"`
}

type RecordedAssignment struct {
	Project string `json:"project"`
	Number  int    `json:"number"`
}

// RecordedActivity deliberately excludes payload, target, parent and dispatch data.
type RecordedActivity struct {
	ID         string              `json:"id"`
	Runtime    string              `json:"runtime"`
	Session    string              `json:"session"`
	Segment    string              `json:"segment"`
	Kind       string              `json:"kind"`
	At         int64               `json:"at"`
	Activity   string              `json:"activity,omitempty"`
	Category   string              `json:"category,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	Assignment *RecordedAssignment `json:"assignment"`
}

type ActivityPage[T any] struct {
	Data       []T              `json:"data"`
	NextCursor *string          `json:"next_cursor"`
	Snapshot   ActivitySnapshot `json:"snapshot"`
}

func invalidActivityQuery() error {
	return core.Fail("invalid_input", "invalid activity filters, bounds, limit, or cursor")
}

var activityTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$`)

func normalizeActivityFilters(f AppliedActivityFilters) (AppliedActivityFilters, string, []any, error) {
	where, args := "1=1", []any{}
	add := func(column, value string) {
		if value != "" {
			where += " AND " + column + "=?"
			args = append(args, value)
		}
	}
	for _, id := range []string{f.Runtime, f.Session, f.Segment} {
		if id != "" && !core.ValidActivityIdentifier(id) {
			return f, "", nil, invalidActivityQuery()
		}
	}
	if f.Assignment == "" {
		f.Assignment = "all"
	}
	switch f.Assignment {
	case "all":
	case "assigned":
		where += " AND s.issue_id IS NOT NULL"
	case "unassigned":
		where += " AND s.issue_id IS NULL"
	default:
		return f, "", nil, invalidActivityQuery()
	}
	var bounds [2]*int64
	for i, value := range []*string{&f.From, &f.To} {
		if *value == "" {
			continue
		}
		if !activityTime.MatchString(*value) {
			return f, "", nil, invalidActivityQuery()
		}
		t, err := time.Parse(time.RFC3339Nano, *value)
		if err != nil || t.Before(time.UnixMilli(0)) || t.After(time.UnixMilli(4102444800000)) {
			return f, "", nil, invalidActivityQuery()
		}
		ms := t.UnixMilli()
		if t.Nanosecond()%1000000 != 0 {
			ms++
		}
		bounds[i] = &ms
		*value = time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
		if i == 0 {
			where += " AND e.at>=?"
		} else {
			where += " AND e.at<?"
		}
		args = append(args, ms)
	}
	if bounds[0] != nil && bounds[1] != nil && *bounds[0] >= *bounds[1] {
		return f, "", nil, invalidActivityQuery()
	}
	for _, v := range []struct {
		value   string
		allowed string
	}{
		{f.Kind, "bind start stop gap"}, {f.Category, "tool inference retry permission question delegation"},
		{f.Reason, "observer_restart event_loss uncertain_inference uncorrelated_child cancelled reconnected unsupported_lifecycle"},
	} {
		if v.value != "" && !containsWord(v.allowed, v.value) {
			return f, "", nil, invalidActivityQuery()
		}
	}
	add("e.runtime", f.Runtime)
	add("e.session", f.Session)
	add("e.segment", f.Segment)
	add("e.kind", f.Kind)
	add("json_extract(e.payload,'$.category')", f.Category)
	add("json_extract(e.payload,'$.reason')", f.Reason)
	return f, where, args, nil
}

func containsWord(allowed, value string) bool {
	for _, word := range strings.Fields(allowed) {
		if value == word {
			return true
		}
	}
	return false
}

// There is no durable insertion watermark in the activity schema. Each request
// owns a read transaction; cursors select a changing view, never a saved snapshot.
type activityCursor struct {
	Version int    `json:"v"`
	Binding string `json:"binding"`
	Runtime string `json:"runtime"`
	Session string `json:"session"`
	At      int64  `json:"at"`
	ID      string `json:"id"`
}

func pageCursor(scope string, f AppliedActivityFilters, limit *int, encoded string) (int, activityCursor, error) {
	n := 20
	if limit != nil {
		n = *limit
	}
	b, _ := json.Marshal(f)
	c := activityCursor{Version: 1, Binding: fmt.Sprintf("%x", sha256.Sum256(append([]byte(scope+":v1:"), b...)))}
	if n < 1 || n > 100 || len(encoded) > 2048 {
		return 0, c, invalidActivityQuery()
	}
	if encoded == "" {
		return n, c, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return 0, c, invalidActivityQuery()
	}
	var decoded activityCursor
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&decoded) != nil || d.Decode(new(any)) != io.EOF || decoded.Version != 1 || decoded.Binding != c.Binding ||
		!core.ValidActivityIdentifier(decoded.Runtime) || !core.ValidActivityIdentifier(decoded.Session) {
		return 0, c, invalidActivityQuery()
	}
	if scope == "events-at-id" {
		if !core.ValidActivityIdentifier(decoded.ID) || decoded.At < 0 || decoded.At > 4102444800000 || decoded.Runtime != f.Runtime || decoded.Session != f.Session {
			return 0, c, invalidActivityQuery()
		}
	} else if decoded.ID != "" || decoded.At != 0 {
		return 0, c, invalidActivityQuery()
	}
	return n, decoded, nil
}

func encodeActivityCursor(c activityCursor) *string {
	b, _ := json.Marshal(c)
	s := base64.RawURLEncoding.EncodeToString(b)
	return &s
}

const activityJoin = ` FROM activity_events e JOIN activity_segments s ON s.runtime=e.runtime AND s.session=e.session AND s.segment=e.segment `

func snapshot(f AppliedActivityFilters) ActivitySnapshot {
	return ActivitySnapshot{ReadAt: time.Now().UTC().Format(time.RFC3339Nano), Consistency: "request_snapshot_changing_view_per_page", Filters: f}
}

func checkActivityAnchor(ctx context.Context, tx *sql.Tx, where string, args []any, c activityCursor, events bool) error {
	if c.Runtime == "" {
		return nil
	}
	where += " AND e.runtime=? AND e.session=?"
	args = append(append([]any{}, args...), c.Runtime, c.Session)
	if events {
		where += " AND e.at=? AND e.id=?"
		args = append(args, c.At, c.ID)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1"+activityJoin+"WHERE "+where+")", args...).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return invalidActivityQuery()
	}
	return nil
}

// Matching segments are distinct persisted segments with at least one matching event.
const segmentCounts = `SELECT e.runtime,e.session,e.segment,s.issue_id,count(*) AS events,
	sum(e.kind='gap') AS gaps,min(e.at) AS first_at,max(e.at) AS last_at`
const rollupCounts = `coalesce(sum(events),0),count(*),coalesce(sum(gaps),0),
	coalesce(sum(CASE WHEN issue_id IS NOT NULL THEN events ELSE 0 END),0),
	coalesce(sum(CASE WHEN issue_id IS NULL THEN events ELSE 0 END),0),
	coalesce(sum(issue_id IS NOT NULL),0),coalesce(sum(issue_id IS NULL),0),min(first_at),max(last_at)`

func countDest(c *RecordedCounts) []any {
	return []any{&c.Events, &c.Segments, &c.Gaps, &c.AssignedEvents, &c.UnassignedEvents, &c.AssignedSegments, &c.UnassignedSegments, &c.FirstAt, &c.LastAt}
}

func (s *Store) ActivitySummary(in ActivityFilters) (ActivitySummaryResult, error) {
	var out ActivitySummaryResult
	f, where, args, err := normalizeActivityFilters(AppliedActivityFilters{ActivityFilters: in})
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(s.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Snapshot = snapshot(f)
	cte := "WITH segments AS (" + segmentCounts + activityJoin + "WHERE " + where + " GROUP BY e.runtime,e.session,e.segment) "
	dest := append(countDest(&out.Data.RecordedCounts), &out.Data.Runtimes, &out.Data.Sessions)
	err = tx.QueryRowContext(s.context(), cte+"SELECT "+rollupCounts+",count(DISTINCT runtime),(SELECT count(*) FROM (SELECT runtime,session FROM segments GROUP BY runtime,session)) FROM segments", args...).Scan(dest...)
	if err != nil {
		return out, err
	}
	out.Data.ByKind, out.Data.ByReason = map[string]int64{}, map[string]int64{}
	for _, breakdown := range []struct {
		expression string
		allowed    string
		data       map[string]int64
	}{
		{"e.kind", "bind start stop gap", out.Data.ByKind},
		{"CASE WHEN e.kind='gap' THEN json_extract(e.payload,'$.reason') ELSE '' END", "observer_restart event_loss uncertain_inference uncorrelated_child cancelled reconnected unsupported_lifecycle", out.Data.ByReason},
	} {
		// Four kinds or seven reasons, plus the empty non-gap bucket. Any other
		// stored value is corruption, not an unbounded new output label.
		rows, err := tx.QueryContext(s.context(), "SELECT "+breakdown.expression+",count(*)"+activityJoin+"WHERE "+where+" GROUP BY 1 ORDER BY 1 LIMIT 9", args...)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var key sql.NullString
			var count int64
			if err = rows.Scan(&key, &count); err != nil {
				break
			}
			if key.String == "" {
				continue
			}
			if containsWord(breakdown.allowed, key.String) {
				breakdown.data[key.String] = count
			} else {
				err = fmt.Errorf("invalid stored activity")
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

func (s *Store) SessionList(in SessionQuery) (ActivityPage[RecordedSession], error) {
	out := ActivityPage[RecordedSession]{Data: []RecordedSession{}}
	f, where, args, err := normalizeActivityFilters(AppliedActivityFilters{ActivityFilters: in.ActivityFilters})
	if err != nil {
		return out, err
	}
	n, c, err := pageCursor("sessions-runtime-session", f, in.Limit, in.Cursor)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(s.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Snapshot = snapshot(f)
	if err = checkActivityAnchor(s.context(), tx, where, args, c, false); err != nil {
		return out, err
	}
	if in.Cursor != "" {
		where += " AND (e.runtime,e.session)>(?,?)"
		args = append(args, c.Runtime, c.Session)
	}
	query := "WITH segments AS (" + segmentCounts + activityJoin + "WHERE " + where + " GROUP BY e.runtime,e.session,e.segment) SELECT runtime,session," + rollupCounts + " FROM segments GROUP BY runtime,session ORDER BY runtime,session LIMIT ?"
	rows, err := tx.QueryContext(s.context(), query, append(args, n+1)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row RecordedSession
		if err = rows.Scan(append([]any{&row.Runtime, &row.Session}, countDest(&row.RecordedCounts)...)...); err != nil {
			break
		}
		if !core.ValidActivityIdentifier(row.Runtime) || !core.ValidActivityIdentifier(row.Session) {
			err = fmt.Errorf("invalid stored identifier")
			break
		}
		row.Assignment = "mixed"
		if row.AssignedSegments == 0 {
			row.Assignment = "unassigned"
		} else if row.UnassignedSegments == 0 {
			row.Assignment = "assigned"
		}
		out.Data = append(out.Data, row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Data) > n {
		out.Data = out.Data[:n]
		last := out.Data[n-1]
		c.Runtime, c.Session = last.Runtime, last.Session
		out.NextCursor = encodeActivityCursor(c)
	}
	return out, tx.Commit()
}

var digestCategories = [...]string{"tool", "inference", "retry", "permission", "question", "delegation"}
var digestGapReasons = [...]string{"observer_restart", "event_loss", "uncertain_inference", "uncorrelated_child", "cancelled", "reconnected", "unsupported_lifecycle"}

const digestEventProjection = `SELECT e.id,e.runtime,e.session,e.segment,e.kind,e.at,e.activity,
	coalesce(json_extract(e.payload,'$.category'),'') AS category,
	coalesce(json_extract(e.payload,'$.reason'),'') AS reason`

func digestEventsQuery(where string) string {
	return `WITH matching AS (` + digestEventProjection + activityJoin + `WHERE ` + where + `),
	selected_sessions AS (
		SELECT runtime,session FROM matching GROUP BY runtime,session
		ORDER BY runtime,session LIMIT ?
	), selected_events AS (
		SELECT m.* FROM matching m
		JOIN selected_sessions ss ON ss.runtime=m.runtime AND ss.session=m.session
	)
	SELECT id,runtime,session,segment,kind,at,activity,category,reason
	FROM selected_events ORDER BY runtime,session,at,id`
}

func digestSegmentsQuery(where string) string {
	columns := []string{}
	for _, category := range digestCategories {
		columns = append(columns, "sum(category='"+category+"')")
	}
	for _, reason := range digestGapReasons {
		columns = append(columns, "sum(reason='"+reason+"')")
	}
	return `WITH matching AS (` + digestEventProjection + activityJoin + `WHERE ` + where + `),
	selected_sessions AS (
		SELECT runtime,session FROM matching GROUP BY runtime,session
		ORDER BY runtime,session LIMIT ?
	), selected_events AS (
		SELECT m.* FROM matching m
		JOIN selected_sessions ss ON ss.runtime=m.runtime AND ss.session=m.session
	)
	SELECT runtime,session,segment,count(*),sum(kind='gap'),min(at),max(at),` + strings.Join(columns, ",") + `
	FROM selected_events GROUP BY runtime,session,segment
	ORDER BY runtime,session,segment`
}

func validateDigestEvent(id, runtime, session, segment, kind string, at int64, activity, category, reason string) error {
	e := core.ActivityEvent{ID: id, Runtime: runtime, Session: session, Segment: segment, Kind: kind, At: at, Activity: activity, Category: category, Reason: reason}
	if e.Validate() != nil {
		return fmt.Errorf("invalid stored activity")
	}
	return nil
}

func addDigestSegment(d *ActivityDigest, events, gaps, first, last int64, categoryCounts, reasonCounts []int64) {
	d.Events += events
	d.Segments++
	d.Gaps += gaps
	if d.FirstAt == nil || first < *d.FirstAt {
		v := first
		d.FirstAt = &v
	}
	if d.LastAt == nil || last > *d.LastAt {
		v := last
		d.LastAt = &v
	}
	for i, count := range categoryCounts {
		if count != 0 {
			d.ByCategory[digestCategories[i]] += count
		}
	}
	for i, count := range reasonCounts {
		if count != 0 {
			d.ByGapReason[digestGapReasons[i]] += count
		}
	}
}

func digestDescription(d ActivityDigest) string {
	eventWord, segmentWord := "events", "segments"
	if d.Events == 1 {
		eventWord = "event"
	}
	if d.Segments == 1 {
		segmentWord = "segment"
	}
	description := fmt.Sprintf("Observed %d %s across %d %s", d.Events, eventWord, d.Segments, segmentWord)
	if d.FirstAt != nil && d.LastAt != nil {
		description += fmt.Sprintf(" from %d to %d ms", *d.FirstAt, *d.LastAt)
	}
	if len(d.ByCategory) == 0 {
		description += "; categories: none"
	} else {
		parts := []string{}
		for _, category := range digestCategories {
			if count := d.ByCategory[category]; count != 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", category, count))
			}
		}
		description += "; categories: " + strings.Join(parts, ", ")
	}
	if d.Gaps == 0 {
		description += "; gaps: none"
	} else {
		parts := []string{}
		for _, reason := range digestGapReasons {
			if count := d.ByGapReason[reason]; count != 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", reason, count))
			}
		}
		description += fmt.Sprintf("; gaps: %d (%s)", d.Gaps, strings.Join(parts, ", "))
	}
	return description
}

func (s *Store) ActivityDigest(in ActivityDigestQuery) (ActivityPage[ActivityDigest], error) {
	out := ActivityPage[ActivityDigest]{Data: []ActivityDigest{}}
	f, where, args, err := normalizeActivityFilters(AppliedActivityFilters{ActivityFilters: ActivityFilters{Runtime: in.Runtime, From: in.From, To: in.To, Assignment: "unassigned"}})
	if err != nil {
		return out, err
	}
	n, c, err := pageCursor("digests-runtime-session", f, in.Limit, in.Cursor)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(s.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Snapshot = snapshot(f)
	if err = checkActivityAnchor(s.context(), tx, where, args, c, false); err != nil {
		return out, err
	}
	if in.Cursor != "" {
		where += " AND (e.runtime,e.session)>(?,?)"
		args = append(args, c.Runtime, c.Session)
	}
	pageArgs := append(append([]any{}, args...), n+1)

	// Validate only the selected page plus its one look-ahead session. This
	// keeps malformed normalized rows from becoming arbitrary output while not
	// validating unrelated sessions after the page boundary.
	rows, err := tx.QueryContext(s.context(), digestEventsQuery(where), pageArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, runtime, session, segment, kind, activity, category, reason string
		var at int64
		if err = rows.Scan(&id, &runtime, &session, &segment, &kind, &at, &activity, &category, &reason); err != nil {
			break
		}
		if err = validateDigestEvent(id, runtime, session, segment, kind, at, activity, category, reason); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}

	rows, err = tx.QueryContext(s.context(), digestSegmentsQuery(where), pageArgs...)
	if err != nil {
		return out, err
	}
	var current *ActivityDigest
	for rows.Next() {
		var runtime, session, segment string
		var events, gaps, first, last int64
		categoryCounts := make([]int64, len(digestCategories))
		reasonCounts := make([]int64, len(digestGapReasons))
		dest := []any{&runtime, &session, &segment, &events, &gaps, &first, &last}
		for i := range categoryCounts {
			dest = append(dest, &categoryCounts[i])
		}
		for i := range reasonCounts {
			dest = append(dest, &reasonCounts[i])
		}
		if err = rows.Scan(dest...); err != nil {
			break
		}
		if current == nil || current.Runtime != runtime || current.Session != session {
			if current != nil {
				out.Data = append(out.Data, *current)
			}
			if !core.ValidActivityIdentifier(runtime) || !core.ValidActivityIdentifier(session) || !core.ValidActivityIdentifier(segment) {
				err = fmt.Errorf("invalid stored activity")
				break
			}
			current = &ActivityDigest{Runtime: runtime, Session: session, ByCategory: map[string]int64{}, ByGapReason: map[string]int64{}}
		}
		addDigestSegment(current, events, gaps, first, last, categoryCounts, reasonCounts)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if current != nil {
		out.Data = append(out.Data, *current)
	}
	hasMore := len(out.Data) > n
	if hasMore {
		out.Data = out.Data[:n]
	}
	for i := range out.Data {
		out.Data[i].Description = digestDescription(out.Data[i])
	}
	if hasMore {
		last := out.Data[n-1]
		c.Runtime, c.Session = last.Runtime, last.Session
		out.NextCursor = encodeActivityCursor(c)
	}
	return out, tx.Commit()
}

func (s *Store) ActivityList(in ActivityQuery) (ActivityPage[RecordedActivity], error) {
	out := ActivityPage[RecordedActivity]{Data: []RecordedActivity{}}
	if in.Runtime == "" || in.Session == "" {
		return out, invalidActivityQuery()
	}
	f, where, args, err := normalizeActivityFilters(AppliedActivityFilters{ActivityFilters: ActivityFilters{Runtime: in.Runtime, From: in.From, To: in.To, Assignment: in.Assignment}, Session: in.Session, Segment: in.Segment, Kind: in.Kind, Category: in.Category, Reason: in.Reason})
	if err != nil {
		return out, err
	}
	n, c, err := pageCursor("events-at-id", f, in.Limit, in.Cursor)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(s.context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Snapshot = snapshot(f)
	if err = checkActivityAnchor(s.context(), tx, where, args, c, true); err != nil {
		return out, err
	}
	if in.Cursor != "" {
		where += " AND (e.at,e.id)>(?,?)"
		args = append(args, c.At, c.ID)
	}
	query := `SELECT e.id,e.runtime,e.session,e.segment,e.kind,e.at,e.activity,
		coalesce(json_extract(e.payload,'$.category'),''),coalesce(json_extract(e.payload,'$.reason'),''),i.project,i.number` + activityJoin + `LEFT JOIN issues i ON i.id=s.issue_id WHERE ` + where + ` ORDER BY e.at,e.id LIMIT ?`
	rows, err := tx.QueryContext(s.context(), query, append(args, n+1)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var row RecordedActivity
		var project sql.NullString
		var number sql.NullInt64
		if err = rows.Scan(&row.ID, &row.Runtime, &row.Session, &row.Segment, &row.Kind, &row.At, &row.Activity, &row.Category, &row.Reason, &project, &number); err != nil {
			break
		}
		// Validate only the allowlisted projection, never decode the payload DTO.
		e := core.ActivityEvent{ID: row.ID, Runtime: row.Runtime, Session: row.Session, Segment: row.Segment, Kind: row.Kind, At: row.At, Activity: row.Activity, Category: row.Category, Reason: row.Reason}
		if e.Validate() != nil || project.Valid != number.Valid || project.Valid && (!core.ValidName(project.String) || !core.ValidPR(int(number.Int64))) {
			err = fmt.Errorf("invalid stored activity")
			break
		}
		if project.Valid {
			row.Assignment = &RecordedAssignment{Project: project.String, Number: int(number.Int64)}
		}
		out.Data = append(out.Data, row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Data) > n {
		out.Data = out.Data[:n]
		last := out.Data[n-1]
		c.Runtime, c.Session, c.At, c.ID = last.Runtime, last.Session, last.At, last.ID
		out.NextCursor = encodeActivityCursor(c)
	}
	return out, tx.Commit()
}
