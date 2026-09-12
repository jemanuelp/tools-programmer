# Run an isolated, source-exact integration laboratory

The opt-in laboratory builds the selected working-tree bytes and exercises the real CLI, private MCP subprocess and installed OpenCode against a deterministic local provider. **An implementation or unit-test result is not a runtime acceptance receipt.** Reports identify exactly which scenarios ran; blocked, unsupported and failed cases never count as passing coverage.

## Quick path

Initial platform: **Linux amd64, Docker Engine, Ubuntu 24.04**. The host needs Git and Go 1.26+ to build the runner. No host Node, Bun, OpenCode, provider account or global installation is required.

```sh
# Explicit preparation: these two commands may download public Go dependencies.
go mod download
go build -trimpath -o bin/tools-programmer-lab ./cmd/tools-programmer-lab

# Explicit network-enabled image preparation; nothing is run against a provider.
bin/tools-programmer-lab prepare --source .

bin/tools-programmer-lab list

# Existing output directories are refused; choose a NEW directory every run.
bin/tools-programmer-lab run --source . --output /tmp/lab-first-report \
  --scenarios contracts,permission-denial,silent,egress --timeout 10m
```

`prepare` has a 30-minute deadline and keeps the named local dependency image. `run` never pulls an image and never installs dependencies. If the image is absent or the source requires an uncached module, explicitly prepare it again. Do not solve an offline-build failure by enabling runtime egress. `--image` selects an already prepared local image, resolved to its immutable image ID before creation.

Allow approximately **0.5–1.5 GB downloads, 2–5 GB image/build storage and 4–6 GiB available RAM** for initial preparation and a run. These are planning allowances, not measured image sizes. Runtime is limited to 2 CPUs, 4 GiB memory with no additional swap allowance, 256 PIDs and a 3 GiB private tmpfs. Scenarios run once, sequentially, with fresh state; the global deadline includes offline compilation. Normal cases have 90 seconds each; interactive cases have at most four minutes, still bounded by the global deadline.

## What runs where

| Boundary | Contract |
|---|---|
| Host runner | Selects/copies source, invokes Docker with argument arrays, reads actual terminal decisions, validates reports and removes its container |
| Explicit image build | Ubuntu snapshot packages, verified toolchain archives, locked plugin dependencies and Go module preparation |
| Single runtime container | Nonroot UID 10001, no capabilities, no-new-privileges, read-only root, no network except its own loopback |
| Fixture per scenario | New repository, private database, HOME/XDG directories, observer copy and runtime sessions |
| Provider | Bounded per-invocation state; actual model-visible tool catalogue and runtime terminal results gate progression |
| Export | Only bounded structured report frames; the host writes new 0700 directories and 0600 JSON/Markdown files |

There are no published ports, Docker sockets, host HOME mounts, credentials, real databases or host-source mounts. Source is delivered as a bounded archive over a private `docker exec` stdin stream to the already-started container, using UID 10001. Its receiver creates an exact private copy at `/tmp/source`; no root runtime or helper container is needed. The OpenCode fixture repository is separate from that copy, so the product repository's agent instructions and project configuration are not auto-discovered. Default plugins, external skills, model downloads and updates are disabled. One preinstalled plugin package and lock are provided to the pinned runtime's global dependency check.

The runtime controller and provider use loopback ephemeral ports. A server restart intentionally reuses the same observed port, fixture directory, runtime state and session, preserving the observer namespace. A UI/SSE reconnect is different: it does not restart the server-side plugin. The private tmpfs explicitly permits execution of the freshly compiled lab and product binaries (`exec`); `nosuid`, `nodev`, nonroot execution, zero capabilities and the read-only image root remain enforced. Docker's implicit `noexec` default is incompatible with compiling executables there and then running them.

### Source identity and privacy

The snapshot includes current tracked bytes (including modifications/deletions) and nonignored untracked development files. Paths, original modes and content hashes contribute to the source hash. Two scans plus Git metadata comparison detect ordinary concurrent edits. Stop editing during capture; this is not an atomic filesystem snapshot or a security boundary against a hostile process under the same account.

