# Review recording and approval commands

Use `tools-programmer` on PATH, or the executable path supplied by the user. Check `--version` and `--help`. Put optional `--db PATH` before the command; otherwise `TOOLS_PROGRAMMER_DB` or the application's private state directory is used. Do not invent another database to bypass an unknown operation.

## Local review history

```sh
tools-programmer project add --name demo --repo example/demo \
  --github-login expected-login --git-name 'Expected User' --git-email expected@example.com
tools-programmer project list
tools-programmer pr sync --project demo --prs 8
tools-programmer pr list --project demo
tools-programmer review record --project demo --pr 8 --sha FULL_REVIEWED_SHA \
  --reviewer reviewer-name --method manual --outcome approved \
  --summary 'Technical review summary' --evidence 'test-output-reference'
tools-programmer review list --project demo --pr 8
```

Replace placeholders with verified values. Outcomes: `approved`, `changes_requested`, `incomplete`. Reviewer/method describe actual work, not assumed completion. Local time is generated in UTC. Events remain append-only. The latest verdict for an exact project/PR/SHA controls eligibility. History marks freshness relative to the last remote observation, not the current wall-clock time. Local listing/recording never needs gh authentication.

## Explicit approval

Before an authorized commit, run `tools-programmer project check --project demo` from the intended checkout. It checks effective author and committer (including environment overrides), local origin association, and the registered path when present. It never executes in stored paths, contacts GitHub or changes configuration. Require success in the commit's actual environment. Direct Git commits bypass this advisory check; commit-time identity overrides and preserved authors require separate verification. Do not install hooks or global enforcement without explicit authorization.

Legacy projects remain unbound after additive migration. With user-approved values, bind once using `tools-programmer project bind --project demo --repo example/demo --github-login expected-login --git-name 'Expected User' --git-email expected@example.com`. Existing bindings cannot be replaced; unresolved intents must match the expected login. Old plans cannot be applied after binding, but their unresolved operations can be reconciled under that explicit binding. Keep all history and create new plans for new approvals.

```sh
tools-programmer approval plan --project demo --prs 8,9
tools-programmer approval show --id PLAN_ID
# Execute only after explicit user authorization for this plan:
tools-programmer approval apply --id PLAN_ID
```

Planning writes only local data. Inspect every item's `eligible`, `already_approved`, `reasons`, actor, repository, head, base and observation time. A persisted blocked plan can exit successfully: JSON eligibility, not exit status alone, decides whether approval is proposed. Never treat observed successful CI as technical review evidence by itself.

Remote commands compare the target host's `/user` actor against the registered expected login before every repository request, including final POST, readback and reconciliation. Token environment overrides are ignored. Write/maintain/admin capability is required; self-authored PRs are blocked. No automatic account switching. Keep authentication stable during the command; authentication checks and later requests are not atomic. Apply revalidates head/base/actor, state, reviews, checks and local verdict before exact-commit `APPROVE` requests. A late mismatch leaves any claimed intent unresolved rather than discarding possible-write state.

Latest observed check run per name/app and latest commit status per context must succeed. Pending/failure/unknown/neutral/skipped checks block. No checks blocks unless the project was explicitly registered with `--allow-no-checks`; this never ignores failures. This policy does not prove branch protection or merge readiness.

## Ambiguous writes

```sh
tools-programmer approval show --id PLAN_ID
tools-programmer approval reconcile --id PLAN_ID
```

Reconciliation is read-only. A durable `intent` or `unknown` blocks all new mutation claims in that database. It may represent a published review even after timeout or process crash. Confirmation requires matching review ID, actor, `APPROVED` state and exact commit; unknown-ID recovery additionally requires the unique generated operation marker. Missing evidence stays blocked. Never delete the database, manually reset the state, force/retry the POST, or claim success without confirmation.

Confirmed records are historical; later dismissal does not erase them. The tool does not duplicate already-observed approvals or re-publish a confirmed same-head operation. There is no merge, label, draft-transition or ready-for-merge operation. The old ad-hoc Python approver is not the supported reusable interface.
