# Estimate issues locally; measure only observed agent activity

Issue estimates and runtime observations live in the existing private SQLite database. Nothing is published to GitHub. The CLI and read-only MCP use the same `Service.IssueSummary` and a consistent SQLite read snapshot. **The observer is experimental: fixture tests do not establish accuracy on a live OpenCode/OpenChamber host.**

## Quick path

Use an already registered project. All IDs below are local IDs returned by the preceding command, not guessed values.

```sh
tools-programmer issue add --project demo --number 1 --remote-id OBSERVED_GITHUB_NODE_ID
tools-programmer issue link --issue-id ISSUE_ID --pr 8
tools-programmer issue link --issue-id ISSUE_ID --pr 9
tools-programmer issue propose --issue-id ISSUE_ID --kind initial --score 3 \
  --proposer agent-name --rationale 'Small known change with limited integration uncertainty'

# Only after a human explicitly accepts this proposal:
tools-programmer issue confirm --proposal-id PROPOSAL_ID --human human-name \
  --rationale 'Reviewed scope and accepted the baseline' --human-confirmed

tools-programmer issue summary --project demo --number 1
```

Omit `--remote-id` if it has not been observed; no remote lookup is performed. Later, `issue observe --issue-id ISSUE_ID --remote-id OBSERVED_ID` appends that observation without replacing the issue record. Repeating the same observation is idempotent; a different stable ID is rejected. A PR has exactly one local issue owner within its registered project/repository. Multiple PRs may serve that issue. Cross-project foreign keys and unique ownership prevent accidental reassignment. Ownership and recorded issue identities are immutable; correcting a mistaken identity is not an implicit merge/rewrite operation.

### Estimation and outcomes

| Record | Meaning |
|---|---|
| `initial` proposal + confirmation | Original baseline; only one initial proposal may be confirmed |
| `revision` proposal + confirmation | Justified scope change or unforeseen work; preserves the baseline |
| `final` proposal + confirmation | Retrospective relative assessment; later assessments append, never overwrite |
| Unestimated | No confirmed initial proposal; represented as `initial: null`, not score zero |
| `completed` / `cancelled` / `reopened` | Explicit issue outcome, independent of idle, session end or GitHub closure |

Use exactly **1, 2, 3, 5, 8, 13, 21** for relative complexity, uncertainty and effort. Compare with previously confirmed work: 1–3 indicates smaller/well-understood scope, 5–8 indicates more moving parts or uncertainty, and 13–21 indicates substantial scope/uncertainty worth discussing or splitting. These are guidance, not objective thresholds. Explain each proposal in its rationale. Points are never converted to hours or used as productivity/quality scores.

Use `issue propose --kind revision|final` with the same flags as the initial proposal, then confirm its returned proposal ID separately. Proposed and confirmed timestamps, proposer, distinct human confirmer, and both rationales are retained. Confirmation order has independent monotonic IDs; a later confirmation does not rewrite proposal order. `final` in a summary is the latest **confirmed historical** final assessment, including after reopening; it does not imply that the reopened issue is complete.

```sh
tools-programmer issue outcome --issue-id ISSUE_ID --outcome completed \
  --actor human-name --rationale 'Implementation and agreed verification completed'
tools-programmer issue outcome --issue-id ISSUE_ID --outcome reopened \
  --actor human-name --rationale 'New acceptance work identified'
```

Confirmation is a local declaration, **not authentication of human presence**. An agent must obtain actual explicit approval, never call its own proposal “human-confirmed,” and never infer consent from a successful test. There is no confirmation/assignment mutation tool in the read-only MCP.

## Recorder and accounting contract

`activity record` accepts one JSON object on stdin (maximum 16 KiB), with duplicate keys, unknown fields, nulls, invalid identifiers, and invalid states rejected. It is a **trusted local adapter boundary**, not a model-facing mechanism for inventing session IDs. Local users with database access can fabricate declarations; this is not a security boundary against the same OS account.

```json
{"id":"binding-event","runtime":"runtime-namespace","session":"actual-runtime-session","segment":"collector-execution-segment","kind":"bind","at":1788990000000,"project":"demo","issue":1}
```