Symlinks, escaping paths, known credential/state filenames, oversized files and unexpected files in a captured snapshot are refused, not silently ignored. `.git`, ignored build/database artifacts, CodeGraph indexes and local lab `node_modules` are not copied. Keep unrelated sensitive files outside the selected source or ignored; filename screening is not a universal secret detector. The host snapshot and archive stay inside a 0700 outer directory; nothing chmods the checkout or makes that outer directory public. The receiver ignores archive ownership and creates files as UID 10001 while preserving their exact original permission bits, including 0600. Its root directory is 0700. It rejects duplicate paths and special files and verifies the original manifest before signalling readiness.

Neither HEAD-only archives nor a previously built product binary substitute for the selected source. After its startup handshake and verified source delivery, the image bootstrap recompiles the selected **lab runner**, then that runner compiles the selected **product**, using `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `-mod=readonly` and `-buildvcs=false`. Both builds verify source identity before and after compilation. This is an exact copied context, not a read-only source bind mount: original file modes remain intact in private tmpfs, while the host checkout is never mounted or modified. Reports carry the Git commit, dirty flag, source hash, binary hash, image ID and checked runtime versions. The `source-control` case changes a separate copied build input and verifies both identity rejection and a different compiled binary.

## Select the behavior you need

| Scenario | Exercised behavior and independent assertion |
|---|---|
| `contracts` | CLI persistence, baseline/revision/final history, confirmation refusal, outcome transitions, immutable PR ownership and real read-only MCP parity |
| `permission-denial` | Actual assignment request and fixture denial; no subsequent sleep or assigned work |
| `fresh-human` | Actual human assignment permission, exactly one assignment and `sleep 3`, automatic timing/attribution and CLI/MCP parity |
| `silent` | Actual silent three-second tool; raw runtime boundaries and independent monotonic bound versus automatic accounting |
| `wait-overlap` | Actual pending question while an independent four-second tool completes; positive measured overlap and work remains counted; fixture rejects the question, never answers for a human |
| `permission-overlap` | Pending issue-B assignment permission overlaps independent work still assigned to A; fixture denial cannot switch ownership |
| `parallel` | Two actual child tasks with overlapping sleeps and a waiting parent; raw child interval union versus summed time, independently of production `CalculateActivity` |
| `retry` | One real provider HTTP 429 / Retry-After; separate observed retry interval, monotonic backoff and exclusion from active time |
| `switch-reuse` | Same actual child reused after A→B; native full-turn quiescence, post-switch immutable A bookmark, original-record preservation and B parity |
| `background` | Actual background task/job metadata; child remains on A across parent B; subsequent quiescent reuse belongs to B |
| `cancel` | Abort owned session during a long sleep; bounded terminal/unknown accounting and no extension to wall-clock now |
| `restart` | Kill owned runtime process group during sleep; preserve missing-terminal gaps; restart same namespace, count only new work and create a new segment |
| `reconnect` | Disconnect/reconnect actual external SSE transport without inventing plugin loss; observer still captures later work |
| `egress` | Actual loopback connection succeeds; nonloopback interfaces are absent; IPv4/IPv6 public and host-gateway connection attempts fail |
| `missing-dependencies` | Missing plugin import fails without creating node_modules; empty Go module cache fails offline |
| `contamination` | Effective runtime configuration, absent warmed sessions/assignments, no inherited credential variables or host configuration |
| `timeout-cleanup` | Deadline kills and reaps an owned process group; owned OpenCode port closes on cleanup |

No static scenario entry is a placeholder that always returns unsupported. The runtime scenarios must reach the real tool and accounting assertions to pass. Missing expected capabilities or unfinished product behavior must be investigated; do not relabel an unexpected FAIL as an accepted limitation. `unsupported` exists in the report format for genuinely established capability gaps, not as a substitute for implementing a scenario.

The controlled scenarios use narrow, explicitly synthetic fixture permission rules for `lab#1`, `lab#2`, `sleep *` and the `worker` subagent. Estimation confirmations are synthetic declarations testing persistence, **not authentication of human presence**. Neither fixture permission rules nor denial mechanics establish actual human approval.

For `switch-reuse`, the parent and first child must have completed native assistant turns (including native completion timestamps and final `stop`, not just a completed Bash part) and be idle. The parent is then assigned to B. Only after both native turns are quiescent and two consecutive A readbacks agree is A's immutable bookmark captured, **before dispatching B's reused child**. Settlement is bounded to five seconds using evidence polling; it is not an increased timing tolerance. Original stored events and binding fields must survive the switch unchanged; derived segment end projections are not mistaken for stored binding mutations. The later complete A summary and its records must match the bookmark exactly, and the existing same-child/B-attribution/parity checks still apply.

