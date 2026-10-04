# Review Time Tracking — Issue 42

## Objective

Implement https://github.com/jemanuelp/tools-programmer/issues/42: manually entered human review durations and automatically observed OpenCode review effort, reported separately for local and PR reviews.

## Problem and scope

Existing local reviews record verdicts, not effort. Observed activity has no explicit review context, and existing issue summaries do not cover project-only review effort. Reuse native interval accounting and project resolution; do not make effort records into verdicts or approvals.

### Authorized scope

- Local implementation, tests, and task tracking for issue 42.
- Preserve existing changes to `AGENTS.md` and `.opencode/skills/tools-programmer/SKILL.md`.
- The user authorized publishing the T1 feature-branch chain to `github.com/jemanuelp/tools-programmer`, including a draft tracker and new child PRs. This preparation phase is local-only: do not push, create/edit PRs, mutate issues or labels, or publish until the parent checks the prepared commits and evidence.
- No merge (local or remote), force-push, branch deletion, account or Git-identity configuration changes, production database migration, global installation, or native review/receipt/approval lifecycle.
- The user explicitly approved a clean replacement chain: tracker `feat/review-time-42-clean` from fresh `main` `7ea50552e3becff4a58c55da8224c497bbd51d0d`; first child `feat/review-time-42-manual-clean` targets that tracker; each later child targets its immediate previous child. Preserve all existing refs, including the polluted old tracker and old manual branch. Every new commit requires the registered project identity check; existing user changes are excluded except the explicitly authorized MCP fix in `internal/cli/mcp.go` and `internal/cli/mcp_test.go`.
- Native session tracking is unavailable in this runtime; do not invent session IDs, registrations, or assignments.
- Receipt-driven development is off for this clone; keep it off and do not start native review lifecycle work. Existing native review evidence remains incomplete and is not a receipt.

## Accepted requirements

1. First-release automatic capture covers OpenCode main sessions and subagents using explicit review signals, never prompt keywords.
2. Human duration, accumulated agent effort, and elapsed review time remain separate. Reuse interval unions, waiting/retry masks, and unknown-gap accounting. Do not double-classify one agent interval or sum overlapping phase elapsed totals into global elapsed time.
3. A project is mandatory; issue and PR are optional. Show project-only effort without inferred later assignment.
4. Stable identity and identical data replay return the existing result without adding time; changed-data reuse conflicts. Equal attributes alone do not identify duplicate reviews.
5. Preserve the actual reviewed SHA when known. Explicitly unknown revisions remain separately visible; never substitute current PR HEAD. New-revision work is separate, and effort never establishes review completion or approval.
6. Full manual and observer persistence/reporting tests, including concurrency, replay/conflict, cancellation/restart, and SHA changes, are required. A versioned real OpenCode main/subagent demonstration is also required before automatic coverage is complete.

## Implementation approach

Keep manual durations separate from observed agent events. Add review context and project-level persistence/reporting without duplicating native accounting truth. Explicit plugin-owned signals classify only confirmed observed activity; marker wall time is not measured work. Propagate context to children through exact task-call/session correlation; ambiguous correlation stays unknown. Preserve existing activity, verdict, approval, and unresolved-intent history.

## Work units

- [x] **T1 — Manual entry, durable replay, and project reporting.** Add validated manual duration/provenance, additive persistence, stable identity conflict handling, and a public entry-to-persistence-to-report path. Include tests and usage documentation.
  - Route: delegated direct; preparation and multiple non-trivial files require one bounded writer.
  - Forecast: 300–500 authored lines including tests; estimate, not a cap.
  - Checks: `go test ./internal/core ./internal/store ./internal/service ./internal/cli ./internal/mcpserver`.
  - Evidence: the original implementation is on the old child at `aa0cf98107829793b160806b0bc72fd11d42ba00`, whose parent `313360ae9326b1c554fbfc49391e219ba0667eea` is polluted by already-integrated identity-update work. Historical RED: `go test ./internal/core ./internal/store ./internal/service ./internal/cli ./internal/mcpserver -run 'TestReviewTime' -count=1` failed to compile because `ReviewTimeEntry` had not yet been implemented. Historical GREEN and prior checks remain historical evidence; no new RED was claimed for re-anchoring. Clean-chain commits: core `2f66710b7da5c0e5c4549cb01fed3349f822ec48` (116 lines); schema/MCP guard `311f9ce1f13798351ea82533b8b54516d94c4781` (142); storage/replay `d61af81cf4a14b95f9cab3cf99c9309edad49343` (260); report `71a3ca02b80d1f3eb468c44574e7fca408e8373c` (240); CLI/docs `64baf5fd1added99e7f0ae60c01d2dc4eb259ebd` (328). Each commit passed the registered project identity gate immediately beforehand. The prepared final code tree `bd9b9dc2b31b5ac2cb7853666e91ecee33162ba8` passed the focused per-slice tests/vet and all complete checks below. No remote publication occurred.
- [ ] **T2 — Explicit OpenCode review classification and child propagation.** Add main/subagent signal support and persisted observed review attribution, reusing existing active-interval accounting. Include deterministic observer, transport, correlation, and recovery tests.
  - Route: delegated direct; integration preparation and multiple non-trivial files.
  - Forecast: 200–350 authored lines including tests.
  - Checks: focused observer/transport/correlation/recovery tests, then `node --test integrations/opencode/*.test.mjs`; run existing plugin/type checks where applicable.
  - Evidence: not started; actual host capability not yet demonstrated.
