---
name: tools-programmer
description: "Trigger: before commits, project identity check, PR review history, record review, approve PR. Check identities and record exact-SHA reviews and approvals."
license: Apache-2.0
metadata:
  author: jemanuelp
  version: "1.1"
---

## Activation Contract

Use before commits, for project identity checks, technical review recording, review history, or explicit GitHub PR approval through `tools-programmer`.

## Hard Rules

- Record only completed technical work against the exact full reviewed head SHA. Never infer review completion from green CI.
- Keep local verdicts distinct from GitHub approvals and mutation attempt states.
- Require explicit user authorization for the specific stored plan before `approval apply`.
- Never switch accounts to bypass self-approval; never merge, label, or transition drafts.
- Stop on unknown outcomes. Reconcile read-only; never reset state or blindly repeat a write.
- Keep credentials and raw sensitive output out of summaries/evidence.
- Before an authorized commit, run `tools-programmer project check --project NAME` in the intended checkout with the same environment. Require matching effective author and committer; stop on any failure. Verify preserved/overridden commit authors separately.
- Use only user-approved expected identities. Bind legacy projects explicitly; never infer an identity, replace state or change Git configuration to pass a guard. Direct Git commits bypass the advisory check; do not install hooks or global enforcement without authorization.

## Decision Gates

| Evidence | Action |
|---|---|
| Incomplete technical review | Record `incomplete`; do not propose approval. |
| Head changed | Review the new SHA; retain stale history. |
| Plan blocked or actor/base changed | Explain reasons; obtain fresh evidence and a new plan. |
| Already approved | Report the observation; do not duplicate it. |
| Unbound project or identity mismatch | Stop; require explicit legacy binding or user investigation. Never auto-switch. |

## Execution Steps

1. Read [references/cli.md](references/cli.md); select the registered project and explicit PR numbers.
2. Inspect the exact code/test evidence; record reviewer, method, verdict and reviewed SHA.
3. Create a read-only approval plan; show repository, actor, SHAs, observation dates and all eligibility reasons.
4. Obtain explicit authorization, then apply that plan. Inspect confirmed review IDs and partial attempt states.
5. On uncertainty, stop mutations and reconcile the owning plan read-only.

## Output Contract

Report project/PR/SHA, local verdict and freshness, plan ID, confirmed GitHub review IDs, unresolved operations and next safe action. Do not call a local verdict a published approval.

## References

- [references/cli.md](references/cli.md) — command syntax, check policy and recovery limits.