The historical families report `issue2-runtime-families-bc351e3e-01` recorded `switch-reuse` failing `issue_a_history_stable`, but retained no field diff. Its oracle captured A before the parent switch, spanning a period where the switch turn can legitimately append A activity before binding B. The corrected boundary does not retroactively pass that report or prove/exclude a product defect; a fresh scoped rerun is required. New history mismatches retain only before/after SHA-256 hashes, the failed check and allowlisted changed-field categories. Categories describe differences, not their cause; no raw summaries, records, prompts or tool payloads are exported.

### Genuine human lane

```sh
bin/tools-programmer-lab run --source . --output /tmp/lab-human-one \
  --scenarios fresh-human --interactive --timeout 10m
bin/tools-programmer-lab run --source . --output /tmp/lab-human-two \
  --scenarios fresh-human --interactive --timeout 10m
```

The host requires a controlling terminal. After OpenCode produces the actual pending request, the runner displays one static question. The human types `allow` or `deny`; a model, preconfigured consent ID or autoapproval flag cannot answer it. The reply is sent only for the request returned by that fresh runtime. Without `--interactive`, the same scenario terminates as `blocked / human_decision_required`, without assigning or sleeping. A refusal or deadline is also a nonpass.

The supported interactive path is a real `/dev/tty` passing `TCGETS`, with a four-minute case deadline inside the configured total budget. There is no parent-chat question bridge or generic PTY adapter. Run the commands above from the human's controlling terminal; a chat confirmation is not substituted for terminal input.

An unanswered human wait that reaches its deadline reports `blocked / human_decision_timeout` (human wait timeout), not a network failure. Context cancellation observed before the reply reports `blocked / human_decision_cancelled`; neither outcome sends a permission reply or extends the deadline, and normal cleanup still runs. A received denial reports `blocked / human_declined` after sending `reject`. An actual reply transport failure remains `fail / runtime_transport`. The context is rechecked after reading the decision, but this cannot eliminate cancellation races during HTTP dispatch.

Two independently generated fresh reports with distinct run identities are required; copying a report or repeating a command against warmed state does not satisfy that requirement. This implementation does not assert that those human runs have happened.

## Reports and failure handling

Read `report.md` first, then `report.json` for assertion labels, observed/expected counters, UTC start time and elapsed milliseconds. The input hash covers scenario selection, interactive mode and deadline separately from source identity. `versions_verified` distinguishes checked runtime versions from the expected baseline on an early failure. No prompts, raw tool results, runtime error bodies, source paths, provider credentials or databases are exported. Runtime logs are discarded. Image preparation reduces stdout/stderr in memory to an allowlisted cause, numeric vertex/step, stage category, Docker exit and failed-process exit; no raw build log is exported.

Preparation diagnostics preserve the first classified cause on the failed build vertex (for example `cause=tls_trust`, `stage=apt`, `step=2/11`, `docker_exit=1`, `process_exit=100`), not a later missing-package symptom. Lines are bounded to 8 KiB and at most 128 vertex summaries are retained; oversized output is flagged while later bounded error lines can still supply exit/stage evidence. An inspect command is shown only when this invocation actually emitted a valid build-details reference. No ambient latest-build lookup or historical reference is substituted. If none was emitted, diagnostics omit the reference instead of guessing one.

Exit codes: **0** only when startup completes, every selected scenario passes and container removal succeeds; **1** for failure, blocked/unsupported/not-run coverage or cleanup failure; **2** for invalid CLI usage/selection. A host interruption preserves the last valid bounded report frame. Unexecuted selections have `status: not_run`, no checks and zero scenario elapsed time, never fabricated per-scenario failures. tmpfs is not copied after container shutdown: it disappears on stop, so reports are streamed while attached.

Global cancellation stops dispatch before the next scenario's fixture/setup; completed and current-case evidence remains intact, and untouched selections receive `not_run / run_interrupted`. A case-local timeout does not skip later selections while the global context remains alive. Each complete report frame must pass schema, run/source/input identity and selected-prefix validation before replacing the checkpoint. A later invalid or oversized complete frame retains that checkpoint but records a separate `protocol_error` in JSON and Markdown and forces a nonzero exit, even after all PASS results. Oversized lines are discarded in full, never decoded from a truncated prefix; an unfinished final line cannot replace the checkpoint. Existing startup failure causes and exact-container cleanup evidence remain separate.

