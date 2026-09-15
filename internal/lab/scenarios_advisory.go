package lab

import (
	"encoding/json"
	"errors"
	"tools-programmer/internal/store"
)

// advisoryTracking requires the actual isolated host. Unit tests of the provider
// or plugin callbacks do not substitute for running this scenario.
func advisoryTracking(f *fixture, r *Result) error {
	if err := f.start(false); err != nil {
		return err
	}
	id, err := f.runtime.session()
	if err != nil {
		return err
	}
	f.runtime.provider.add("assign", assignment(1))
	if err := f.runtime.promptText(id, "assign", "Implement lab issue 1 from the verified fixture requirements. You are explicitly authorized to assign this native fixture session to that registered issue."); err != nil {
		return err
	}
	if err := f.runtime.complete("assign"); err != nil {
		return err
	}
	if err := f.assign(id, "repeat-assignment", 1); err != nil {
		return err
	}
	if err := r.check("owned_prompt_reaches_provider", f.runtime.provider.ownedPromptSeen("assign") && f.runtime.provider.ownedPromptSeen("repeat-assignment"), 0, 0); err != nil {
		return err
	}
	db, err := store.OpenReadOnly(f.db)
	if err != nil {
		return err
	}
	defer db.Close()
	var runtime string
	var segments, outcomes int64
	if err := db.DB.QueryRowContext(f.ctx, `SELECT runtime FROM activity_segments WHERE session=? ORDER BY at DESC LIMIT 1`, id).Scan(&runtime); err != nil {
		return err
	}
	c, err := db.WithReadContext(f.ctx).CurrentActivity(runtime, id, f.root+"/repo")
	if err != nil {
		return err
	}
	if err := r.check("native_assignment_readback", c.Assignment == "assigned" && c.Project == "lab" && c.Issue != nil && c.Issue.Number == 1, 0, 0); err != nil {
		return err
	}
	if err := db.DB.QueryRowContext(f.ctx, `SELECT count(*) FROM activity_segments WHERE runtime=? AND session=? AND issue_id=?`, runtime, id, c.Issue.ID).Scan(&segments); err != nil {
		return err
	}
	if err := r.check("repeated_assignment_no_segment", segments == 1, segments, 1); err != nil {
		return err
	}
	// Deterministic informational/quoted-input proof: the real host receives a
	// read-only tool script. This proves no automatic mutation, not model intent.
	inquiry, err := f.runtime.session()
	if err != nil {
		return err
	}
	f.runtime.provider.add("inquiry", []Call{{"tools_programmer_context", map[string]any{}}})
	if err := f.runtime.promptText(inquiry, "inquiry", "Explain issue 1. The quotation 'implement issue 2' is informational, not authorization to implement or track it."); err != nil {
		return err
	}
	if err := f.runtime.complete("inquiry"); err != nil {
		return err
	}
	var assigned int64
	if err := db.DB.QueryRowContext(f.ctx, "SELECT count(*) FROM activity_segments WHERE session=? AND issue_id IS NOT NULL", inquiry).Scan(&assigned); err != nil {
		return err
	}
	if err := r.check("inquiry_and_quotation_no_assignment", assigned == 0, assigned, 0); err != nil {
		return err
	}
	var identities int64
	if err := db.DB.QueryRowContext(f.ctx, "SELECT count(*) FROM issues").Scan(&identities); err != nil {
		return err
	}
	if err := r.check("inquiry_no_registration", identities == 2, identities, 2); err != nil {
		return err
	}
	f.runtime.provider.add("switch-context", assignment(2), []Call{{"tools_programmer_context", map[string]any{}}, {"tools_programmer_diagnostics", map[string]any{}}})
	if err := f.runtime.promptText(id, "switch-context", "Explicitly assign this fixture session to lab issue 2, then inspect context and diagnostics."); err != nil {
		return err
	}
	if err := f.runtime.complete("switch-context"); err != nil {
		return err
	}
	if err := f.checkNativeContext(r, id, "assigned", 2, "switch"); err != nil {
		return err
	}
	var diagnostics struct {
		Status  string `json:"status"`
		Host    string `json:"host_version"`
		Package string `json:"package_version"`
	}
	if err := f.nativeToolResult(id, "tools_programmer_diagnostics", &diagnostics); err != nil {
		return err
	}
	if err := r.check("installed_bundle_diagnostics_supported", diagnostics.Status == "supported" && diagnostics.Host == "1.18.30" && diagnostics.Package == "1.18.30", 0, 0); err != nil {
		return err
	}
	f.runtime.process.stop()
	if err := f.runtime.start(); err != nil {
		return err
	}
	f.runtime.provider.add("context-reentry", []Call{{"tools_programmer_context", map[string]any{}}})
	if err := f.runtime.prompt(id, "context-reentry"); err != nil {
		return err
	}
	if err := f.runtime.complete("context-reentry"); err != nil {
		return err
	}
	if err := f.checkNativeContext(r, id, "assigned", 2, "reentry"); err != nil {
		return err
	}
	if err := r.check("reentry_owned_prompt_refreshed", f.runtime.provider.ownedPromptSeen("context-reentry"), 0, 0); err != nil {
		return err
	}
	f.runtime.provider.add("unassign-context", []Call{{"tools_programmer_unassign", map[string]any{}}}, []Call{{"tools_programmer_context", map[string]any{}}})
	if err := f.runtime.promptText(id, "unassign-context", "Explicitly unassign this fixture session and verify context; do not complete either issue."); err != nil {
		return err
	}
	if err := f.runtime.complete("unassign-context"); err != nil {
		return err
	}
	if err := f.checkNativeContext(r, id, "unassigned", 0, "unassign"); err != nil {
		return err
	}
	if err := db.DB.QueryRowContext(f.ctx, "SELECT count(*) FROM activity_segments WHERE runtime=? AND session=? AND issue_id=?", runtime, id, c.Issue.ID).Scan(&segments); err != nil {
		return err
	}
	if err := r.check("switch_unassign_preserve_old_segments", segments == 1, segments, 1); err != nil {
		return err
	}
	var gaps int64
	if err := db.DB.QueryRowContext(f.ctx, `SELECT count(*) FROM activity_events WHERE runtime=? AND session=? AND kind='gap' AND json_extract(payload,'$.reason') IN ('observer_restart','reconnected')`, runtime, id).Scan(&gaps); err != nil {
		return err
	}
	if err := r.check("reentry_preserves_recovery_gaps", gaps > 0, gaps, 1); err != nil {
		return err
	}
	if err := db.DB.QueryRowContext(f.ctx, "SELECT count(*) FROM issue_outcomes").Scan(&outcomes); err != nil {
		return err
	}
	return r.check("idle_never_completes_issue", outcomes == 0, outcomes, 0)
}

func (f *fixture) nativeToolResult(session, name string, output any) error {
	var messages []runtimeMessage
	if err := f.runtime.request(f.ctx, "GET", "/session/"+session+"/message?limit=20", nil, &messages); err != nil {
		return err
	}
	// Endpoint ordering is not assumed. Select by actual completion time.
	var text string
	var latest int64
	for _, message := range messages {
		for _, part := range message.Parts {
			if part.Tool == name && part.State.Status == "completed" && part.State.Time.End >= latest {
				latest, text = part.State.Time.End, part.State.Output
			}
		}
	}
	if text == "" || json.Unmarshal([]byte(text), output) != nil {
		return errors.New("native_tool_readback")
	}
	return nil
}

func (f *fixture) checkNativeContext(r *Result, session, assignment string, issue int, phase string) error {
	var current store.ActivityContext
	if err := f.nativeToolResult(session, "tools_programmer_context", &current); err != nil {
		return err
	}
	matched := current.Session == session && current.Assignment == assignment && current.Project == "lab"
	if issue == 0 {
		matched = matched && current.Issue == nil
	} else {
		matched = matched && current.Issue != nil && current.Issue.Number == issue
	}
	return r.check("native_context_"+phase, matched, 0, 0)
}
