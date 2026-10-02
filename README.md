# tools-programmer

A Go command-line tool for **local PR review history** and **explicit, SHA-bound GitHub approvals**, backed by a private SQLite database. The SQLite engine is compiled into the executable; the mutable history file is external. An optional stdio MCP server lets agents query the same history and register existing GitHub issues locally. No background service, auto-reviewer, or repository-specific configuration is required.

Local **issue estimates and agent activity** are also available: [issue workflow and experimental OpenCode observer](docs/issues.md). Estimates use human-confirmed Fibonacci **1, 2, 3, 5, 8, 13, 21**, never hours. Runtime accuracy is not certified; summaries expose unknown observation gaps rather than fabricating time.

For selectable, isolated runtime regression work, see the [Docker/Ubuntu integration laboratory](docs/lab.md). Preparation is explicit; default runs use only a controlled local provider, never paid credentials.

**A local `approved` verdict is not a published GitHub approval. Green CI is not a technical review.** The tool records declarations and applies authorized actions; it cannot decide that a human or agent actually reviewed code.

## Build and first review

Requires Go 1.26+ for this module. Remote commands require an existing authenticated `gh` installation (the adapter uses `gh api`, compatible with gh 2.45). Local history commands do not need `gh` or network access.

```sh
go mod download
CGO_ENABLED=0 go build -trimpath -o bin/tools-programmer ./cmd/tools-programmer
bin/tools-programmer --version
bin/tools-programmer --help

# Synchronize the bundled OpenCode skill, integration and MCP registration.
# The command uses the selected database path but never opens or changes it.
bin/tools-programmer --db /private/directory/history.db sync opencode

bin/tools-programmer project add --name demo --repo example/demo --path /path/to/checkout \
  --github-login expected-login --git-name 'Expected User' --git-email expected@example.com
bin/tools-programmer project list
bin/tools-programmer pr sync --project demo --prs 8
bin/tools-programmer pr list --project demo
```

Repository references accept `owner/repo` or `host/owner/repo`, normalize to lowercase, and default to `github.com`. URLs, `.git` suffixes and host ports are not accepted. Registration is **not** evidence of access. Repository renames/redirects are rejected during inspection. Project name and repository are unique and immutable in this MVP; local paths are optional references and can be explicitly rebound, never used as execution directories.

## Version identity and release builds

The next version is **0.2.0**, defined once in `internal/core/version.go`.
Versioning follows SemVer; Git tags named `vMAJOR.MINOR.PATCH` identify releases,
not the SQLite schema number. This checkout prepares that version; it does not
publish a release. Ordinary `go build` always produces a development identity,
even at a tagged commit.

`--version` keeps the JSON `data.version` string and adds string fields `commit`,
`state` (`clean`, `dirty`, or `unknown`), and `build` (`development` or `release`).
For example, without source metadata:

```json
{"data":{"version":"0.2.0-dev+unknown.unknown","commit":"unknown","state":"unknown","build":"development"}}
```

With Git metadata, development versions use
`0.2.0-dev+FULL_COMMIT.dirty` (or `.clean`). `commit` is the full source HEAD SHA;
for dirty builds it identifies the base commit, **not** the uncommitted changes.
MCP advertises the same version string. Neither version nor help opens a database;
version inspection uses embedded Go build information, never a runtime Git command.

### Build an existing release tag

First review, test, and commit the version change through the normal authorized
workflow. Only with separate approval, create and publish an annotated tag at that
exact reviewed commit. Never move a published tag: choose a new SemVer version for
new source. A GitHub release, uploads, and installation are separate optional actions,
not part of building. The commands below only **verify an existing tag and build**;
they do not create or publish one.

From the clean checkout of that tag, run in a shell with Python 3 and Go available:

```sh
set -eu
test -z "$(git status --porcelain --untracked-files=all)"
CGO_ENABLED=0 go build -trimpath -buildvcs=true -o bin/tools-programmer ./cmd/tools-programmer
version=$(bin/tools-programmer --version | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["version"].split("-dev+")[0])')
tag="v$version"
test "$(git rev-parse HEAD)" = "$(git rev-parse --verify "refs/tags/$tag^{commit}")"
test -z "$(git status --porcelain --untracked-files=all)"
CGO_ENABLED=0 go build -trimpath -buildvcs=true \
  -ldflags "-X tools-programmer/internal/core.ReleaseTag=$tag" \
  -o bin/tools-programmer ./cmd/tools-programmer
bin/tools-programmer --version
```

Expect `data.version` to be `0.2.0`, `build` to be `release`, `state` to be
`clean`, and `commit` to match the tag's full commit. A missing/mismatched stamp,
dirty source, or unknown provenance falls back to development. The stamp asserts
that the builder verified the tag; the binary cannot prove publication or authenticity.
For reproducible binaries, keep the exact source, Go toolchain, target GOOS/GOARCH,
dependencies, build flags, and environment identical; use the pinned `go.sum` and
`-trimpath`. No build timestamp is injected. Check reproducibility by comparing
SHA-256 hashes of two builds with those same inputs.

