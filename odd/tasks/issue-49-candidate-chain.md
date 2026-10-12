# Issue 49: publish the read-only V2 candidate chain
Objective: publish the isolated OpenCode 2.0.26 candidate, not full runtime acceptance of #49.
Baseline: `7ea50552e3becff4a58c55da8224c497bbd51d0d`; strategy: feature-branch-chain; checks: Go and offline SDK fixtures.

## Specs

- S1: "crea la cadena de PRs para el issue 49". Create a draft/no-merge tracker against main and eight child PRs. The first child targets the tracker; each later child targets its immediate predecessor. No merges or issue closure.
- S2: "Publicar candidato y permitir CI". Publish only the verified read-only 2.0.26 candidate; exclude private prototypes, generated binaries/databases/dependencies, and #42/#50 changes. Permit the existing automatic V1 laboratory CI on GitHub Actions, not local V2 host/model/laboratory execution.
- S3: The selected publication envelope authorizes up to 4,200 changed lines only for the package/lock/candidate type configuration/docs unit, including the 3,842-line generated lock. All other child PRs remain at or below 400 additions plus deletions. Do not compress or delete tests/docs to meet a budget.
- S4: "usa gh con el usuario jemanuelp"; "user=jemanuelp"; "email=jemanuelpachecoo@gmail.com". Use that authorized GitHub session for HTTPS publication and verify the existing Git author and committer in the registered checkout before each commit. Never infer technical approval from CI.

## Tasks

- T0 [S1-S4] inline; pending: draft tracker with this delivery record; commit pending.
- T1 [S1-S4] inline; pending: V1 version preflight with tests and CLI help; commit pending.
- T2 [S1-S4] inline; pending: locked 2.0.26 read-only plugin/package with offline fixtures and standalone candidate type configuration; commit pending; narrow size exception authorized.
- T3 [S1-S4] inline; pending: exact protocol-2 embedded bundle and integrity tests; commit pending.
- T4 [S1-S4] inline; pending: independent V2 destination resolution and tests; commit pending.
- T5 [S1-S4] inline; pending: owned V2 JSONC content and tests; commit pending.
- T6 [S1-S4] inline; pending: isolated V2 version admission and tests; commit pending.
- T7 [S1-S4] inline; pending: side-effect-free candidate preparation and tests; commit pending.
- T8 [S1-S4] inline; pending: explicit public V2 setup/info with regression tests and README; commit pending.

## Review boundaries

| Unit | Review first | Rollback / scope |
| --- | --- | --- |
| T1 | Version parser and rejection tests | V1 setup preflight only |
| T2 | Exact dependency pins and read-only tool fixture | Candidate source package only; generated lock has explicit exception |
| T3 | Eight-asset inventory and relocated imports | V2 embedding only; V1 bundle unchanged |
| T4 | Root separation and collision tests | V2 destination resolver only |
| T5 | Owned tuple validation and preservation tests | V2 configuration content only |
| T6 | Private HOME/XDG/cwd probe tests | V2 version-only admission only |
| T7 | No-side-effect preparation tests | V2 private planning only |
| T8 | Public rejection/idempotence/setup-order tests | Explicit V2 dispatch/publisher only |

The tracker is an integration placeholder, not a merge-ready implementation or an approval. Child tests use temporary state and fake processes/GitHub; no local OpenCode service, model, Docker laboratory or production helper may be launched. CI outcomes remain distinct from technical review. Build artifacts from the earlier dirty snapshot are not committed or uploaded with this chain.

## Log

- L1 (2026-10-12): "crea la cadena de PRs para el issue 49"
- L2 (2026-10-12): "Publicar candidato y permitir CI"
- L3 (2026-10-12): "usa gh con el usuario jemanuelp\n\nuser=jemanuelp\nemail=jemanuelpachecoo@gmail.com"
- L4: Verified `gh` actor `jemanuelp` has push permission in `jemanuelp/tools-programmer`. Native project check passed for the requested author and committer tuple without changing Git configuration. Clone-local receipt-driven review is off and remains off.
- L5: Read-only mapping confirmed the eight dependency-ordered units. Use a temporary Git index in the same registered checkout to construct exact candidate trees and commits; preserve the original branch, real index, dirty source bytes and private prototypes. No writer trigger applies; parent owns all Git actions. Fresh workers run bounded prefix verification, not implementation.
- L6: The candidate type configuration is standalone: extending the excluded private prototype configuration would make the published package non-self-contained. This adds no runtime behavior and changes no embedded bundle asset.
- L7: Earlier full-check evidence belongs to the uncommitted candidate snapshot. Each published prefix requires fresh checks of its own tree. Installed OpenCode loading, real-host coexistence, native permission helpers and full #49 acceptance remain pending and outside this delivery.
