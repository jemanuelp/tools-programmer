# Estimate issues locally; measure only observed agent activity

Issue estimates and runtime observations live in the existing private SQLite database. Nothing is published to GitHub. The CLI and read-only MCP use the same `Service.IssueSummary` and a consistent SQLite read snapshot. **Activity accounting covers normalized observations only; it does not certify a live runtime collector.**

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

Closed intervals are clipped to their assignment segment. The observer splits active intervals during an explicit switch. Within one runtime session, overlapping tools/streaming activity are unioned; they do not become extra agents. Per-session unions are summed for `agent_ms`, and unioned again for `active_elapsed_ms`. Two parallel ten-minute children plus a delegation-waiting parent produce **10 elapsed minutes and 20 agent-minutes**. An independently working parent adds its own observed time.

Permission/question waits and retry/delegation intervals mask inference. A targeted wait masks only its blocked tool; independent tools remain active. Retry has separate union and summed-agent fields. No active interval ends at “now”: an orphan start/stop is an explicit unknown gap. An interrupted observation cannot prove continuous activity through its missing suffix. A heartbeat supplies no active interval. A silent tool with a supported start and eventual terminal event retains the full observed interval unless interrupted by a gap.

## Verification and rollback

```sh
go test ./internal/core ./internal/store ./internal/cli ./internal/mcpserver ./internal/service
go test ./...
go vet ./...
```

Fixtures use temporary SQLite and fake GitHub/processes. Existing approval tests remain required. Additive v3 migration preserves registrations, bindings, review history and unresolved remote operations; read-only MCP never initializes or migrates. There is no destructive reset command. An authorized disposable fresh database can be initialized at an explicitly selected new private path, without deleting the existing one. Never discard unresolved real remote outcomes to bypass a guard.

Code rollback boundary: the local issue workflow, including domain/store migration, recorder, CLI/shared summary/MCP, and their tests and documentation. **A migrated DB is not backward compatible with the older binary**: retain a v3-capable binary; do not roll back `user_version`, remove tables, or restore stale approval state. No commits or deployment are required to inspect these work units.