### Source archives

Archives and `-buildvcs=false` builds default to unknown provenance. A trusted
builder can supply `-X tools-programmer/internal/core.ArchiveRevision=FULL_COMMIT`
and `-X tools-programmer/internal/core.ArchiveState=clean` in `-ldflags` after
verifying that the archive exactly matches that commit. Add the matching
`ReleaseTag` stamp only for a verified release tag. Use `dirty` for modified source;
omit unverifiable assertions. Revisions must be full lowercase 40-character Git
SHAs; invalid values become `unknown`. Archive assertions never override embedded
VCS metadata. They are not cryptographic attestations, and suppressing VCS data
also suppresses automatic dirty detection: prefer checkout builds when possible.

## Project identities and pre-commit check

Supply the user-approved GitHub login and Git commit name/email explicitly at registration. Nothing is inferred from the active account, Git configuration, or review declarations. The GitHub login is compared case-insensitively; Git author and committer names/emails must match exactly. Repository and GitHub login bindings cannot be replaced. The single active expected Git tuple can be deliberately updated as described below; no identity allowlist is introduced. The tool never switches accounts or changes Git configuration.

The check opens an existing v7 database read-only: it never creates or migrates state. From the intended checkout, immediately before an authorized ordinary commit:

```sh
tools-programmer project check --project demo
```

This local check never invokes gh. It reads `git var GIT_AUTHOR_IDENT` and `GIT_COMMITTER_IDENT`, including environment and effective configuration overrides, rather than checking `user.name`/`user.email` alone. It requires a working tree with a single local `origin` URL matching the registered repository or its explicitly associated origin; supported forms are HTTPS, `ssh://git@host/owner/repo.git`, and `git@host:owner/repo.git`. Custom ports, unregistered aliases, URL credentials, escapes, queries, fragments and remote helpers are not accepted. If registration has a path, its resolved directory must equal the checkout root. Subdirectories are supported, and changing a worktree path requires `project rebind-checkout`. Git repository-redirection environment variables are refused. Only the current directory is used for check execution, never a stored project path. Git subprocesses are bounded to 10 seconds and 64 KiB per output stream; stderr is suppressed.

### Deliberately update an existing expected Git tuple

After explicit user approval of the exact project's new name/email, run this native operation in the **actual committing environment**. Supply the intended checkout explicitly, even when its path differs from the registered mapping:

```sh
tools-programmer project update-git-identity --project demo \
  --git-name 'New User' --git-email new@example.com \
  --path /absolute/path/to/committing/checkout --authorized
# Only if a path move is separately authorized:
tools-programmer project rebind-checkout --project demo --path /absolute/path/to/committing/checkout
# From that checkout, in the same environment as the intended commit:
tools-programmer project check --project demo
```

`--authorized` records the native caller's explicit authorization declaration; it is not proof of human presence and does not identify or invent a human actor. Never infer approval from configuration or an MCP declaration. Obtain consent before passing it. Both effective author **and** committer, including environment overrides, must already match the proposed tuple. The command does not configure Git for you.

| Outcome | Guarantee |
|---|---|
| `changed` | Only expected Git name/email change. Project name, creation time, unique origin, canonical repository/host, GitHub login, other policy and checkout mapping stay unchanged. All issue/activity/review/observation/plan/intent/confirmation history remains intact. |
| `no_op` | The already-active tuple still passes native validation. The returned receipt has its own ID, project, UTC time, authorization provenance, validation outcome and unchanged revision, with no change audit ID. Retain the receipt if needed; it is not inserted into history. No policy rewrite, duplicate change entry or plan invalidation occurs. |
| Rejected | Missing consent, invalid tuples, wrong origin, either effective identity mismatch or changed checkout evidence fail without policy/change-audit mutation. A full-policy comparison rejects stale validation, including a concurrent path change. An audit/storage failure rolls back the entire tuple change. An ordinary writable open may still perform an additive schema migration before validation. |

Origin, canonical root and both effective identities are inspected twice, with local revalidation immediately before the atomic full-policy comparison/write. Successful changes append exactly one private `identity_changes` entry: project, prior/new tuple, `native_cli_explicit_flag` provenance, UTC time and successful validation result. Raw origin/identity diagnostics and checkout paths are omitted from update receipts and errors; Git stderr is suppressed. No network I/O occurs inside the transaction.

Exactly one tuple is active. Repeated A -> B -> A changes are supported. Every real change advances `identity_revision`, permanently staling prior immutable approval plans even when A becomes active again. Create a new plan and obtain fresh apply authorization. A stale plan cannot acquire a new remote-write intent after the change commits, even when Apply passed its initial check earlier. Already-acquired intents, sent/unknown writes and confirmations are not cancelled or reset; read-only reconciliation stays available under the unchanged repository/login. Unresolved operations continue to block new mutations.

