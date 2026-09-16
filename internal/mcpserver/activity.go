package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tools-programmer/internal/core"
	"tools-programmer/internal/store"
)

func addActivityTools(server *mcp.Server, db *store.Store) {
	addActivity(server, "activity_summary", "Count matching recorded events and segments in this database, not active time or observer health. UTC half-open bounds; one request snapshot.", func(ctx context.Context, in store.ActivityFilters) (store.ActivitySummaryResult, error) {
		return db.WithReadContext(ctx).ActivitySummary(in)
	})
	addActivity(server, "session_list", "List only sessions with matching recorded events. Counts and mixed classification cover matching segments, not full sessions. Keyset runtime/session order; default 20, maximum 100; next_cursor null ends traversal. Changing view per page.", func(ctx context.Context, in store.SessionQuery) (store.ActivityPage[store.RecordedSession], error) {
		return db.WithReadContext(ctx).SessionList(in)
	})
	addActivity(server, "activity_digest", "Summarize the observed shape of unassigned activity per session. This is not a semantic task summary or complete agent-time calculation. Keyset runtime/session order; default 20, maximum 100; next_cursor null ends traversal. Changing view per page.", func(ctx context.Context, in store.ActivityDigestQuery) (store.ActivityPage[store.ActivityDigest], error) {
		return db.WithReadContext(ctx).ActivityDigest(in)
	})
	addActivity(server, "activity_list", "Inspect allowlisted events for a runtime/session, including kind=gap and reason. Assignment comes from persisted segments. Keyset at/id order; default 20, maximum 100; next_cursor null ends traversal. Unknown sessions return empty data; changing view per page.", func(ctx context.Context, in store.ActivityQuery) (store.ActivityPage[store.RecordedActivity], error) {
		return db.WithReadContext(ctx).ActivityList(in)
	})
}

// Typed results publish explicit output schemas while preserving the data envelope.
func addActivity[In, Out any](server *mcp.Server, name, description string, query func(context.Context, In) (Out, error)) {
	no := false
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: "Read-only local observations. " + description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &no, IdempotentHint: true, OpenWorldHint: &no}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			var zero Out
			// Existing indexes do not bound global aggregation work. Limit its wall time
			// as well as page memory, and propagate cancellation into database/sql.
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if ctx.Err() != nil {
				return nil, zero, errors.New("request canceled")
			}
			out, err := query(ctx, in)
			if err != nil {
				if ctx.Err() != nil {
					return nil, zero, errors.New("request canceled or activity query deadline exceeded")
				}
				var typed *core.Error
				if errors.As(err, &typed) {
					return nil, zero, errors.New(typed.Code + ": " + typed.Message)
				}
				return nil, zero, errors.New("storage_error: cannot read stored history; verify database compatibility and availability")
			}
			return nil, out, nil
		})
}