Timestamps are Unix milliseconds; identifiers are bounded ASCII tokens. Normalized events contain only IDs, categories, timestamps, issue references and enumerated gap reasons. Domain rationale is intentional human/agent text stored separately, never telemetry payload.

| Kind | Additional fields |
|---|---|
| `bind` | Explicit `project` + `issue`, or `parent_session` + `parent_segment` + optional `dispatch_at`, or `resume: true`, or neither (unassigned) |
| `start` / `stop` | `activity`, `category`; optional `target` for permission/question waits blocking a specific tool |
| `gap` | Enumerated `reason`: `observer_restart`, `event_loss`, `uncertain_inference`, `uncorrelated_child`, `cancelled`, `reconnected`, `unsupported_lifecycle` |

Categories: `tool`, `inference`, `retry`, `permission`, `question`, `delegation`. Stops may arrive before starts. Identical event IDs/content are no-ops; conflicting IDs or activity boundaries fail without mutation. Bindings must precede dependent observations; callers may retry a rejected dependency after recording its binding. Assignment changes append in timestamp order and cannot rewrite already observed work. Descendants copy the referenced parent's assignment **at dispatch**, not at completion. A reused child uses a new segment; existing segments retain their issue. Unassigned activity is retained unassigned, without title inference.

Closed intervals are clipped to their assignment segment. The observer splits active intervals during an explicit switch. A later segment for another issue remains an internal accounting boundary; it is not projected as `until` or an unknown-gap `to` into the earlier issue's canonical response. A continuation for the same issue may expose that boundary because it appends new history to that issue. Within one runtime session, overlapping tools/streaming activity are unioned; they do not become extra agents. Per-session unions are summed for `agent_ms`, and unioned again for `active_elapsed_ms`. Two parallel ten-minute children plus a delegation-waiting parent produce **10 elapsed minutes and 20 agent-minutes**. An independently working parent adds its own observed time.

Permission/question waits and retry/delegation intervals mask inference. A targeted wait masks only its blocked tool; independent tools remain active. Retry has separate union and summed-agent fields. No active interval ends at “now”: an orphan start/stop is an explicit unknown gap. An interrupted observation cannot prove continuous activity through its missing suffix. A heartbeat supplies no active interval. A silent tool with a supported start and eventual terminal event retains the full observed interval unless interrupted by a gap.

## Opt-in OpenCode loading — not performed by build or tests

The artifact is in `integrations/opencode/`, **outside auto-discovery directories**. The existing MCP installer does not install it. No daemon or telemetry service is needed; the observer sends normalized events to a bounded local subprocess recorder.