MCP `project_update_git_identity` takes explicit `project`, `git_name`, `git_email`, absolute `path` and `authorized`. It is **read-only**: missing consent is refused; a well-formed authorized declaration returns `native_validation_required` with this native CLI continuation, never a policy update. The server does not invoke Git, use its own identity as committing evidence, or accept a declaration as proof of human consent. Updating identity never rebinds a path and never replaces the ordinary pre-commit check. Subsequent environment/configuration changes still require a fresh check.

### Move a registered checkout

```sh
tools-programmer project rebind-checkout --project demo --path /absolute/path/to/new/checkout
tools-programmer project list
```

The explicit target may be a symlink or subdirectory of a non-bare working tree, including a linked worktree; the stored path is its canonical Git root. A pathless registration can bind this way. The command inspects Git **outside** the database transaction with subprocesses rooted at the target (not the current directory), checks its local origin against the canonical repository or registered alias, and verifies the effective author and committer in the current environment. It makes no Git/configuration changes, network calls or GitHub writes. Only a successfully changed path appends an audit event (`checkout_rebindings`: old/new path, UTC timestamp, result); rebinding to the same canonical root is a no-op. A concurrent policy/path change fails instead of overriding it. Project creation time and all review, issue, activity, plan and operation history remain unchanged. Existing plans refer to the old project policy and cannot be applied after a rebind; create a fresh plan. Unresolved operations still require read-only reconciliation. The new path takes over activity matching without rewriting earlier events. This is a local syntactic identity check, not network attestation or proof of checkout contents.

### Explicit origin aliases

With explicit authorization, associate an alias with an **existing, identity-bound** project:

```sh
tools-programmer project origin --project demo --repo github.com/example/demo \
  --origin-url 'git@github-P.com:Example/Demo.git'
tools-programmer project list
tools-programmer project check --project demo
```

`repository` remains the immutable canonical identity used by gh. `origin_url` preserves the supplied credential-free URL. New registrations and migrated rows default to `https://HOST/OWNER/REPO`, so canonical and associated hosts are identical until explicitly changed. The command confirms the canonical repository and replaces only this project's single association; repeating it is safe. Pass the canonical HTTPS URL to remove alias acceptance. A parsed remote already owned by another project's canonical identity or association is rejected, including equivalent URL spellings. Registering a new project cannot claim an existing association either.

Checks compare parsed, case-normalized host/owner/repository identities, accepting equivalent supported transport spellings and optional `.git`; they never infer aliases from host prefixes. The owner and repository must remain unchanged. Canonical origins continue to match, while the replaced alias stops matching. Unmapped projects remain strict. An explicit association is a **trusted declaration, not network attestation**: no SSH configuration, `ssh -G`, DNS, credentials or network endpoint is inspected. This does not rebind GitHub login, author, committer or path, erase history, or change Git origin. Worktree support is unchanged and separate. MCP exposes the stored association in project listings but adds no mutation tool or permission grant.

**This is an advisory pre-commit gate, not Git enforcement.** Direct `git commit` bypasses it. Run it in the same shell/environment as the commit; later environment/config changes, `git -c`, `--author`, amend/reuse/cherry-pick author preservation, hooks and other commit-time changes can invalidate the result. It does not validate the identities of an already-created commit. No hooks or global enforcement are installed. Stop on failure; do not auto-switch accounts or rewrite configuration. OpenCode guidance requires this check before commits, but cannot enforce arbitrary external Git invocations.

### Existing databases and safe binding

Schema v2 adds three identity columns transactionally, with empty defaults. Existing projects remain **unbound**; local history stays readable/recordable, but remote operations and identity checks fail closed. Existing snapshots, review events, plans, intents, unknown outcomes and confirmations are preserved without rewriting payloads. Older v1 binaries refuse v2 databases; no downgrade is provided. Do not restore stale state or drop identity columns to bypass a guard.

Schema v3 adds issue/estimation/activity tables and append-only guards. Ordinary writable open migrates v1/v2 additively; existing approval operations, including unknown outcomes, are untouched. No reset command or automatic deletion is introduced. Older binaries reject the newer schema; do not downgrade the database to make them run.

Schema v4 adds `issues.url` with an empty-string default. Existing issue IDs, activity references and append-only guards are preserved. Missing URLs remain `"url":""` in registration and summary output; they are not inferred or backfilled. Upgrade explicitly through an ordinary CLI command before launching MCP; MCP does not migrate state.

Schema v5 adds `projects.origin_url` and initializes it from the canonical repository, without granting alias acceptance. All existing identity and historical rows remain intact. An explicitly authorized ordinary CLI operation (including `project origin` or `project list`) migrates older state; read-only checks and native/MCP access never migrate. Older binaries reject newer schemas, so upgrade consumers before using migrated state; do not downgrade or replace the database.

