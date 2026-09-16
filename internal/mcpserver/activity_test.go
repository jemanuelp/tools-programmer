package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func activityFixture(t *testing.T) (*store.Store, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "private", "activity.db")
	w, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r, err := store.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return w, r, path
}

func recordEvent(t *testing.T, w *store.Store, runtime, session, segment, id, kind string, at int64) {
	t.Helper()
	e := core.ActivityEvent{Runtime: runtime, Session: session, Segment: segment, ID: id, Kind: kind, At: at}
	if kind == "gap" {
		e.Reason = "event_loss"
	}
	if kind == "start" || kind == "stop" {
		e.Activity = "work"
		e.Category = "tool"
	}
	if err := w.RecordActivity(e); err != nil {
		t.Fatal(err)
	}
}

func activityCall[T any](t *testing.T, client *mcp.ClientSession, ctx context.Context, name string, args any) T {
	t.Helper()
	r, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil || r.IsError {
		t.Fatalf("%s: %+v %v", name, r, err)
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET_") {
		t.Fatalf("private field leaked: %s", b)
	}
	return out
}

// Compare the public JSON contract, not descriptions, property order, or SDK Go types.
type activitySchema struct {
	Type                 json.RawMessage            `json:"type"`
	Properties           map[string]*activitySchema `json:"properties"`
	Required             []string                   `json:"required"`
	Items                *activitySchema            `json:"items"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
}

func assertActivitySchemaType(t *testing.T, schema *activitySchema, want string) {
	t.Helper()
	if schema == nil {
		t.Fatal("missing schema")
	}
	var types []string
	if err := json.Unmarshal(schema.Type, &types); err != nil {
		var single string
		if err := json.Unmarshal(schema.Type, &single); err != nil {
			t.Fatalf("invalid schema type %s: %v", schema.Type, err)
		}
		types = []string{single}
	}
	sort.Strings(types)
	if got := strings.Join(types, "|"); got != want {
		t.Fatalf("schema types: got %q, want %q", got, want)
	}
}

func assertActivitySchemaObject(t *testing.T, schema *activitySchema, objectType, required string, properties map[string]string) map[string]*activitySchema {
	t.Helper()
	assertActivitySchemaType(t, schema, objectType)
	if string(schema.AdditionalProperties) != "false" {
		t.Fatalf("public DTO must reject undeclared properties: %s", schema.AdditionalProperties)
	}
	gotRequired, wantRequired := append([]string{}, schema.Required...), strings.Fields(required)
	sort.Strings(gotRequired)
	sort.Strings(wantRequired)
	if strings.Join(gotRequired, " ") != strings.Join(wantRequired, " ") {
		t.Fatalf("required fields: got %v, want %v", gotRequired, wantRequired)
	}
	if len(schema.Properties) != len(properties) {
		t.Fatalf("properties: got %v, want %v", schema.Properties, properties)
	}
	for name, typ := range properties {
		t.Run(name, func(t *testing.T) { assertActivitySchemaType(t, schema.Properties[name], typ) })
	}
	return schema.Properties
}

func TestActivityToolSchemas(t *testing.T) {
	_, r, _ := activityFixture(t)
	client, ctx := connect(t, r)
	listed, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		tools[tool.Name] = tool
	}
	for _, name := range []string{"activity_summary", "session_list", "activity_digest", "activity_list"} {
		t.Run(name, func(t *testing.T) {
			tool := tools[name]
			if tool == nil {
				t.Fatal("tool not discovered")
			}
			decode := func(value any) *activitySchema {
				t.Helper()
				b, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var schema *activitySchema
				if err := json.Unmarshal(b, &schema); err != nil || schema == nil {
					t.Fatalf("missing or invalid schema: %s %v", b, err)
				}
				return schema
			}
			input := map[string]string{"runtime": "string", "from": "string", "to": "string"}
			if name != "activity_digest" {
				input["assignment"] = "string"
			}
			required := ""
			if name != "activity_summary" {
				input["limit"], input["cursor"] = "integer|null", "string"
			}
			if name == "activity_list" {
				required = "runtime session"
				for _, field := range []string{"session", "segment", "kind", "category", "reason"} {
					input[field] = "string"
				}
			}
			assertActivitySchemaObject(t, decode(tool.InputSchema), "object", required, input)
			output := map[string]string{"data": "object", "snapshot": "object"}
			required = "data snapshot"
			if name != "activity_summary" {
				output["data"], output["next_cursor"] = "array|null", "null|string"
				required += " next_cursor"
			}
			props := assertActivitySchemaObject(t, decode(tool.OutputSchema), "object", required, output)
			snapshot := assertActivitySchemaObject(t, props["snapshot"], "object", "read_at consistency filters", map[string]string{"read_at": "string", "consistency": "string", "filters": "object"})
			assertActivitySchemaObject(t, snapshot["filters"], "object", "", map[string]string{
				"runtime": "string", "session": "string", "segment": "string", "assignment": "string",
				"from": "string", "to": "string", "kind": "string", "category": "string", "reason": "string",
			})
			if name == "activity_list" {
				row := assertActivitySchemaObject(t, props["data"].Items, "object", "id runtime session segment kind at assignment", map[string]string{
					"id": "string", "runtime": "string", "session": "string", "segment": "string", "kind": "string",
					"at": "integer", "activity": "string", "category": "string", "reason": "string", "assignment": "null|object",
				})
				assertActivitySchemaObject(t, row["assignment"], "null|object", "project number", map[string]string{"project": "string", "number": "integer"})
				return
			}
			if name == "activity_digest" {
				row := assertActivitySchemaObject(t, props["data"].Items, "object", "runtime session events segments gaps first_at last_at by_category by_gap_reason description", map[string]string{
					"runtime": "string", "session": "string", "events": "integer", "segments": "integer", "gaps": "integer",
					"first_at": "integer|null", "last_at": "integer|null", "by_category": "object", "by_gap_reason": "object", "description": "string",
				})
				for _, field := range []string{"by_category", "by_gap_reason"} {
					var values activitySchema
					if err := json.Unmarshal(row[field].AdditionalProperties, &values); err != nil {
						t.Fatal(err)
					}
					assertActivitySchemaType(t, &values, "integer")
				}
				return
			}
			counts := map[string]string{
				"events": "integer", "segments": "integer", "gaps": "integer", "assigned_events": "integer",
				"unassigned_events": "integer", "assigned_segments": "integer", "unassigned_segments": "integer",
				"first_at": "integer|null", "last_at": "integer|null",
			}
			required = "events segments gaps assigned_events unassigned_events assigned_segments unassigned_segments first_at last_at"
			data := props["data"]
			if name == "session_list" {
				data = data.Items
				counts["runtime"], counts["session"], counts["assignment"] = "string", "string", "string"
				required += " runtime session assignment"
			} else {
				counts["runtimes"], counts["sessions"] = "integer", "integer"
				counts["by_kind"], counts["by_reason"], counts["breakdowns_truncated"] = "object", "object", "boolean"
				required += " runtimes sessions by_kind by_reason breakdowns_truncated"
			}
			fields := assertActivitySchemaObject(t, data, "object", required, counts)
			if name == "activity_summary" {
				for _, field := range []string{"by_kind", "by_reason"} {
					var values activitySchema
					if err := json.Unmarshal(fields[field].AdditionalProperties, &values); err != nil {
						t.Fatal(err)
					}
					assertActivitySchemaType(t, &values, "integer")
				}
			}
		})
	}
	for _, missing := range []string{"runtime", "session"} {
		t.Run("missing_"+missing, func(t *testing.T) {
			args := map[string]any{"runtime": "r", "session": "s"}
			delete(args, missing)
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "activity_list", Arguments: args})
			b, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if err != nil || result == nil || !result.IsError || !strings.Contains(string(b), missing) || !strings.Contains(string(b), "required") {
				t.Fatalf("missing %s must fail SDK schema validation: %s %v", missing, b, err)
			}
		})
	}
}

func TestActivityDiscoveryUnassignedAndReadOnly(t *testing.T) {
	w, r, path := activityFixture(t)
	for i := 0; i < 23; i++ {
		session := fmt.Sprintf("session-%02d", i)
		recordEvent(t, w, "runtime", session, "segment", session, "bind", 1000)
	}
	recordEvent(t, w, "other", "session-00", "segment", "session-00", "bind", 1000)
	// Excluded payload fields can contain arbitrary strings and must never be decoded into output.
	if _, err := w.DB.Exec(`INSERT INTO activity_events(runtime,id,session,segment,kind,activity,at,payload) VALUES('runtime','gap','session-00','segment','gap','',2000,'{"reason":"event_loss","prompt":"SECRET_PROMPT","target":"SECRET_TARGET","environment":"SECRET_ENV","result":"SECRET_RESULT","project":"SECRET_PROJECT","issue":999}')`); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	client, ctx := connect(t, r)
	tools, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 10 {
		t.Fatal(len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if strings.HasPrefix(tool.Name, "activity_") || tool.Name == "session_list" {
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
				t.Fatalf("missing contract: %+v", tool)
			}
		}
	}
	summary := activityCall[store.ActivitySummaryResult](t, client, ctx, "activity_summary", map[string]any{})
	if summary.Data.Events != 25 || summary.Data.Segments != 24 || summary.Data.Sessions != 24 || summary.Data.Runtimes != 2 || summary.Data.UnassignedEvents != 25 || summary.Data.AssignedSegments != 0 || summary.Data.ByReason["event_loss"] != 1 {
		t.Fatalf("%+v", summary)
	}
	var issues int
	if err = w.DB.QueryRow("SELECT count(*) FROM issues").Scan(&issues); err != nil || issues != 0 {
		t.Fatal(issues, err)
	}
	page := activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", map[string]any{})
	if len(page.Data) != 20 || page.NextCursor == nil || page.Data[0].Runtime != "other" {
		t.Fatalf("%+v", page)
	}
	last := activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", map[string]any{"cursor": *page.NextCursor})
	if len(last.Data) != 4 || last.NextCursor != nil {
		t.Fatalf("%+v", last)
	}
	seen := map[string]bool{}
	for _, row := range append(page.Data, last.Data...) {
		key := row.Runtime + "/" + row.Session
		if seen[key] || row.Assignment != "unassigned" {
			t.Fatal(row)
		}
		seen[key] = true
	}
	for _, runtime := range []string{"runtime", "other"} {
		events := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", map[string]any{"runtime": runtime, "session": "session-00"})
		if events.Data[0].ID != "session-00" || events.Data[0].Assignment != nil {
			t.Fatal(events)
		}
	}
	unknown := activityCall[store.ActivitySummaryResult](t, client, ctx, "activity_summary", map[string]any{"runtime": "unknown"})
	if unknown.Data.Events != 0 || unknown.Data.FirstAt != nil {
		t.Fatal(unknown)
	}
	maximum := activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", map[string]any{"limit": 100})
	if len(maximum.Data) != 24 || maximum.NextCursor != nil {
		t.Fatal(maximum)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read changed database", err)
	}
	if _, err = r.DB.Exec("DELETE FROM activity_events"); err == nil {
		t.Fatal("read store accepted mutation")
	}
}

func TestActivityPersistedSwitchesAndFilteredScope(t *testing.T) {
	w, r, _ := activityFixture(t)
	recordEvent(t, w, "r", "session", "unassigned", "bind-u", "bind", 1000)
	for i, project := range []string{"first", "second"} {
		if _, err := w.AddProject(core.Project{Name: project, Repo: "example/" + project, ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "reviewer@example.com"}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.AddIssue(project, i+1, ""); err != nil {
			t.Fatal(err)
		}
		e := core.ActivityEvent{ID: "bind-" + project, Runtime: "r", Session: "session", Segment: project, Kind: "bind", At: int64(2000 + i*1000), Project: project, Issue: i + 1}
		if err := w.RecordActivity(e); err != nil {
			t.Fatal(err)
		}
	}
	// Late work remains bound to its original segment, not the latest assignment.
	recordEvent(t, w, "r", "session", "first", "start", "start", 2100)
	recordEvent(t, w, "r", "session", "first", "stop", "stop", 2200)
	recordEvent(t, w, "r", "session", "unassigned", "gap", "gap", 1100)
	client, ctx := connect(t, r)
	all := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", map[string]any{"runtime": "r", "session": "session"})
	first := &store.RecordedAssignment{Project: "first", Number: 1}
	second := &store.RecordedAssignment{Project: "second", Number: 2}
	wantAll := []store.RecordedActivity{
		{ID: "bind-u", Runtime: "r", Session: "session", Segment: "unassigned", Kind: "bind", At: 1000},
		{ID: "gap", Runtime: "r", Session: "session", Segment: "unassigned", Kind: "gap", At: 1100, Reason: "event_loss"},
		{ID: "bind-first", Runtime: "r", Session: "session", Segment: "first", Kind: "bind", At: 2000, Assignment: first},
		{ID: "start", Runtime: "r", Session: "session", Segment: "first", Kind: "start", At: 2100, Activity: "work", Category: "tool", Assignment: first},
		{ID: "stop", Runtime: "r", Session: "session", Segment: "first", Kind: "stop", At: 2200, Activity: "work", Category: "tool", Assignment: first},
		{ID: "bind-second", Runtime: "r", Session: "session", Segment: "second", Kind: "bind", At: 3000, Assignment: second},
	}
	if !reflect.DeepEqual(all.Data, wantAll) || all.NextCursor != nil {
		t.Fatalf("default assignment rows: got %+v, want %+v", all, wantAll)
	}
	for _, tt := range []struct {
		assignment       string
		events, segments int64
		classification   string
		rows             []store.RecordedActivity
	}{
		{"all", 6, 3, "mixed", wantAll},
		{"assigned", 4, 2, "assigned", wantAll[2:]},
		{"unassigned", 2, 1, "unassigned", wantAll[:2]},
	} {
		t.Run(tt.assignment, func(t *testing.T) {
			args := map[string]any{"assignment": tt.assignment}
			s := activityCall[store.ActivitySummaryResult](t, client, ctx, "activity_summary", args)
			p := activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", args)
			if s.Data.Events != tt.events || s.Data.Segments != tt.segments || len(p.Data) != 1 || p.Data[0].Assignment != tt.classification || p.Data[0].Events != tt.events {
				t.Fatal(s, p)
			}
			args["runtime"], args["session"] = "r", "session"
			events := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", args)
			if !reflect.DeepEqual(events.Data, tt.rows) || events.NextCursor != nil {
				t.Fatalf("persisted assignment rows: got %+v, want %+v", events, tt.rows)
			}
			wantFilters := store.AppliedActivityFilters{ActivityFilters: store.ActivityFilters{Runtime: "r", Assignment: tt.assignment}, Session: "session"}
			if events.Snapshot.Filters != wantFilters || events.Snapshot.Consistency != "request_snapshot_changing_view_per_page" {
				t.Fatalf("unexpected snapshot: %+v", events.Snapshot)
			}
			if _, err := time.Parse(time.RFC3339Nano, events.Snapshot.ReadAt); err != nil {
				t.Fatalf("invalid snapshot read_at: %v", err)
			}
		})
	}
	for _, tt := range []struct {
		name    string
		filters map[string]any
		want    []string
	}{
		{"half open", map[string]any{"from": "1970-01-01T00:00:02Z", "to": "1970-01-01T00:00:03Z"}, []string{"bind-first", "start", "stop"}},
		{"sub millisecond", map[string]any{"from": "1970-01-01T00:00:02.100000001Z", "to": "1970-01-01T00:00:02.200000001Z"}, []string{"stop"}},
		{"segment", map[string]any{"segment": "first"}, []string{"bind-first", "start", "stop"}},
		{"category", map[string]any{"category": "tool", "kind": "start"}, []string{"start"}},
		{"gap reason", map[string]any{"kind": "gap", "reason": "event_loss"}, []string{"gap"}},
		{"empty combination", map[string]any{"kind": "bind", "category": "tool"}, []string{}},
		{"unknown session", map[string]any{"session": "unknown"}, []string{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"runtime": "r", "session": "session"}
			for k, v := range tt.filters {
				args[k] = v
			}
			p := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", args)
			ids := []string{}
			for _, e := range p.Data {
				ids = append(ids, e.ID)
				if e.Segment == "first" && (e.Assignment == nil || e.Assignment.Project != "first" || e.Assignment.Number != 1) {
					t.Fatal(e)
				}
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Fatal(ids, tt.want)
			}
		})
	}
	p := activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", map[string]any{"from": "1970-01-01T00:00:02.1Z", "to": "1970-01-01T00:00:02.3Z"})
	if p.Data[0].Segments != 1 || p.Data[0].Assignment != "assigned" || p.Data[0].Events != 2 || *p.Data[0].FirstAt != 2100 {
		t.Fatal(p)
	}
}

func TestActivityDigestUnassignedShapeAndPrivacy(t *testing.T) {
	w, r, path := activityFixture(t)
	for i := 0; i < 23; i++ {
		session := fmt.Sprintf("session-%02d", i)
		recordEvent(t, w, "runtime", session, "segment", session, "bind", 1000)
		if i == 0 {
			if _, err := w.DB.Exec(`INSERT INTO activity_events(runtime,id,session,segment,kind,activity,at,payload) VALUES('runtime','gap','session-00','segment','gap','',2000,'{"reason":"event_loss","prompt":"SECRET_PROMPT","args":"SECRET_ARGS","result":"SECRET_RESULT","environment":"SECRET_ENV","file":"SECRET_FILE"}')`); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := w.AddProject(core.Project{Name: "assigned", Repo: "example/assigned", ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "reviewer@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddIssue("assigned", 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordActivity(core.ActivityEvent{ID: "assigned-bind", Runtime: "runtime", Session: "session-00", Segment: "assigned", Kind: "bind", At: 3000, Project: "assigned", Issue: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordActivity(core.ActivityEvent{ID: "assigned-start", Runtime: "runtime", Session: "session-00", Segment: "assigned", Kind: "start", At: 4000, Activity: "work", Category: "tool"}); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	client, ctx := connect(t, r)
	first := activityCall[store.ActivityPage[store.ActivityDigest]](t, client, ctx, "activity_digest", map[string]any{"limit": 20})
	if len(first.Data) != 20 || first.NextCursor == nil || first.Data[0].Runtime != "runtime" || first.Data[0].Session != "session-00" {
		t.Fatalf("first digest page: %+v", first)
	}
	digest := first.Data[0]
	if digest.Events != 2 || digest.Segments != 1 || digest.Gaps != 1 || digest.FirstAt == nil || *digest.FirstAt != 1000 || digest.LastAt == nil || *digest.LastAt != 2000 || digest.ByCategory["tool"] != 0 || digest.ByGapReason["event_loss"] != 1 {
		t.Fatalf("unexpected unassigned digest: %+v", digest)
	}
	if digest.Description == "" || !strings.Contains(digest.Description, "event_loss=1") || strings.Contains(digest.Description, "SECRET_") {
		t.Fatalf("unexpected digest description: %q", digest.Description)
	}
	repeated := activityCall[store.ActivityPage[store.ActivityDigest]](t, client, ctx, "activity_digest", map[string]any{"limit": 20})
	if !reflect.DeepEqual(first.Data, repeated.Data) || first.Snapshot.Filters != repeated.Snapshot.Filters {
		t.Fatal("digest is not deterministic")
	}
	last := activityCall[store.ActivityPage[store.ActivityDigest]](t, client, ctx, "activity_digest", map[string]any{"limit": 20, "cursor": *first.NextCursor})
	if len(last.Data) != 3 || last.NextCursor != nil {
		t.Fatalf("last digest page: %+v", last)
	}
	for i, row := range append(first.Data, last.Data...) {
		wantEvents := int64(1)
		if i == 0 {
			wantEvents = 2
		}
		if row.Runtime != "runtime" || row.Events != wantEvents || row.Segments != 1 || row.ByCategory == nil || row.ByGapReason == nil {
			t.Fatalf("unexpected digest row: %+v", row)
		}
	}
	empty := activityCall[store.ActivityPage[store.ActivityDigest]](t, client, ctx, "activity_digest", map[string]any{"runtime": "unknown"})
	if len(empty.Data) != 0 || empty.NextCursor != nil {
		t.Fatalf("unknown digest scope: %+v", empty)
	}
	afterSuccessful, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, afterSuccessful) {
		t.Fatal("digest changed database", err)
	}
	if _, err := w.DB.Exec(`INSERT INTO activity_events(runtime,id,session,segment,kind,activity,at,payload) VALUES('runtime','broken','session-01','segment','gap','',3000,'SECRET_BROKEN_JSON')`); err != nil {
		t.Fatal(err)
	}
	malformedBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "activity_digest", Arguments: map[string]any{"runtime": "runtime", "limit": 20}})
	b, _ := json.Marshal(result)
	if err != nil || result == nil || !result.IsError || !strings.Contains(string(b), "storage_error") || strings.Contains(string(b), "SECRET_") {
		t.Fatalf("malformed digest result: %s %v", b, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(malformedBefore, after) {
		t.Fatal("digest changed database", err)
	}
}

func TestActivityDigestExcludesAssignedSegments(t *testing.T) {
	w, r, _ := activityFixture(t)
	if _, err := w.AddProject(core.Project{Name: "assigned", Repo: "example/assigned", ExpectedGitHub: "reviewer", GitName: "Reviewer", GitEmail: "reviewer@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddIssue("assigned", 1, ""); err != nil {
		t.Fatal(err)
	}
	recordEvent(t, w, "r", "mixed", "free", "free-bind", "bind", 1000)
	if err := w.RecordActivity(core.ActivityEvent{ID: "issue-bind", Runtime: "r", Session: "mixed", Segment: "issue", Kind: "bind", At: 2000, Project: "assigned", Issue: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordActivity(core.ActivityEvent{ID: "issue-start", Runtime: "r", Session: "mixed", Segment: "issue", Kind: "start", At: 2100, Activity: "work", Category: "tool"}); err != nil {
		t.Fatal(err)
	}
	client, ctx := connect(t, r)
	got := activityCall[store.ActivityPage[store.ActivityDigest]](t, client, ctx, "activity_digest", map[string]any{"runtime": "r"})
	if len(got.Data) != 1 || got.Data[0].Events != 1 || got.Data[0].Segments != 1 || got.Data[0].FirstAt == nil || *got.Data[0].FirstAt != 1000 || strings.Contains(got.Data[0].Description, "tool") {
		t.Fatalf("assigned activity leaked into digest: %+v", got)
	}
}

func TestActivityKeysetAndChangingView(t *testing.T) {
	w, r, _ := activityFixture(t)
	recordEvent(t, w, "r", "s", "seg", "bind", "bind", 1000)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		recordEvent(t, w, "r", "s", "seg", id, "gap", 2000)
	}
	client, ctx := connect(t, r)
	args := map[string]any{"runtime": "r", "session": "s", "limit": 2, "from": "1970-01-01T00:00:01.000Z"}
	ids := []string{}
	for {
		p := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", args)
		for _, e := range p.Data {
			ids = append(ids, e.ID)
		}
		if p.NextCursor == nil {
			break
		}
		args["cursor"] = *p.NextCursor
		args["from"] = "1970-01-01T00:00:01Z"
		args["assignment"] = "all"
	}
	if !reflect.DeepEqual(ids, []string{"bind", "a", "b", "c", "d", "e"}) {
		t.Fatal(ids)
	}
	delete(args, "cursor")
	first := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", args)
	recordEvent(t, w, "r", "s", "seg", "late-before", "gap", 1500)
	recordEvent(t, w, "r", "s", "seg", "late-after", "gap", 2500)
	args["cursor"] = *first.NextCursor
	args["limit"] = 100
	p := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", args)
	ids = nil
	for _, e := range p.Data {
		ids = append(ids, e.ID)
	}
	if !reflect.DeepEqual(ids, []string{"b", "c", "d", "e", "late-after"}) || p.Snapshot.Consistency != "request_snapshot_changing_view_per_page" {
		t.Fatal(p)
	}
	delete(args, "cursor")
	p = activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", args)
	if len(p.Data) != 8 {
		t.Fatal(p)
	}
	// New sessions before a session cursor are visible only on a new traversal.
	firstSessions := activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", map[string]any{"limit": 1})
	if firstSessions.NextCursor != nil {
		t.Fatal(firstSessions)
	}
	recordEvent(t, w, "r", "z", "seg", "z", "bind", 1000)
	firstSessions = activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", map[string]any{"limit": 1})
	recordEvent(t, w, "r", "a", "seg", "a-bind", "bind", 1000)
	pSessions := activityCall[store.ActivityPage[store.RecordedSession]](t, client, ctx, "session_list", map[string]any{"limit": 1, "cursor": *firstSessions.NextCursor})
	if len(pSessions.Data) != 1 || pSessions.Data[0].Session != "z" {
		t.Fatal(pSessions)
	}
}

func TestActivitySafeValidationAndEmptyResults(t *testing.T) {
	w, r, _ := activityFixture(t)
	client, ctx := connect(t, r)
	empty := activityCall[store.ActivitySummaryResult](t, client, ctx, "activity_summary", map[string]any{})
	if empty.Data.Events != 0 || empty.Data.FirstAt != nil || empty.Data.LastAt != nil || len(empty.Data.ByKind) != 0 {
		t.Fatal(empty)
	}
	recordEvent(t, w, "r", "s", "seg", "bind", "bind", 1000)
	recordEvent(t, w, "r", "s", "seg", "gap", "gap", 2000)
	first := activityCall[store.ActivityPage[store.RecordedActivity]](t, client, ctx, "activity_list", map[string]any{"runtime": "r", "session": "s", "limit": 1})
	badAnchorBytes, _ := base64.RawURLEncoding.DecodeString(*first.NextCursor)
	var badAnchor map[string]any
	json.Unmarshal(badAnchorBytes, &badAnchor)
	badAnchor["id"] = "missing"
	badAnchorBytes, _ = json.Marshal(badAnchor)
	for _, tt := range []struct {
		name string
		args map[string]any
	}{
		{"activity_list", map[string]any{}},
		{"activity_summary", map[string]any{"assignment": "wrong"}},
		{"session_list", map[string]any{"limit": 0}}, {"session_list", map[string]any{"limit": 101}},
		{"session_list", map[string]any{"limit": -1}}, {"session_list", map[string]any{"limit": 1.5}},
		{"session_list", map[string]any{"from": "not-a-date"}},
		{"session_list", map[string]any{"from": "1970-01-01T00:00:00+00:00"}},
		{"session_list", map[string]any{"from": "1970-01-01T00:00:02Z", "to": "1970-01-01T00:00:01Z"}},
		{"session_list", map[string]any{"from": "1970-01-01T00:00:01Z", "to": "1970-01-01T00:00:01Z"}},
		{"session_list", map[string]any{"runtime": strings.Repeat("x", 201)}},
		{"session_list", map[string]any{"runtime": "SECRET_INPUT?"}},
		{"session_list", map[string]any{"cursor": "SECRET_CURSOR"}},
		{"session_list", map[string]any{"cursor": *first.NextCursor}},
		{"activity_list", map[string]any{"runtime": "r", "session": "s", "kind": "wrong"}},
		{"activity_list", map[string]any{"runtime": "r", "session": "s", "category": "wrong"}},
		{"activity_list", map[string]any{"runtime": "r", "session": "s", "reason": "wrong"}},
		{"activity_list", map[string]any{"runtime": "r", "session": "s", "cursor": *first.NextCursor, "assignment": "unassigned"}},
		{"activity_list", map[string]any{"runtime": "r", "session": "s", "cursor": base64.RawURLEncoding.EncodeToString(badAnchorBytes)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := client.CallTool(ctx, &mcp.CallToolParams{Name: tt.name, Arguments: tt.args})
			if err == nil && !got.IsError {
				t.Fatal("accepted", tt.args)
			}
			b, _ := json.Marshal(got)
			if strings.Contains(string(b), "SECRET_") {
				t.Fatalf("input leaked: %s", b)
			}
		})
	}
	// Corrupt normalized data fails closed without reflecting payload or SQL details.
	if _, err := w.DB.Exec(`INSERT INTO activity_events(runtime,id,session,segment,kind,activity,at,payload) VALUES('r','broken','s','seg','gap','',3000,'SECRET_BROKEN_JSON')`); err != nil {
		t.Fatal(err)
	}
	got, err := client.CallTool(ctx, &mcp.CallToolParams{Name: "activity_list", Arguments: map[string]any{"runtime": "r", "session": "s"}})
	b, _ := json.Marshal(got)
	if err != nil || !got.IsError || !strings.Contains(string(b), "storage_error") || strings.Contains(string(b), "SECRET_") || strings.Contains(string(b), "SQL") {
		t.Fatal(string(b), err)
	}
}

func TestActivityCancellationReleasesPool(t *testing.T) {
	_, r, _ := activityFixture(t)
	client, ctx := connect(t, r)
	conn, err := r.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.CallTool(callCtx, &mcp.CallToolParams{Name: "activity_summary"}); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for r.DB.Stats().WaitCount == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.DB.Stats().WaitCount == 0 {
		t.Fatal("query never reached pool")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled call succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not cancel")
	}
	for r.DB.Stats().WaitDuration == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.DB.Stats().WaitDuration == 0 {
		t.Fatal("query still waiting")
	}
}

func TestActivitySummaryConsistentDuringArrivals(t *testing.T) {
	w, r, _ := activityFixture(t)
	recordEvent(t, w, "r", "s", "seg", "bind", "bind", 1000)
	client, ctx := connect(t, r)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 40; i++ {
			err := w.RecordActivity(core.ActivityEvent{Runtime: "r", Session: "s", Segment: "seg", ID: fmt.Sprintf("gap-%02d", i), Kind: "gap", Reason: "event_loss", At: int64(2000 + i)})
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 20; i++ {
		s := activityCall[store.ActivitySummaryResult](t, client, ctx, "activity_summary", map[string]any{})
		if s.Data.Events != s.Data.ByKind["bind"]+s.Data.ByKind["gap"] || s.Data.Gaps != s.Data.ByReason["event_loss"] || s.Data.Events != s.Data.UnassignedEvents || s.Data.Segments != 1 {
			t.Fatalf("inconsistent request snapshot: %+v", s)
		}
		d := activityCall[store.ActivityPage[store.ActivityDigest]](t, client, ctx, "activity_digest", map[string]any{"runtime": "r"})
		if len(d.Data) != 1 || d.Data[0].Runtime != "r" || d.Data[0].Session != "s" || d.Data[0].Segments != 1 || d.Data[0].Events < d.Data[0].Gaps {
			t.Fatalf("inconsistent digest snapshot: %+v", d)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