The existing report stream now starts before source readiness and carries one shared `startup` record: source wait/verification, offline lab build/verification, re-exec, guest entry, version checks, product build/verification and completion. Failures retain the stage, allowlisted cause, exit information and elapsed startup time. Before source verification, reported identity is the host's expected identity, not a claim of a verified copy. Compiler output uses the same bounded classifier as preparation; raw stdout/stderr is not exported. A re-exec permission failure reports `stage: reexec`, `cause: exec_denied`, `exit_code: 1`; untouched selections share `code: startup_failed`. The readiness wait and offline lab build use the configured total deadline, not an added 30-second startup cutoff. A missing or interrupted protocol is reported as such rather than attributing activity to scenarios.

The runner removes only its exact observed container ID. If create times out, it attempts recovery using the random run name plus matching label before removal; it never prunes globally. SIGKILL, host power loss or an unavailable Docker daemon can prevent cleanup. In that case inspect the uniquely named `tools-programmer-lab-<run>` container and its matching `tools-programmer.lab.run` label before manually removing that specific ID. An unconfirmed cleanup never becomes PASS. The prepared image intentionally remains reusable; no named volumes are created.

Timing tolerances are declared in the scenarios before execution: silent tool versus runtime boundaries 500 ms, parallel union/sum 750 ms, retry boundary 750 ms, and the silent monotonic envelope adds 1500 ms for runtime transport/settling. Do not widen these after a failed run to obtain green results. Investigate resource contention and the labelled observation first.

## Pins and deliberate upgrades

`lab/versions.json` records the public metadata provenance. The Dockerfile pins the **amd64 Ubuntu manifest**, not an unverified tag or an inferred digest. Ubuntu packages resolve against the signed `20260910T000000Z` snapshot; the installed package inventory is retained inside the image. Go 1.26.8, Node 24.18.0 and OpenCode 1.18.30 baseline-x64 archives are checked against SHA-256 before extraction. `npm ci --ignore-scripts` enforces the committed integrity lock; Go uses `go.sum` and explicit `go mod verify` during preparation.