Schema v6 adds the append-only `checkout_rebindings` audit table. Ordinary writable open migrates v5 transactionally; an attempted rebind can leave this migration in place even when subsequent target validation fails, but it cannot change the project path or append an audit event on validation failure. Existing histories remain intact; older binaries reject newer schemas.

Schema v7 adds append-only `identity_changes` and its project index, without rewriting project or historical rows. The durable per-project change count supplies `identity_revision`; the active tuple remains solely in `projects`. Pre-v7 bound plans omit the revision and decode to zero: they remain usable when their full policy is unchanged and no identity change has occurred, then become permanently stale after a real change. Unbound legacy plans still fail closed. Read-only checks and MCP require v7 and never migrate. Upgrade consumers before an authorized ordinary writable CLI open (for example `project list`) migrates existing state. Older binaries reject v7; never downgrade or restore stale state to revive plans or erase intents.

After explicit user approval of the identities, confirm the existing repository when binding:

```sh
tools-programmer project bind --project demo --repo example/demo \
  --github-login expected-login --git-name 'Expected User' --git-email expected@example.com
```

Binding works once, only for a wholly unbound project. If unresolved intents exist, the expected login must match every unresolved actor; conflicts remain blocked for investigation, never reset. Legacy approval plans remain non-executable even after binding: create a fresh plan and obtain fresh authorization. Legacy unresolved intents can still be reconciled under the explicit current binding; the original plan and intent are retained. Do not remove state to enable rebinding or work around an unknown outcome.

After actually reviewing the exact head and collecting evidence:

```sh
# Replace HEAD_SHA with the full lowercase 40-character SHA actually reviewed.
bin/tools-programmer review record --project demo --pr 8 --sha HEAD_SHA \
  --reviewer reviewer-name --method manual \
  --outcome approved --summary 'Reviewed behavior and tests' \
  --evidence 'review-notes.md; test-run-reference'
bin/tools-programmer review list --project demo --pr 8
```

Outcomes are `approved`, `changes_requested`, or `incomplete`. Recording appends an event; it never replaces old history. The latest event for the project/PR/exact SHA controls local eligibility, regardless of reviewer. UTC recording time is generated by the tool, not backdated. Reviewer, method, summary and evidence are user-supplied text: **never put credentials or sensitive raw output in them**. Evidence references are not opened or uploaded.

Freshness is `unobserved`, `stale`, or `current-at-last-observation`. “Current” always means relative to the last successful sync/inspection timestamp, not a live guarantee. A new head needs a new technical review; an old approval never transfers to it.

## Explicit approval workflow

```sh
# Read-only on GitHub; persists a local plan and refreshed observations.
bin/tools-programmer approval plan --project demo --prs 8,9
bin/tools-programmer approval show --id PLAN_ID

# Only after the user explicitly authorizes applying this inspected plan:
bin/tools-programmer approval apply --id PLAN_ID

# If an attempted write is ambiguous, read-only recovery:
bin/tools-programmer approval reconcile --id PLAN_ID
```

PR lists must contain 1–50 explicit, unique positive numbers. No command scans every project or implicitly approves all open PRs. Planning succeeds with a persisted blocked plan when inspection succeeds but eligibility fails: inspect every item's `eligible` and `reasons`. An inspection/API failure is a nonzero error, not a partially usable plan. Sync may retain earlier successful observations if a later PR fails.

Apply checks the entire batch before its first write, then inspects each PR again before its own write. It refuses a changed head, base SHA/ref, author, or authenticated actor; closed/draft PRs; missing write/maintain/admin capability; self-authored PRs; pending/unknown reviews; unresolved changes requests; or a missing/revoked approved local verdict. A planned existing approval that disappears cannot silently become a new write.

The actor comes from `/user` for the selected host using existing gh configuration and must match the project's explicit expected login. The adapter checks before each repository request, including pagination, the final approval POST and readback/reconciliation. Fresh apply checks also enforce the binding independently of the actor stored in a plan. A mismatch stops requests; if intent was already claimed or a POST may have occurred, it remains unresolved rather than being erased. The tool does not collect/store tokens or switch accounts. Token-override environment variables are removed before invoking gh, so configure authentication through gh itself. Review permission is conservatively inferred from repository `push`, `maintain` or `admin` capabilities, but GitHub can still reject the action. **There is no self-approval workaround.** Account switching during a command is unsupported; a separate `/user` check and subsequent request cannot eliminate that race atomically.

### Observed-check policy

- Inspect all paginated head-commit check runs (`filter=all`) and commit statuses, not fixed workflow names.
- For runs, select the greatest run ID per name/application. For statuses, select the greatest status ID per context. An older success cannot hide a newer pending/failure; different apps remain distinct.
- Require every selected run to be `completed/success` and every selected status to be `success`. Pending, failed, neutral, skipped, unknown and malformed results block.
- No checks blocks by default. A repository without CI may be registered with the explicit project policy `project add ... --allow-no-checks`. This does **not** ignore failing checks and cannot be changed on an existing project in this MVP.

