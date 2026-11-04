# Windows probe integration review tracker

Related approved issue: #60. This is a partial delivery, not issue completion.
The V2 adapter and real-host evidence remain owned by #61 and #50/#62.

## Integration policy

Use a feature-branch chain. The first review unit targets this integration
branch; each later unit targets the immediate preceding unit. The integration
PR remains draft and no-merge while the chain is under review. No merge or
manual Windows workflow dispatch is authorized by the publication request.
Existing pull-request CI may run independently.

## Review units

Source baseline: `7ea50552e3becff4a58c55da8224c497bbd51d0d`.
Original local commits are retained unchanged for traceability:

| Unit | Original commit | Scope | Added + deleted |
| --- | --- | --- | ---: |
| 1 | `143f3cb16c4bfcf890a79ffa60744aad91ff6fa7` | Read-only component preflight | 182 |
| 2 | `e1e99595c4f7ca223824a2f978594fb684d3d874` | Bounded host probe and platform runners | 259 |
| 3 | `3f9a79c5fbe48ceff08ab2950d94c8941fc38b48` | Public sync dispatch and regressions | 400 |
| 4 | `d5523b5c40a75801ac98567d8d372d21a4dafeb7` | Native Windows fixtures and manual workflow | 310 |

Review branches preserve each original source patch over this coordination
document. Do not include uncommitted Linux late-I/O work or private ODD logs.

## Evidence boundaries

Each original unit passed local snapshot checks; the final source also passed
race/build/smoke and Windows cross-compilation. These are not hosted CI results
or native Windows runtime acceptance. The manual fake-host job is described in
`docs/windows-probe-workflow.md` once review unit 4 is included; it must not be
dispatched without separate authorization. Real OpenCode compatibility is not
established by this review tracker or the fake-host fixture.
