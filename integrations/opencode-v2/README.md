# OpenCode V2 read-only candidate

This local, unpublished candidate targets CLI, plugin and client **2.0.26**.
The adapter has its own version, **0.1.0-candidate.2.0.26**; this is not an
OpenCode release number or a verified host-support claim. V1 remains separate.

## Inspect before installing

```sh
tools-programmer integration info opencode-v2
sha256sum /absolute/path/to/tools-programmer
```

The read-only info command reports the adapter version, dependency pins, exact
protocol-2 file hashes and inventory digest, and embedded binary revision/state.
`host_verified` remains false. The default `integration info` still reports V1.
A dirty source revision identifies its baseline, not a clean released source;
retain the candidate binary hash and its build-input inventory as well.
If the toolchain omits automatic VCS metadata, the existing archive-build
mechanism may embed the observed revision and `dirty` state as explicit builder
assertions. Candidate metadata must label this provenance `builder_asserted`,
not automatic VCS evidence or a clean published release.

## Explicit setup, only with installation authorization

```sh
tools-programmer --db /absolute/existing/history.db sync opencode-v2 \
  --opencode-bin /absolute/selected/v2/opencode \
  --config-root /absolute/private/v2/config \
  --runtime-id opencode-v2:operator-chosen-realm
```

Setup runs only the selected executable's bounded `--version` probe, expecting
`opencode v2.0.26`. Startup logging/directories use private temporary HOME,
XDG roots, TMPDIR and cwd; credentials, loaders, telemetry and SSH settings
are not inherited. This is source-backed state separation for the selected
released CLI, not a sandbox for an arbitrary executable or real-host proof.
No service, session or model command is used by setup.

Setup then publishes eight hash-owned assets plus `manifest.json` before
atomically updating the native `plugins` entry. The default global root is
`${XDG_CONFIG_HOME:-$HOME/.config}/opencode-v2`; ambient V1 configuration
overrides are not used as the V2 destination. Overlapping V1 roots, symlinks,
malformed/conflicting config, customized owned assets and unverified old-bundle
upgrades are refused. JSONC comments, unrelated settings and operator options
are preserved. Repeating an unchanged setup is a byte-exact no-op.

Individual writes are atomic, not a transaction. An I/O failure may leave an
inactive partial bundle; inspect `updated` and the error before retrying in
trusted directories without concurrent external edits. No rollback deletes
operator files or previous bundles.

## Dependency and launch boundaries

Setup does **not** install dependencies, the host, skills, MCP registrations,
databases or permission rules. The embedded package manifest and lock are the
same files used for development; dev tools are pinned but may be omitted for
runtime dependency preparation. Only after separately authorizing installation,
prepare the owned `opencode-v2` package with `npm ci --omit=dev --ignore-scripts
--no-audit --no-fund --registry=https://registry.npmjs.org` using private npm
configuration. Its dependency graph contains no OpenCode CLI package. Native
install scripts are intentionally disabled; native-extension behavior is not
validated by the candidate's offline checks.

Launch the explicitly selected V2 executable with `OPENCODE_CONFIG_DIR` set
to the chosen config root. That variable isolates **global configuration only**:
use separately owned HOME/data/cache/state/runtime/temp roots for the laboratory,
and account for project/ancestor config discovery. Do not share V1 service/data
realms or infer isolation from a separate plugin directory. No launch is part
of package creation or its synthetic tests.

The runtime ID is explicit, stable and unique to the V2 realm. Context uses the
actual native session's directory, never the plugin-instance directory. It
opens existing history read-only; missing/unbound context stays unavailable.
No database copy, initialization or runtime binding migration occurs.

## What is and is not in this candidate

Included: the read-only `tools_programmer_context` tool, its portable dependency
closure, locked manifest, guarded separate setup and inspectable hashes.
Excluded: assignment/unassignment, permission bridges, observers, native helpers,
diagnostics prototypes and laboratory execution. Those prototypes remain private
and disabled. Package/type/fake-process tests do not establish installed loading,
V1/V2 runtime coexistence, native authorization or completion of issue #49.

Exact upstream candidate source:
[`9b4ec5714d481559990db0a816d5dec19541a814`](https://github.com/anomalyco/opencode/tree/9b4ec5714d481559990db0a816d5dec19541a814).
Historical 2.0.23 evidence in the ODD log describes only its earlier snapshots.