This is a conservative **observed-check policy**, not proof that branch protection, rulesets, required checks not yet created, review threads, merge queues, deployments, or merge requirements are satisfied. The tool never merges, labels, marks ready, or transitions drafts.

### Durable intent, confirmation and unknown outcomes

Before the single `POST .../pulls/N/reviews`, SQLite atomically claims an intent and rechecks the latest local verdict. The body includes a random stable operation marker, and the request always includes the exact `commit_id` and `event: APPROVE`. A uniqueness constraint spans plans for project/PR/SHA/actor. No SQLite transaction spans a network call.

Only a matching response **and separate authoritative readback** of review ID, actor, `APPROVED` state and commit create a `github-confirmed` record. Local verdicts, attempt states (`intent`, `unknown`, `confirmed`) and GitHub review states are separate. Existing remote approvals are reported in observations/plans and skipped; they are not invented as locally performed operations. Confirmations are historical facts, not a promise that an approval has not since been dismissed.

Any POST error, mismatched response, failed readback, interrupted process, or persistence uncertainty may mean a write occurred. The batch stops. An unresolved intent anywhere in the database blocks **all** further mutation claims, including different projects or newer heads. Concurrent apply cannot claim while another intent is unresolved. Repeated confirmed actions never publish again for that identity, even after dismissal; re-approval of a dismissed same-head review is outside this MVP.

Reconciliation only reads the known review ID, or finds exactly one review matching the operation's stable body marker and identity. It never writes to GitHub. An absent/edited marker, dismissed/mismatched review, or inaccessible readback leaves the operation blocked. A crash before the POST can therefore require manual investigation indefinitely: **there is intentionally no reset, force, or blind retry command**. Keep the database and operation marker intact.

GitHub offers no atomic conditional review-create transaction covering head, base, actor, CI and permissions together. Changes after final inspection remain possible; explicit `commit_id` prevents silently approving a different head. Do not interpret a successful action as a merge-safety guarantee. Do not change gh authentication during execution.

## Database and output

Path priority:

1. `--db PATH` **before** the command.
2. `TOOLS_PROGRAMMER_DB`.
3. `$XDG_STATE_HOME/tools-programmer/history.db`, or `~/.local/state/tools-programmer/history.db`.

Example isolated state: `tools-programmer --db /private/directory/history.db project list`. Relative file paths are accepted, SQLite URI/DSN input is not interpreted. Use an application-owned **0700** directory; the database is created **0600**. Existing public files/directories and immediate symlink targets are refused, not silently chmodded. Use trusted local filesystems and ancestors; this is not a defense against a compromised local account or hand-edited database.

Schema version 7 uses `PRAGMA user_version`, foreign keys, a 5-second busy timeout and `synchronous=FULL`. Initialization and additive migration are serialized with `BEGIN IMMEDIATE`; a newer schema fails without resetting data. Opening state may initialize/migrate the database even for a local read command, **except `project check`, `issue summary` and `mcp serve`, which require existing v7 state**. The first two open read-only; MCP opens existing state read-write solely to support local issue registration and opt-in declarations, without creating or migrating it. Existing review/approval tables remain separate from issue histories and normalized activity events. The default SQLite rollback journal is used, not WAL. Database, journal/WAL/SHM and build outputs are ignored by Git. Remote review bodies are transient and never persisted; only the generated operation marker can be reconstructed locally.

**Backup:** stop all processes accessing the database, then copy the closed database file into a private directory with mode 0600. Do not copy a live file without SQLite's backup API, or remove a hot journal/WAL. Restoring an old backup loses newer intent history; do not apply its old plans as if it were current. Inspect live remote state and resolve discrepancies first. Never delete state to bypass an unknown action.

CLI command results are JSON envelopes: `{"data": ...}` or `{"data": ..., "error":{"code":"...","message":"..."}}`. Action errors include persisted partial results where readable. Exit status is 0 on command success and 1 on errors; a successfully created ineligible plan still exits 0. Help is plain text; `--version` is JSON. Help/version do not create default state. `mcp serve` instead reserves stdout for MCP JSON-RPC and sends startup diagnostics to stderr. Raw gh stderr and database internals are not printed. GitHub subprocesses are limited to 30 seconds and 2 MiB each of stdout/stderr; pagination stops after 100 pages per endpoint and fails closed if incomplete.

## Local-history MCP for agents

Use the same executable and existing database; there is no separate server package to install:

```sh
# Explicit registration only; this example does not initialize a database.
tools-programmer --db /private/directory/history.db mcp install opencode
# OpenCode launches this command when it connects:
tools-programmer --db /private/directory/history.db mcp serve
```

**Quit and restart OpenCode after installation.** By default, the MCP server exposes only:

