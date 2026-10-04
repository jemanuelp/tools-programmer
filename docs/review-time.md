# Manual review time

Review-time entries record declared human effort separately from review
verdicts, approvals, automatically observed agent effort, and elapsed review
time. They do not mark a review complete or approved.

## Record a duration

The project must already be registered. Supply a stable entry ID and a
self-declared reviewer label:

```sh
tools-programmer review-time record \
  --project tools-programmer \
  --entry-id review42-human-1 \
  --reviewer "Reviewer label" \
  --duration 35m \
  --issue-number 42 \
  --pr 17 \
  --sha 0123456789abcdef0123456789abcdef01234567
```

`--duration` accepts positive Go duration values such as `35m`, `1h20m`, or
`250ms`; precision finer than one millisecond is rejected. The stored duration
is an integer number of milliseconds. `--issue-number`, `--pr`, and `--sha` are
optional. An entry without an issue or PR is explicitly project-only; the
report does not infer an assignment later. A local entry may include a known
SHA without a PR. A PR entry without `--sha` is kept in the separate `unknown`
revision bucket; the current PR head is never looked up or substituted.

The entry ID is a stable replay identity. Repeating the same ID and declaration
returns the original entry without adding time. Reusing it with changed data
returns a conflict. To record another review with otherwise identical
attributes, use a different ID. The reviewer label and `manual` source describe
the declaration; they are not authenticated identity or cryptographic proof.
IDs are bounded to 200 characters, start with an ASCII letter or digit, and
then use only ASCII letters, digits, `.`, `_`, `:`, `/`, or `-`.

## Report durations

```sh
tools-programmer review-time report --project tools-programmer
tools-programmer review-time report --project tools-programmer --issue-number 42
tools-programmer review-time report --project tools-programmer --pr 17
```

The report includes total, local, and PR human milliseconds, then groups the
declared duration by review type, optional issue and PR, and known versus
unknown SHA. Project-only work appears with no issue or PR. Distinct entry IDs
count separately, including entries with equal reviewer, duration, and
revision attributes.

T1 reports manual human duration only. `agent_review_ms` and
`elapsed_review_ms` are `null`, and automatic coverage is `pending`; they are
not reported as zero. Record timestamps do not measure elapsed review time.
Automatic OpenCode review capture and its supported-version demonstration are
separate follow-up work.