After explicit consent, an operator can copy `plugin.ts`, `observer.mjs`, `parent-snapshot.mjs`, `session-snapshot.mjs`, `runtime-read.mjs` and `transport.mjs` together into an operator-owned directory where `@opencode-ai/plugin` is resolvable (for example a subdirectory under the existing OpenCode config directory, not its auto-discovered plugins folder). Preserve the existing config; append this tuple to its existing `plugin` array rather than replacing the file or other entries:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "plugin": [
    ["file:///absolute/observer/plugin.ts", {
      "binary": "/absolute/tools-programmer",
      "database": "/private/tools-programmer/history.db"
    }]
  ]
}
```

This is a **fragment**, not a replacement configuration. Review any existing permissions separately; do not grant broad permissions to enable timing. Quit and restart OpenCode only when the host owner authorizes it. To disable, remove only that tuple and restart with consent. Keep the history DB; disabling collection is not deletion.

The `tools_programmer_assign` custom tool asks permission and accepts only project and issue. Its implementation obtains `sessionID` from actual `ToolContext`, not model arguments. The runtime namespace hashes server origin/project/worktree; restart on a different origin is intentionally a different namespace and requires a new assignment. Stable namespaces can resume the last stored assignment but open a new observation segment with a recovery gap. Parallel observer instances for the same namespace are not certified; load one observer per project/server and do not install duplicate copies.

## What is proved, and what remains experimental

The adapter targets installed OpenCode **1.18.29** types/hooks. It uses raw `message.part.updated`, permission/question events, retry status and task metadata. It never uses OpenChamber's synthetic “busy” activity signal, which also includes retries. Tool `error` events supply a terminal boundary even when the after-hook is absent. Streaming text/reasoning updates provide conservative observed inference spans; busy and provider latency alone are not counted.

### Recover first-child correlation before recording its activity

When a child is first observed, its authoritative `session.created.info.parentID` identifies the only parent eligible for a fallback read. If the observer retained unresolved task-before dispatches for that parent, it makes **one** scoped `client.session.messages` read before creating the child's segment. It matches the exact parent task `callID` and `state.metadata.sessionId`/`parentSessionId` against the retained dispatch. Exactly one matching dispatch inherits its original issue snapshot, even if the parent has since switched issues. Queued duplicate child events and later task metadata do not create duplicate bindings. Completed background task metadata can still identify the dispatch; it does not complete the child.

This follows [tagged 1.18.29 TaskTool](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/opencode/src/tool/task.ts): `ctx.metadata` records parent/child IDs **before** `ops.prompt` starts child work. The [tagged v1 SDK](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/sdk/js/src/gen/sdk.gen.ts) and installed declarations specify `session.messages({path: {id}, query: {directory, limit}})`. The adapter uses the provided plugin client's existing transport, not an independently configured server client.

The read has a 3-second total deadline, one page of at most 20 messages (a full page is rejected as potentially incomplete), at most 256 parts and a 1-MiB response-stream bound. Only two normalized identifiers per task leave the reader; raw responses never reach the recorder. The bounded transport described below now cancels non-success bodies before the SDK can parse them. Missing/ambiguous metadata, saturation or failure produces the existing uncorrelated gap, without polling, title inference or historical reassignment. No unrelated session list is queried.

For a reused child, a new retained parent dispatch permits another metadata lookup only after **observed idle and no open activities**. A unique match opens a fresh segment for its first new activity; existing records are untouched. Ambiguous new execution starts remain unassigned. Multiple pending background extensions lack an exact per-run child-message association in task metadata: overlapping dispatch requests are marked unknown rather than reassigned or counted as concurrent agents.

### Durable before-hook provenance

The observer now persists the authoritative task-before observation **before returning from the hook**. It uses the existing immutable activity table: two `delegation` boundaries with identical timestamps describe an instantaneous dispatch marker, not a parent work interval or another agent. A deterministic SHA-256 marker identifies the exact runtime/session/call tuple; its stored parent segment and timestamp select the original assignment. No schema change, raw task arguments or new telemetry service is required.

`activity dispatch-show --runtime ID --session PARENT_ID --call CALL_ID` is a narrow **local read-only** lookup. It validates bounded identifiers, uses parameterized SQL, checks persisted boundary consistency, and requires an existing v3 DB without creating or migrating it. The observer's subprocess lookup allows 3 seconds and 4 KiB output, passes no provider credentials, and is not exposed as an MCP mutation. A missing record returns null; conflicting records fail closed. A start marker durably written before interruption is sufficient provenance even if its zero-duration stop was not written; that incomplete marker remains an explicit unknown in history. Timestamp-tied assignment changes are rejected rather than invalidating an already recorded dispatch.

After observer memory loss, runtime metadata must identify exactly one matching parent call for the child. The observer can then reload that call's persisted segment/timestamp and open a **new** correctly assigned child segment with a recovery gap. Parent issue B remains independent while the recovered child uses its original dispatch issue A. Previously unassigned/incomplete child records are never rewritten. Loss before any durable marker, conflicting data, or multiple equally plausible calls remains unknown; neither the parent's current issue nor the latest timestamp substitutes for exact evidence. Rebuild the binary together with the observer artifacts; older binaries lacking this lookup safely leave recovery unknown. No installation or upgrade is automatic.

The tagged [background registry](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/core/src/background-job.ts) is process-local: `extend` chains each new run behind `Deferred.await(previous)` and `start` returns an already-running job rather than creating a concurrent duplicate. Thus queued/overlapping **dispatch requests** are supported, not multiple concurrent agents in one child session. Task metadata lacks the registry's per-run sequence/child-message association, so attribution between multiple plausible queued requests after observer restart cannot be invented. A real server restart interrupts these jobs; a stored `running` message part alone does not prove a surviving process.

### Current-state recovery, without reconstructing lost time

**Durable restart limit:** without a retained in-memory invocation, restoration additionally requires either a fresh authoritative child-created event or a complete bounded child history containing exactly one user turn. A reused child can retain its original creation `parentID` even when dispatched elsewhere; an old matching task row alone is therefore insufficient for multi-turn/cross-parent restart attribution. Such cases remain unknown rather than selecting the newest marker. Quiescent reuse with a newly observed before-hook still has its exact live dispatch snapshot and remains supported. Compacted/deleted/incomplete histories are not certified provenance.

The first actual session event or tool-before hook after plugin creation lazily reads that session's current state. A detected recorder/queue loss opens a fresh segment and performs one new recovery attempt on the next actual observation. Root assignments resume only the stored assignment for the actual runtime session. A child dispatch can now be recovered from memory or its exact durable marker. A restarted observer lacking both forms of evidence marks child attribution unknown and does not count tool/inference/retry time for a guessed issue.

Recovery verifies `GET /session/{id}` against the provided project ID, then reads two normalized passes of session messages, pending permissions, pending questions and session status. Wait sets, retry state and single-turn provenance must agree; ordinary tool progress uses the newer projection rather than invalidating the whole recovery. The latter three routes are scoped by the provided directory; their bounded lists/maps are filtered to the actual session before projection. No session enumeration or provider calls are used. Each attempt allows at most **9 GETs, 3 seconds total, 2 MiB aggregate, 1 MiB per successful body, 128 pending requests per list/status-map entries and 256 message parts**. Failed endpoints, malformed/saturated data, or changing control/provenance evidence yield unknown coverage, not empty successful state. Pending/running/streaming updates and duplicate control notifications do not invalidate a stable recovery.

Only `running` tools with a currently active session (`busy` or `retry`) restart observation, at snapshot completion time—not at their historical runtime start timestamps. Idle/missing status plus persisted running parts remains unknown, not evidence of surviving work. Historical completed/error tools become tombstones, not active intervals or invented stops. Targeted permission/question waits are restored before tools, so blocked tools remain excluded while independent tools can count. An untargeted pending request prevents tool/inference counting until its reply because its blocked activity cannot be identified. Current retry state is separate; busy/heartbeat alone never creates an active interval. Snapshot recovery does not reconstruct inference. Live events received before the successful snapshot cutover cannot reverse its state; receipt-generation fencing and terminal tombstones prevent stale snapshots from reviving completed activities.

On a failed recovery, tool/inference coverage stays unknown until supported stable evidence is available. Each **distinct actual tool-before invocation** can request one new bounded attempt; duplicate hooks and ordinary event traffic cannot retry it. A reload or newly detected recording-loss epoch also retains its existing recovery path. There is no polling, timer retry, or loop-until-success. Previously unknown time is not filled in after recovery succeeds.

Snapshot imports and queued live evidence have different ordering rules. Receive-order watermarks and terminal receipts prevent stale snapshots from reviving completed work, including equal-millisecond events. They do not discard a fully observed live start merely because its later stop arrived while persistence was busy. Queued starts observed before snapshot cutover can resume only at cutover, without counting the earlier unknown interval. Permission/question asked/replied boundaries remain ordered observations instead of being dropped wholesale by cutover; targeted waits still mask only their blocked tool plus inference. Actual wait/retry transitions during a read remain unsafe and require a later stable attempt. Internal receipt order and persistence bookkeeping never enter normalized telemetry.

**Transport compatibility:** the v1 plugin facade lacks permission/question GET methods. `runtime-read.mjs` capability-checks the exact 1.18.29 generated client's internal `_client.get`/`getConfig` seam. It preserves the provided fetch implementation (including in-process fetch), base URL and configured headers, without changing client configuration or discovering credentials. The per-request wrapper rejects redirects and cancels every non-2xx response before the SDK's error-body parser. This version-pinned internal seam is tested against the installed SDK; unsupported client shapes fail closed and require adaptation, not fallback authentication.

**Reconnect semantics:** [the tagged plugin](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/opencode/src/plugin/index.ts) listens directly to `EventV2Bridge`, not SSE. [`server.connected` is generated by the SSE handler](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/opencode/src/server/routes/instance/httpapi/handlers/event.ts); it is not invented as a plugin reconnect trigger. A server/plugin instance restart uses lazy first-observation recovery; detected recorder loss uses the explicit loss path. An external UI reconnect does not itself interrupt this server-side observer. Unreported bus-listener loss cannot be inferred from silence or heartbeats.

Route evidence: tagged [permission GET](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/opencode/src/server/routes/instance/httpapi/groups/permission.ts), [question GET](https://github.com/anomalyco/opencode/blob/v1.18.29/packages/opencode/src/server/routes/instance/httpapi/groups/question.ts), and the installed 1.18.29 SDK declarations. Fixtures establish bounded projection and state transitions, not actual host timing accuracy.

**Known limitations requiring live evidence or follow-up:**

- Inference before the first streaming update and missing streaming terminal updates remain unknown; this is not a complete provider-compute timer.
- Timely task metadata, first-child fallback and quiescent reused-child lookup can correlate supported dispatches. Already observed unassigned work stays unchanged. Multiple pending/queued dispatches without an exact per-run association remain unknown; actual background/reused/delegated behavior requires live verification.
- Restart cannot recover an ephemeral event stream. Bounded current-state reconciliation restarts only provable current observations; old incomplete intervals remain gaps. Dispatches never durably recorded, ambiguous queued child runs and unreported bus loss are not reconstructed.
- Out-of-order normalized start/stop ingestion is supported. Arbitrarily reordered raw runtime notifications, multiple observer instances, cancellation edge cases and reconnect behavior are **not certified**. Ambiguity is incomplete evidence, not successful work.
- The bounded queue/subprocess can lose observations under failure or overload. Subsequent successful recording marks an event-loss gap; errors are static diagnostics without payloads. A permanently stopped observer leaves unresolved starts, not continuing activity.
- Configuration/type inspection, module tests and synthetic fixtures do not prove actual installed lifecycle accuracy. Remaining runtime acceptance is tracked separately in issues #3–#5, with reusable regression scenarios in #2. No provider spend, host restart or global installation is part of the fixture tests.

## Verification and rollback

```sh
go test ./internal/core ./internal/store ./internal/cli ./internal/mcpserver ./internal/service
node --test integrations/opencode/observer.test.mjs integrations/opencode/transport.test.mjs
node --test integrations/opencode/correlation.test.mjs integrations/opencode/parent-snapshot.test.mjs
node --test integrations/opencode/recovery.test.mjs integrations/opencode/session-snapshot.test.mjs
node --test integrations/opencode/durable-dispatch.test.mjs
# Optional installed-package loader fixture; no live server or provider access:
OPENCODE_PLUGIN_MODULE=/absolute/node_modules/@opencode-ai/plugin/dist/index.js \
  bun test integrations/opencode/plugin.test.ts
go test ./...
go vet ./...
```

Fixtures use temporary SQLite and fake GitHub/processes. Existing approval tests remain required. Additive v3 migration preserves registrations, bindings, review history and unresolved remote operations; read-only MCP never initializes or migrates. There is no destructive reset command. An authorized disposable fresh database can be initialized at an explicitly selected new private path, without deleting the existing one. Never discard unresolved real remote outcomes to bypass a guard.

Code rollback boundaries: domain/store migration and tests; issue CLI/shared summary/MCP and tests; optional observer and its isolated tests/docs. Removing the observer does not remove local issue functionality. **A migrated DB is not backward compatible with the older binary**: retain a v3-capable binary; do not roll back `user_version`, remove tables, or restore stale approval state. No commits or deployment are required to inspect these work units.