| Tool | Input | Stored result under `data` |
|---|---|---|
| `project_list` | `{}` | Registered projects and declared identities, not access certification |
| `project_update_git_identity` | `{"project":"demo","git_name":"New User","git_email":"new@example.com","path":"/absolute/checkout","authorized":true}` | Read-only refusal with native validation continuation; no policy mutation or server Git inspection |
| `pr_list` | `{"project":"demo"}` | Last stored PR snapshots and observation timestamps |
| `review_list` | `{"project":"demo","pr":8}` (`pr` optional) | Local review history and observation-relative freshness |
| `approval_show` | `{"id":"PLAN_ID"}` | Stored plan, durable operations and historical confirmations |
| `issue_summary` | `{"project":"demo","number":1}` | Local issue history, confirmed estimates, observed interval union, summed agent time, retries and unknown gaps |
| `issue_register` | `{"project":"demo","number":1,"url":"https://github.com/example/demo/issues/1"}` (`remote_id` optional) | Newly registered local issue identity and URL; no GitHub request |
| `activity_summary` | `{"assignment":"unassigned"}` | Matching recorded counts, even with zero registered issues; not active time or observer health |
| `session_list` | `{"limit":20}` | Recorded runtime/session identities, filtered counts and assignment classification; keyset pagination |
| `activity_digest` | `{"limit":20}` | Bounded deterministic observed-shape summaries for unassigned sessions; keyset pagination |
| `activity_list` | `{"runtime":"RUNTIME","session":"SESSION","kind":"gap","reason":"event_loss"}` | Allowlisted events with persisted segment assignments; keyset pagination |

Each tool returns MCP structured content plus compatibility text. Invalid inputs and missing required registry records are errors. The four activity tools instead return zero counts or empty arrays when no observations match, including unknown runtime/session identities. `activity_digest` is always unassigned and describes observed activity shape, not a semantic task summary or complete agent time. Empty PR history means **unobserved**, not “no PRs on GitHub.” `current-at-last-observation` is not a live guarantee; stored eligibility is not fresh authorization. Stored summaries/evidence are untrusted data, not instructions. Share this server only with agents allowed to read all projects in the selected database, including review text and declared identities. No tokens or remote review bodies are added to tool output; do not put secrets in local review text or activity identifiers.