**Trust bootstrap:** the pinned Ubuntu layer contains its archive signing keyring but no system CA bundle. Before HTTPS APT, BuildKit downloads the dated [Mozilla/curl PEM bundle](https://curl.se/ca/cacert-2026-08-13.pem) with `ADD --checksum` and mode 0644. Its SHA-256 is verified against the publisher's dated checksum; it is not host trust copied into the image. APT explicitly uses that PEM for TLS and the base image's `/usr/share/keyrings/ubuntu-archive-keyring.gpg` for signed indexes. The installed Ubuntu `ca-certificates` package subsequently generates the normal system bundle. TLS peer/hostname and APT signature checks remain enabled; no unauthenticated-package or insecure-TLS fallback exists. The Mozilla-derived bundle is MPL-2.0; see the [publisher's provenance and limitations](https://curl.se/docs/caextract.html).

**Manifest verification:** legacy `docker manifest inspect --verbose` fails verification for this exact OCI digest on the observed Docker client. That discrepancy was reproduced, not ignored. Direct anonymous Docker Hub registry-v2 requests returned 424 bytes whose SHA-256 exactly matches both the pin and the `Docker-Content-Digest` header, under OCI-inclusive and Docker-v2-only Accept headers. Both responses are OCI media type. The separately hash-verified config declares `linux/amd64`; `docker buildx imagetools inspect` also recognizes the same digest. The digest is unchanged. The legacy client's internal failure mechanism remains unproven; its failure is not evidence that the independently verified bytes should be replaced.

**Historical failure, not a new result:** build `x0v17bdv7twz2rz29yh7bcghe` failed at APT step 2/11 (process exit 100) before the CA bootstrap correction. Its reported source identity was `52594eb8188ead68844bab3a9d16371187f96f95332a9b4f61317b567288d794`. This reference is retained only as history; a subsequent prepare invocation must report its own observed result. Bootstrap artifact/TLS verification and unit tests do not establish that the corrected image has built successfully.

The subsequent historical build `default/default/33fn5r11ksflnbi8mp1xevqpu` completed the verified ADD but failed APT at padded step `[ 3/12]`, process exit 100, because `_apt` could not read the CA path. A tiny, product-free probe of the exact ADD found **0644 on both newly created directories** `/etc/ssl` and `/etc/ssl/certs`, as well as the correctly pinned 0644 PEM. `_apt` (UID 42) could not traverse those directories. Changing only their mode to 0755 preserved the PEM hash and allowed `_apt` to read all bytes. The Dockerfile now applies that narrow directory correction before APT; TLS and signature checks are unchanged. The diagnostic parser accepts padded step numbers and classifies the primary CaInfo read error as `ca_read`, rather than exporting the secondary Release-file symptom. This probe proves path readability, not successful full image preparation or runtime coverage.

Historical build `zc62jkxpkm75g9nmsd07r6cm0` reached user creation after package, archive, npm and Go preparation, but exited 127 because build PATH omitted `/usr/sbin`. A read-only probe verified `/usr/sbin/useradd` is executable in the exact pinned base; the Dockerfile now uses that absolute path. The adjacent startup audit also reproduced unreadable host-owned 0600 source under UID 10001, which motivated the ownership-preserving private transfer above. A separate receiver-only probe confirmed 0700 directories and 0600 files owned/readable by UID 10001, with the original source manifest still valid. Private HOME/XDG/tmpfs creation also passed a UID-10001 probe. These limited probes do not prove full image startup or any product/runtime scenario.

The first runtime batch on source `5f08f778b8ebed79077aeacd5d6cd21ecc9788039b7b84715402aeba0e82b404` and image `0cfbeeda598b30e943fa8c18a9bef441f1bf65c60aa867f1be61657c46ba9fe5` exited before scenario evidence. One bounded startup-only diagnostic on that exact image/source found `/tmp` mounted `noexec`; the offline lab build succeeded in 34,779 ms with exit 0 and no compiler stderr, but execution of the compiled runner's static `list` command failed with exit 126. This establishes an executable-mount blocker, not a source-transfer failure or a reason to increase timeouts. No OpenCode/model/scenario was run by that diagnostic. The corrected executable tmpfs and instrumented full startup chain still require fresh verification; old zero-check reports are retained as historical failures, not rewritten as successful coverage.

To upgrade: independently resolve the official manifest/checksums, update the Dockerfile and version record together, regenerate the JS lock only during explicit preparation, verify imports/SDK fixtures, prepare a new image, and run the selected negative controls plus affected runtime scenarios. Do not infer compatibility across OpenCode versions from HTTP health. The 1.18.30 baseline is specific to this lab; the observer documentation's older 1.18.29 transport analysis is not a universal compatibility claim.

## CI and future regressions

To add a regression, add one function taking the existing fixture and result, register a deterministic ID in `scenarios.go`, and keep tests beside the behavior. Reuse lifecycle, provider and privacy boundaries; do not copy a private lab or introduce a second orchestration framework. Assert expected success from actual terminal state before advancing provider stages. Read resolved segment assignment rather than an event's original payload issue. Keep unknown setup activity unassigned; never inject normalized events and call them automatic observation.

```sh
go test ./internal/lab ./internal/cli ./internal/mcpserver
go test -race ./internal/lab
node --test integrations/opencode/*.test.mjs
# Optional package/SDK fixture after explicit local npm ci in lab/:
OPENCODE_PLUGIN_MODULE="$PWD/lab/node_modules/@opencode-ai/plugin/dist/index.js" \
  bun test integrations/opencode/plugin.test.ts
```

## Remaining acceptance and boundaries

Issue #1 was closed for its delivered scope. #2 owns this reusable infrastructure and its regression implementations; **#3 human-wait, #4 cancellation/restart and #5 actual OpenChamber acceptance remain separate**. A passing harness build does not close or certify any issue.

Active queued/overlapping reuse of a still-running background child is not yet an implemented runtime scenario; quiescent reuse is. That gap is explicitly retained in reports rather than inferred from background launch. Two genuine fresh human runs and live verification of this new container baseline remain required. Paid-model and actual OpenChamber execution are not implemented by this controller: they require a separately authorized adapter, compatibility verification and (for paid providers) explicit credentials/network scope and budget. They cannot be enabled by a hidden environment fallback and no GUI/provider claim follows from server-only checks.

Rollback removes `cmd/tools-programmer-lab`, `internal/lab`, `lab`, the CI workflow and these documentation links. It does not alter the observer, product schema, real databases, global configuration or GitHub state. Container repeatability is not a security certification; run only trusted source and image definitions.
