# Inspect recorded activity without assigning it

Use `activity_summary`, `session_list`, and `activity_list` to inspect observations in the selected tools-programmer database, including unassigned work with zero registered issues. **Recorded counts and timestamp spans are not active elapsed time or evidence that an observer is healthy.** These tools do not change `issue_summary`.

## Quick path

Start the existing stdio endpoint with `tools-programmer --db /absolute/history.db mcp serve`. The activity tools below are read-only; the separate `issue_register` tool can register issues locally. In an MCP client, call:

1. `activity_summary {"assignment":"unassigned"}` to count unassigned observations.
2. `session_list {"limit":20}` to obtain recorded `runtime` and native `session` identifiers.
3. `activity_list {"runtime":"RUNTIME","session":"SESSION","limit":20}` using a returned pair.
4. `activity_list {"runtime":"RUNTIME","session":"SESSION","kind":"gap","reason":"event_loss"}` to inspect gaps without a separate tool.

These are MCP tool calls, not new CLI subcommands. Sessions are only those with matching stored events—not all OpenCode conversations. Unknown sessions and unmatched filters return `data: []`; an empty summary has zero counts, empty breakdown objects and null timestamps. Empty results say nothing about work elsewhere. Gaps indicate missing recorded observations, not proof of a capture bug.

## Filters and time bounds

| Tool | Inputs |
|---|---|
| `activity_summary` | Optional `runtime`, `from`, `to`, `assignment` |
| `session_list` | Summary filters plus optional `limit`, `cursor` |
| `activity_list` | Required `runtime`, `session`; optional `segment`, `from`, `to`, `kind`, `category`, `reason`, `assignment`, `limit`, `cursor` |

- `assignment`: `all` (default), `assigned`, or `unassigned`, resolved from the persisted segment binding. No project filter or directory inference is needed for null bindings.
- `kind`: `bind`, `start`, `stop`, `gap`.
- `category`: `tool`, `inference`, `retry`, `permission`, `question`, `delegation`.
- `reason`: `observer_restart`, `event_loss`, `uncertain_inference`, `uncorrelated_child`, `cancelled`, `reconnected`, `unsupported_lifecycle`.
- All specified filters are combined with AND. Inapplicable combinations such as `kind=bind, category=tool` match no events. Empty optional strings mean no filter; empty assignment means `all`.
- Bounds must be UTC RFC3339 ending in `Z`, from the Unix epoch through `2100-01-01T00:00:00Z`. Offsets, including `+00:00`, are rejected. Fractional seconds up to nanosecond precision are supported. Each bound is rounded **up** to milliseconds, preserving `[from, to)` membership for stored integer timestamps. Equal or reversed normalized bounds are invalid. Returned `snapshot.filters` contains normalized bounds.
- `limit` is 1–100, default 20 when omitted or null. Both lists return at most that many rows. Invalid types, limits, enums, identifiers, bounds or cursors fail safely; input values and database internals are not included in application errors.

For example, `{"from":"2026-09-14T10:00:00Z","to":"2026-09-14T11:00:00Z"}` includes events at 10:00 but excludes events at 11:00. These are event timestamps, not arrival times or overlap queries on elapsed intervals.

## Output and count scope

The SDK publishes input and output schemas at discovery. Every successful result keeps `data` and adds `snapshot: {read_at, consistency, filters}`. `read_at` is the UTC request read-transaction start time, not an insertion watermark, event time or durable snapshot ID. `consistency` is `request_snapshot_changing_view_per_page`.

| Result | Fields under `data` |
|---|---|
| Summary object | `events`, `segments`, `gaps`, `assigned_events`, `unassigned_events`, `assigned_segments`, `unassigned_segments`, `first_at`, `last_at`, `runtimes`, `sessions`, `by_kind`, `by_reason`, `breakdowns_truncated` |
| Session row | `runtime`, `session`, the same count/timestamp fields through `last_at`, and `assignment` |
| Activity row | `id`, `runtime`, `session`, `segment`, `kind`, `at`, applicable `activity`, `category`, `reason`, and `assignment: {project, number}` or `null` |

All counts and classifications describe **matching events**. A segment is counted once if it has at least one matching event, even if its binding event falls outside the time range. Bind and gap events count as events. Session `first_at`/`last_at` are the minimum/maximum matching event timestamps in Unix milliseconds, not full-session bounds. The summary counts distinct `(runtime, session)` pairs, not session IDs alone. No registry totals are included.