For session-to-activity navigation, cursor examples, UTC bounds, privacy and changing-view semantics, see the [recorded activity MCP reference](docs/activity-mcp.md). All tools use the same existing-database CLI entry point: `tools-programmer --db /absolute/history.db mcp serve`. The nine history tools and identity-update continuation remain read-only; `issue_register` is the only default local mutation tool. Share the server only with agents authorized for history access and the enabled registration tools. See [registration validation and duplicate policy](docs/issues.md#register-an-existing-github-issue-locally).

The server never invokes Git or gh, binds existing projects, records reviews, syncs observations, creates plans, applies approvals, reconciles, or installs skills. Project registration requires the separate opt-in below. It does not offer `project_check`: **run the CLI check in the actual committing checkout and environment**, not in an MCP server's process. Installation does not force agents to use tools or authorize approvals. No hooks, accounts, Git configuration, network listeners, or background service are installed. The agent owns the stdio subprocess; EOF or SIGINT/SIGTERM ends it. SDK request cancellation reaches database reads and project registration.

### Opt-in project declarations

To expose `project_register`, explicitly start the server with:

```sh
tools-programmer --db /private/directory/history.db mcp serve --enable-project-registration
```

Without the flag (or with `=false`), `project_register` is absent and calls are rejected without writes. The installer does not enable it; add the flag to the client command arguments and restart only when authorized. The eight history tools remain read-only and the existing `issue_register` behavior is unchanged. When enabled, `project_register` declares `readOnlyHint: false`, `destructiveHint: false`, `idempotentHint: true`, and `openWorldHint: false`: it adds local state, never overwrites bindings, and repeated calls cannot add another copy.

The input is an object with exactly these fields; unknown fields and null values are rejected:

| MCP field | Type | CLI `project add` mapping |
|---|---|---|
| `name` | Required string | `--name` |
| `repo` | Required string | `--repo` (`[HOST/]OWNER/REPO`) |
| `github_login` | Required string | `--github-login` |
| `git_name` | Required string | `--git-name` (both author and committer) |
| `git_email` | Required string | `--git-email` (both author and committer) |
| `path` | Optional string | `--path`; omitted/empty stays unbound |
| `allow_no_checks` | Optional boolean | `--allow-no-checks`; defaults to false |

Example: `{"name":"demo","repo":"Example/Demo","github_login":"reviewer","git_name":"Reviewer","git_email":"reviewer@example.com"}`. Success returns the existing `{"data": <project>}` envelope in structured content and compatibility text, including canonical `repository`, declared identities, policy and `created_at`. Read it back through `project_list` or CLI `project list` using the same database.

Validation and normalization are shared with the CLI: repositories are lowercased with `github.com` as the default host; supplied paths become absolute relative to the **server working directory**, without resolving symlinks or checking checkout existence. Prefer absolute paths. No identity, checkout or policy is inferred from environment, configuration, accounts or credentials.

Registration is a declaration, **not ownership, repository access or committing identity certification**. Run CLI `project check` separately in the actual committing environment. The tool uses only the selected existing registry; it never initializes/migrates a database, creates repositories, assigns sessions, backfills issues/activity, or changes review/approval history. Duplicate names or canonical repositories return `project_conflict` without overwriting bindings, including competing calls. Validation errors retain shared codes (`invalid_input`, `identity_unbound`); storage failures are sanitized as `storage_error`. Cancellation before persistence prevents writes; cancellation before transaction commit rolls back. A successfully committed declaration is not undone by later transport cancellation: use `project_list` to resolve an uncertain response, not a blind retry.

### Opt-in issue estimate proposals

To expose `issue_estimate_propose`, explicitly start the server with:

```sh
tools-programmer --db /private/directory/history.db mcp serve --enable-issue-estimates
```

Without the flag (or with `=false`), the tool is absent and calls are rejected without writes. The installer does not enable it. The tool accepts `project`, `number`, `kind` (`initial`, `revision`, or `final`), Fibonacci `score` (`1`, `2`, `3`, `5`, `8`, `13`, or `21`), `proposer`, and `rationale`. It resolves the registered local issue, returns the persisted proposal including its local `id`, and appends history without overwriting earlier proposals. It never confirms an estimate; confirmation remains a separate explicit human action and follows the existing initial-baseline sequence rules.

### Safe OpenCode registration

For the complete setup, `sync opencode` runs `skill sync opencode`,
`integration sync opencode`, and `mcp install opencode` in that order. It
resolves and validates the database path before the first write, reports each
component result, and returns `failed_step` with a nonzero exit status when a
later component fails. It reuses each component's fail-closed conflict and
atomic-write checks, preserves unrelated configuration, and is safe to repeat.
It does not modify `AGENTS.md`, install dependencies, or install or replace the
executable. The individual commands remain available when a component needs to
be repaired separately.

`mcp install opencode` uses the resolved absolute path of its own executable (not PATH). It pins the selected database as an absolute `--db` argument, including when selected through `TOOLS_PROGRAMMER_DB` or the default location. Relative paths are resolved at installation time, so future client working directories cannot select another database. Later environment changes do not redirect this registration. Keep the binary at that location; after moving it or changing databases, inspect and update the registration manually.

The installer prefers existing `opencode.jsonc`, then existing `opencode.json`; if neither exists, it creates `opencode.jsonc` unless a legacy-only `config.json` requires manual configuration. This follows OpenCode's global filename priority. The directory is `OPENCODE_CONFIG_DIR` when set; otherwise `$XDG_CONFIG_HOME/opencode`, falling back to `$HOME/.config/opencode`. Overrides must be absolute. It never opens a database or launches OpenCode/gh. Unsupported targets and malformed command arguments fail before filesystem changes.

- JSON and JSONC (line/block comments and trailing commas) are supported. A surgical insertion changes only `mcp.tools-programmer`, adding its parent object if absent. All original bytes, including comments, whitespace, field order, permission-rule order and numeric literals, are preserved around the insertion. No schema field is added. Existing file permissions are preserved; new files are 0600. An identical registration is a byte-for-byte no-op.
- A different `mcp.tools-programmer` entry is a conflict, including custom arguments, environment, remote configuration, or a changed binary/database path. There is no force overwrite.
- When both filenames exist, only `opencode.jsonc` is edited; `opencode.json` and any legacy `config.json` remain untouched. Lower-priority files are never migrated, consolidated or deleted. A legacy-only `config.json`, malformed JSON/JSONC, duplicate keys (including escaped equivalents) and non-object `mcp` values require manual configuration. No shadow file is created on refusal. Errors return the proposed command for manual use without echoing existing configuration values.
- Symlinks at any destination component (including HOME/ancestors and dangling links) and incompatible file types are refused. Replacement uses a temporary file in the pinned parent directory followed by rename. Use a trusted filesystem without concurrent configuration/tree edits; this is not a hostile-local-account defense or a crash-durability guarantee for the directory entry.

OpenCode can also merge project configs, `OPENCODE_CONFIG`, and `OPENCODE_CONFIG_CONTENT`; the installer does not edit or certify those layers. They may override the global entry. MCP configuration and the separately installed skill have independent paths and lifecycles.

### Other agents and manual configuration

Only OpenCode has an automatic installer today. Any MCP client supporting stdio can launch the same absolute executable with arguments `--db /absolute/history.db mcp serve`; use that client's documented configuration format. Future adapters can implement registration without changing the server's authority. For OpenCode manual configuration, merge this entry into the active file rather than replacing the whole file:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "tools-programmer": {
      "type": "local",
      "command": ["/absolute/tools-programmer", "--db", "/private/directory/history.db", "mcp", "serve"],
      "enabled": true
    }
  }
}
```

Paths containing OpenCode `{env:...}` or `{file:...}` interpolation expressions are refused by the installer. No credentials or environment block are generated. The local entry shape is verified against the published [OpenCode schema](https://opencode.ai/config.json) and [MCP configuration documentation](https://opencode.ai/docs/mcp-servers/). Protocol negotiation, validation and transport lifecycle use the pinned official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) v1.7.0 (requires Go 1.25+, compatible with this module's Go 1.26).

## OpenCode skill

Project skill: [`.opencode/skills/tools-programmer/SKILL.md`](.opencode/skills/tools-programmer/SKILL.md), registered in `AGENTS.md`. It teaches recording actual technical reviews and requiring explicit user authorization before applying approval plans. Command details are portable local references.

Install or update the bundled skill explicitly:

```sh
tools-programmer skill sync opencode
```

This writes `SKILL.md` and `references/cli.md` to `$HOME/.config/opencode/skills/tools-programmer/`. Only `opencode` is supported. The executable embeds the authoritative project files at build time: no checkout, current working directory, database, project registration, gh, or network is needed. Rebuild the binary to distribute newer skill content; no prebuild generation step is required.

**Sync overwrites local edits to those two files.** Back up customizations first. Identical files are left untouched; unknown files, extra references and other skills are preserved. JSON `updated` and `unchanged` lists report each owned file. Existing symlinks in HOME, its ancestors, destination parents or owned files, and incompatible file types are refused. Use a trusted directory tree without concurrent external edits. Replacement uses a temporary file and rename per file, **not a multi-file transaction**; an interrupted/failed sync may leave a partial update. Rerun sync to finish it. It does not promise crash-durable filesystem writes.

Make `tools-programmer` available on **PATH**, then **quit and restart OpenCode** to load the skill. Sync does not install the executable, register projects, change accounts or Git/OpenCode configuration, or install hooks/enforcement. Global guidance is advisory; explicit identities and approval authorization remain required. Building/testing this project does not install the skill in your real HOME.

The old ad-hoc Python approver is not the reusable interface. Use this CLI and its durable history instead; no issue-specific Python copy or execution path is bundled.

## Verify and review the implementation

```sh
gofmt -w skill.go cmd internal
go test ./...
go vet ./...
go test -race ./...
CGO_ENABLED=0 go build -trimpath -o bin/tools-programmer ./cmd/tools-programmer
TOOLS_PROGRAMMER_TEST_BINARY="$PWD/bin/tools-programmer" \
  go test ./internal/cli -run TestBinarySmokeWithFakeGitHub -count=1 -v
