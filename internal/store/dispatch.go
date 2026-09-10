package store

import (
	"encoding/json"

	"tools-programmer/internal/core"
)

// Dispatch reads an exact immutable zero-duration delegation marker. A start
// persisted before interruption is sufficient provenance; a conflicting stop
// is not. It never searches by title, parent current issue, or temporal proximity.
func (s *Store) Dispatch(runtime, session, call string) (*core.DispatchRecord, error) {
	marker, err := core.DispatchMarker(runtime, session, call)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(s.context(), "SELECT session,segment,kind,activity,at,CASE WHEN length(CAST(payload AS BLOB))<=16384 THEN payload ELSE '' END FROM activity_events WHERE runtime=? AND id IN (?,?)", runtime, marker+"-start", marker+"-stop")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var start, stop *core.ActivityEvent
	for rows.Next() {
		var rowSession, segment, kind, activity, raw string
		var at int64
		var e core.ActivityEvent
		if err := rows.Scan(&rowSession, &segment, &kind, &activity, &at, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &e) != nil || e.Validate() != nil || e.Runtime != runtime || e.Session != session || e.Session != rowSession || e.Segment != segment || e.Kind != kind || e.Activity != activity || e.At != at || e.Activity != marker || e.Category != "delegation" || e.ID != marker+"-"+e.Kind || (e.Kind != "start" && e.Kind != "stop") {
			return nil, core.Fail("dispatch_conflict", "stored dispatch marker is inconsistent")
		}
		if e.Kind == "start" {
			start = &e
		} else {
			stop = &e
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if start == nil {
		if stop != nil {
			return nil, core.Fail("dispatch_conflict", "dispatch has no authoritative start marker")
		}
		return nil, nil
	}
	if stop != nil && (stop.Segment != start.Segment || stop.At != start.At) {
		return nil, core.Fail("dispatch_conflict", "dispatch boundaries disagree")
	}
	return &core.DispatchRecord{Runtime: runtime, Session: session, Segment: start.Segment, CallID: call, At: start.At}, nil
}