- [ ] **T3 — End-to-end proof and supported-version documentation.** Exercise both public paths, add bounded real OpenCode main/subagent validation, document results and compatibility, and run complete repository checks.
  - Route: delegated direct; harness preparation and execution exceed the inline evidence budget.
  - Forecast: 100–200 authored lines plus runtime evidence.
  - Checks: `go test ./internal/lab ./internal/cli ./internal/mcpserver`, `go test -race ./internal/lab`, and the documented runtime scenario after it exists.
  - Evidence: not started; real OpenCode evidence is PENDING.

## Test-first and normalization policy

Use observed RED → GREEN → REFACTOR for relevant deterministic behavior. No strict-TDD configuration was found; this is the default applicable test-first policy, not an invented strict-mode setting. Normalize final source before functional verification or native review freeze; never change source under a frozen candidate. Tests use temporary databases and fake GitHub/services, never real approval writes.

### Complete checks

1. `gofmt -w cmd internal` (before final verification/review).
2. `go test ./...`
3. `go vet ./...`
4. `go test -race ./...`
5. `CGO_ENABLED=0 go build -trimpath -o bin/tools-programmer ./cmd/tools-programmer`
6. `TOOLS_PROGRAMMER_TEST_BINARY="$PWD/bin/tools-programmer" go test ./internal/cli -run TestBinarySmokeWithFakeGitHub -count=1`
7. Applicable OpenCode integration checks and the required real-host scenario.

## Delivery and review

- Implementation route: delegated direct, one writer at a time.
- Delivery strategy: `auto-chain`; chain strategy: `feature-branch-chain`, explicitly selected by the user. Local commits and eventual T1 chain publication are authorized; this phase does not perform remote writes.
- Forecast: 600–1,050 authored lines, generated files excluded. The approximately 400-line task heuristic is advisory; do not omit tests, compress code, or split incoherent units to satisfy it.
- Clean starting boundary: verified `main` `7ea50552e3becff4a58c55da8224c497bbd51d0d`. The old base `313360ae9326b1c554fbfc49391e219ba0667eea` diverges at `5ee2d23af0db`; its 3-dot diff against `main` contains 27 files, 1,176 additions, and 40 deletions from identity-update work (main history includes merge PR #37). Do not carry that inherited work into the new chain.
- Prepared-tree changed-line counts: passive tracker `83`; `feat/review-time-42-manual-clean` — core entry/report contract and validation tests (`116`); `feat/review-time-42-schema-clean` — additive schema/migration, v7 history-preservation test, and MCP no-migration guidance/fix (`142`); `feat/review-time-42-storage-clean` — stable-ID ledger persistence/replay and remaining store tests (`260`); `feat/review-time-42-report-clean` — manual-only aggregation, filtering, overflow and report tests (`240`); `feat/review-time-42-cli-clean` — public CLI entry/report path, CLI tests, and usage docs (`328`) plus the final task-progress follow-up (`13`), total `341/400`. Counts are `additions + deletions` from each exact prepared parent/child tree. All are at or below the 400-line hard PR budget; no size exception is authorized.
- Chain route: passive task-plan tracker from clean `main`; first child targets the tracker; later children target the immediate prior child. Preserve the old tracker/manual refs and current HEAD. The tracker does not mark T1 complete; T2/T3 remain pending.
- Native review is not run in this phase; do not create or claim any review receipt, approval, or lifecycle transition.
- Real-host validation may require substantial lab downloads/storage/RAM. Forecast exact cost and side effects before launch; no environment or authenticated remote session changes are authorized by this plan.

## Progress and recovery

- Read-only CodeGraph preparation is complete. User accepted the clean replacement base after the old tracker was shown to have a polluted diff.
- T1 is re-anchored on the approved clean chain from immutable `main`; the final source delta contains only the authorized 13-path T1 change plus the 2-path MCP fix. The old manual branch and pre-existing user-owned files remain preserved.
- Clean-snapshot focused checks passed: `go test ./internal/core -count=1`; `go test ./internal/store -run TestReviewTimeMigrationPreservesV7History -count=1`; `go test ./internal/cli -run TestMCPNeverInitializesOrMigrates -count=1`; `go test ./internal/store -count=1`; `go test ./internal/service -run 'TestReviewTimeReport' -count=1`; `go test ./internal/cli -run 'TestReviewTimeCLIRecordsReplaysAndReportsPublicHistory|TestMCPNeverInitializesOrMigrates' -count=1`; applicable per-slice `go vet` commands passed. The five complete checks below passed on the final behavior tree; the task-progress follow-up changes documentation only.
- T2/T3 remain unchecked; automatic OpenCode capture and the real-host demonstration remain pending.
- Engram recovery topic: `odd/review-time-42/tasks`; latest known mirror attempt returned `session_already_ended` (409). Mark the mirror pending; do not re-register a session or retry.
- Parent owns independent validation and any later publication. This writer did not push, create/edit PRs, mutate issues/labels, or merge anything.