TOOLS_PROGRAMMER_TEST_BINARY="$PWD/bin/tools-programmer" \
  go test ./internal/cli -run TestBinaryMCPInstalledCommandAndLifecycle -count=1 -v
```

Tests use real temporary SQLite files and mocked adapters/subprocesses. The binary smoke shadows `gh` with a fixture-only helper, exercises registration → review → sync → plan → actor-mismatch rejection → apply → readback → repeated apply → reconciliation, and forbids duplicate fake POSTs. Additional tests cover lossless v1 migration, explicit legacy binding, fresh actor changes, recovery after post-write actor mismatch, and effective author/committer overrides using fake Git and read-only real Git against temporary fixtures. MCP tests negotiate through SDK in-memory transports and a built binary launched from generated config in another working directory, verify read-only history, cancellation, clean EOF/signal shutdown, and missing/incompatible-state refusal. Installer tests use temporary HOME/config directories, never the real global configuration. Tests do not use real GitHub, credentials, Docker or network calls. Public module downloads may be needed. `go test -short ./...` skips the marked binary, Git and identity-recovery subprocess integration tests.

Code map:

| Package | Responsibility |
|---|---|
| `internal/core` | Data types, identifier validation, conservative eligibility |
| `internal/store` | Private SQLite storage, schema, append-only history, atomic claims |
| `internal/github` | Bounded gh argument-array adapter and pagination |
| `internal/gitidentity` | Current-checkout association and effective Git identity checks |
| `internal/service` | Plan, preflight, mutation confirmation and reconciliation |
| `internal/cli` | Standard flag parsing and JSON envelopes |
| `internal/skillsync` | Conservative, per-file sync of the embedded OpenCode skill |
| `internal/mcpserver` | Eight read-only history tools, local issue registration and opt-in project declarations over the official MCP SDK |
| `internal/mcpinstall` | Explicit agent registration adapters; initially OpenCode only |
| Root package | Embeds authoritative project skill files without generated copies |
| `cmd/tools-programmer` | Signal-aware executable entrypoint |

Independent review units are policy/storage with their tests, remote/action handling with adapter/service tests, and CLI/skill/docs with the binary smoke. They remain uncommitted. There is no legacy application dependency, telemetry, merge automation, or auto-LLM review engine. Rollback is removal of this independent project and its chosen test state only; never remove an operational database containing unresolved writes.
