package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"tools-programmer/internal/core"
)

func TestActivityNormalizedCursorContract(t *testing.T) {
	f, _, _, err := normalizeActivityFilters(AppliedActivityFilters{ActivityFilters: ActivityFilters{Runtime: "r", From: "1970-01-01T00:00:01.000Z"}, Session: "s"})
	if err != nil {
		t.Fatal(err)
	}
	_, cursor, err := pageCursor("events-at-id", f, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	cursor.Runtime, cursor.Session, cursor.ID, cursor.At = "r", "s", "event", 1000
	valid := *encodeActivityCursor(cursor)
	for _, tt := range []struct {
		name   string
		modify func(*activityCursor)
	}{
		{"version", func(c *activityCursor) { c.Version = 2 }},
		{"binding", func(c *activityCursor) { c.Binding = "other" }},
		{"runtime", func(c *activityCursor) { c.Runtime = "other" }},
		{"session", func(c *activityCursor) { c.Session = "other" }},
		{"negative time", func(c *activityCursor) { c.At = -1 }},
		{"future time", func(c *activityCursor) { c.At = 4102444800001 }},
		{"empty id", func(c *activityCursor) { c.ID = "" }},
		{"long id", func(c *activityCursor) { c.ID = strings.Repeat("a", 201) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := cursor
			tt.modify(&c)
			if _, _, err := pageCursor("events-at-id", f, nil, *encodeActivityCursor(c)); err == nil {
				t.Fatal("accepted invalid cursor")
			}
		})
	}
	for _, encoded := range []string{"!", strings.Repeat("a", 2049), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"extra":"SECRET"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{} {}`))} {
		if _, _, err := pageCursor("events-at-id", f, nil, encoded); err == nil {
			t.Fatal("accepted malformed cursor")
		}
	}
	if _, _, err := pageCursor("sessions-runtime-session", f, nil, valid); err == nil {
		t.Fatal("cross-tool cursor accepted")
	}
	for _, change := range []func(*AppliedActivityFilters){
		func(f *AppliedActivityFilters) { f.To = "1970-01-01T00:00:03Z" },
		func(f *AppliedActivityFilters) { f.Segment = "other" },
		func(f *AppliedActivityFilters) { f.Kind = "gap" },
		func(f *AppliedActivityFilters) { f.Category = "tool" },
		func(f *AppliedActivityFilters) { f.Reason = "event_loss" },
	} {
		changed := f
		change(&changed)
		if _, _, err := pageCursor("events-at-id", changed, nil, valid); err == nil {
			t.Fatal("changed filter accepted")
		}
	}
	if limit, _, err := pageCursor("events-at-id", f, nil, valid); err != nil || limit != 20 {
		t.Fatal(limit, err)
	}
}

func TestActivityStrictBoundsAndIdentifierPolicy(t *testing.T) {
	for _, bound := range []string{"1969-12-31T23:59:59Z", "2100-01-01T00:00:00.001Z", "1970-01-01T00:00:00,001Z", "1970-01-01T0:00:00Z", "1970-01-01T00:00:00.0000000001Z", "2026-02-30T00:00:00Z"} {
		if _, _, _, err := normalizeActivityFilters(AppliedActivityFilters{ActivityFilters: ActivityFilters{From: bound}}); err == nil {
			t.Fatalf("accepted %s", bound)
		}
	}
	for _, id := range []string{"a", strings.Repeat("a", 200), "runtime/session:part-1.0"} {
		if !core.ValidActivityIdentifier(id) {
			t.Fatal(id)
		}
	}
	for _, id := range []string{"", strings.Repeat("a", 201), "/leading", "space id", "unicode-é", "SECRET\nVALUE"} {
		if core.ValidActivityIdentifier(id) {
			t.Fatal("invalid identifier accepted")
		}
	}
}

func TestActivityReadsUseExistingIndexAndCanceledContext(t *testing.T) {
	s, _ := openTest(t)
	rows, err := s.DB.Query(`EXPLAIN QUERY PLAN SELECT e.id`+activityJoin+`WHERE e.runtime=? AND e.session=? ORDER BY e.at,e.id LIMIT 21`, "runtime", "session")
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	joined := strings.Join(plan, "\n")
	t.Log(joined)
	if !strings.Contains(joined, "SEARCH e USING INDEX") || !strings.Contains(joined, "runtime=?") {
		t.Fatal("runtime lookup lost existing index", joined)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	read := s.WithReadContext(ctx)
	_, err = read.ActivitySummary(ActivityFilters{})
	if err == nil {
		t.Fatal("canceled summary succeeded")
	}
	_, err = read.SessionList(SessionQuery{})
	if err == nil {
		t.Fatal("canceled session list succeeded")
	}
	_, err = read.ActivityDigest(ActivityDigestQuery{})
	if err == nil {
		t.Fatal("canceled activity digest succeeded")
	}
	_, err = read.ActivityList(ActivityQuery{Runtime: "r", Session: "s"})
	if err == nil {
		t.Fatal("canceled event list succeeded")
	}
}

func TestActivityStoredIdentifierFailsClosed(t *testing.T) {
	s, _ := openTest(t)
	e := core.ActivityEvent{ID: "binding", Runtime: "r", Session: "s", Segment: "seg", Kind: "bind", At: 1000}
	if err := s.RecordActivity(e); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"reason": "event_loss", "prompt": "SECRET_PROMPT"})
	if _, err := s.DB.Exec(`INSERT INTO activity_events(runtime,id,session,segment,kind,activity,at,payload) VALUES('r',?,'s','seg','gap','',2000,?)`, strings.Repeat("x", 201), string(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivityList(ActivityQuery{Runtime: "r", Session: "s"}); err == nil {
		t.Fatal("oversized persisted identifier exposed")
	}
}
