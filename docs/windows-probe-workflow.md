# Manual Windows process-containment check

`.github/workflows/windows-probe.yml` prepares a fresh `windows-2025` VM to build
the CLI and test its native process cleanup with a fake executable. It does not
install OpenCode, invoke a model, or run the integration laboratory. Preparing
this workflow is not proof that the Windows tests have passed.

## Scope and prerequisites

The selected source revision must include the composite host preflight, Windows
Job Object runner and `TestWindowsSyncProbeContainsAndCleansDescendants`.
The runner starts its root suspended, assigns it to a kill-on-close job, then
resumes it. Tests cover success, an exited wrapper, timeout, overflow and
cancellation, including cleanup of spawned descendants and unchanged setup on
rejection. No test result certifies an actual OpenCode installation or all
Windows versions.

## Publication and execution are separate

The sole trigger is `workflow_dispatch`; pushes and PRs do not trigger this
workflow. GitHub requires the workflow on the default branch for dispatch.
Publish source/workflow only through the repository's normal, authorized review
process; do not update that branch automatically. Any later hosted execution
requires separate user authorization and a named source revision. The existing
laboratory workflow is independent and unchanged.

Standard hosted runners are free on public repositories. Private repositories
use included minutes and can incur charges. The job is capped at 15 minutes,
its test step at five minutes, and `go test` at three minutes; the probe's own
three-second and 8192-byte limits remain separate.

## Security and evidence

The job uses read-only repository permissions, no persisted checkout credentials,
and official actions pinned by commit. It checks out the event's exact SHA,
records source/workflow/runner identity and the selected Go 1.26.x patch, and
explicitly downloads pinned Go dependencies. Module resolution is disabled in
the fake-host test step with `GOPROXY=off`; setup does require network access.

Build/test stdout, stderr and exit codes are retained as artifacts for seven
days, including on failure. No executable, database or checkout is uploaded.
The JSON result guard demands the required root test pass and rejects skipped
tests or `no tests to run`; a zero native exit alone is not acceptance.

## Local preparation checks

YAML structure, permissions, action pins, exit handling and evidence boundaries
were checked locally and independently inspected. Linux tests and Windows
cross-compilation check only their respective scopes. PowerShell/actionlint and
native Windows execution were unavailable locally; no workflow was dispatched
as part of preparation. The real-host matrix still belongs to #50/#62 and the
V2 adapter to #61.