Session assignment is `assigned` when all matching segments are assigned, `unassigned` when none are, and `mixed` when both exist. Filtering `assignment=assigned` can therefore turn a full-history mixed session into an assigned row. A session assigned to two different issues is still `assigned`, not a fabricated single issue; inspect activity rows to see each persisted assignment. Late events retain their segment's assignment after switches. Original event issue/project payloads never override that binding.

Breakdowns have at most four kind keys and seven reason keys. These complete finite allowlists need no truncation, so `breakdowns_truncated` is false. Unexpected stored labels fail with a sanitized storage error rather than becoming arbitrary output keys. No pending interval or wall-clock span is converted into invented active time.

## Pagination and concurrent arrivals

List responses have the shape:

```json
{"data": [], "next_cursor": null, "snapshot": {"read_at": "2026-09-14T12:00:00Z", "consistency": "request_snapshot_changing_view_per_page", "filters": {"assignment": "all"}}}
```

For a nonterminal page, pass the exact returned string:

```text
session_list {"assignment":"unassigned","limit":20}
session_list {"assignment":"unassigned","limit":20,"cursor":"RETURNED_NEXT_CURSOR"}

activity_list {"runtime":"RUNTIME","session":"SESSION","kind":"gap","limit":20}
activity_list {"runtime":"RUNTIME","session":"SESSION","kind":"gap","limit":20,"cursor":"RETURNED_NEXT_CURSOR"}
```

`next_cursor: null` is always terminal; it is never omitted. Sessions sort by `(runtime, session)` using SQLite binary text order. Events sort by `(at, id)` inside the required runtime/session. Event identity is `(runtime, id)`. Equal timestamps do not cause missing or duplicated records on unchanged data.

Cursors are opaque, bounded to 2048 characters and bound to the versioned tool/order scope and normalized filters. Equivalent UTC spellings and omitted versus explicit `assignment=all` can reuse a cursor. Page size may change. Switching filters, tool, runtime or session requires a fresh traversal. Malformed cursors or anchors absent from the filtered dataset fail safely. Cursors are navigation tokens, not signatures, authorization or secrecy mechanisms; clients should not construct or edit them.

Each request uses one SQLite read transaction, keeping its aggregates and breakdowns internally consistent. **There is no cross-page snapshot.** The existing schema has no supported durable insertion watermark, and this feature adds no ledger. An insertion after the cursor sort key can appear on a later page. A late event at or before an already traversed key is not revisited; start a fresh traversal to see it. Likewise, a newly recorded session sorting before the current session cursor is not revisited. Already returned session counts can change between requests. A terminal cursor does not promise that no later arrivals exist.

## Read-only, privacy and query budget

The server opens only an existing compatible database through `OpenReadOnly`. Missing, older, newer or corrupt state is neither created nor migrated. Queries use parameterized SQL, no external adapters, and no observer, OpenCode, Engram, GitHub, authentication, assignment, registration or backfill calls. Cancellation reaches `database/sql`; each activity request additionally has a five-second deadline and never returns partially computed success.

Existing primary keys cover event runtime/ID and segment runtime/session/segment; the segment issue index supports binding lookup. There is no full event `(runtime, session, at, id)` index. Runtime-scoped reads can use the existing event key, but time ordering and global/session aggregation may scan and use temporary SQLite grouping/sorting. Results use `LIMIT limit+1`, bounded DTO accumulation, finite breakdowns and keyset rather than OFFSET pagination. The deadline bounds request duration, **not constant-time database work**. Broad histories can hit the deadline; narrow runtime/time filters or use a smaller history. No query-time index creation, schema migration or index guarantee is implied.

Only the documented projection is serialized. Raw payloads, prompts, tool arguments/results, targets, parent/dispatch data, environment values and directory metadata are excluded. Runtime/session/segment/event/activity identifiers share ingestion's ASCII policy: 1–200 characters, starting alphanumeric, then alphanumeric or `_.:/-`. Project names retain the existing registry policy. Oversized or invalid projected identifiers fail closed, without truncation or reflection into errors. **A syntactically valid identifier can still contain a secret:** never put secrets in identifiers, and authorize clients to read the entire selected database. These tools are not a secret detector or a per-project access-control boundary.
