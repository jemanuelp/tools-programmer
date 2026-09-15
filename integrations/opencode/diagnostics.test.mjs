import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";
import { createDiagnostics, readBinary, readBundle } from "./diagnostics.mjs";
import { runtimeFixture, jsonResponse } from "./runtime-fixture.mjs";

const sha = "a".repeat(64);
const binaryInfo = { protocol: 1, bundle_digest: sha, binary: { version: "0.2.0-dev+unknown.unknown", commit: "unknown", state: "unknown", build: "development" } };
const defaults = () => ({ binary: "/fixture/binary", directory: "/fixture", binaryReader: async () => binaryInfo,
  packageReader: async () => "1.18.30", bundleReader: async () => sha, readHost: async () => ({ healthy: true, version: "1.18.30" }) });

test("diagnostics never infer host version from package or preserve a stale supported result", async () => {
  let healthy = true;
  const check = createDiagnostics({ ...defaults(), readHost: async () => { if (!healthy) throw new Error("PRIVATE"); return { healthy: true, version: "1.18.30" }; } });
  assert.equal((await check("native")).status, "supported");
  healthy = false;
  const failed = await check("native");
  assert.equal(failed.status, "unavailable");
  assert.equal(failed.host_version, undefined);
  assert.deepEqual(failed.warnings, ["host_unavailable"]);
  assert.ok(!JSON.stringify(failed).includes("PRIVATE"));
  for (const [options, warning] of [
    [{ readHost: async () => ({ healthy: true, version: "1.18.31" }) }, "host_package_mismatch"],
    [{ packageReader: async () => "1.18.31" }, "package_unverified"],
    [{ bundleReader: async () => "b".repeat(64) }, "binary_bundle_mismatch"],
    [{ binaryReader: async () => { throw new Error("PRIVATE"); } }, "binary_capabilities_unavailable"],
    [{ readHost: async () => ({ healthy: true, version: "PRIVATE/path" }) }, "host_unavailable"],
  ]) {
    const result = await createDiagnostics({ ...defaults(), ...options })("native");
    assert.equal(result.status, "unavailable");
    assert.ok(result.warnings.includes(warning));
    assert.ok(!JSON.stringify(result).includes("PRIVATE"));
  }
});

test("live host diagnostics use the scoped bounded runtime transport", async () => {
  let requests = 0;
  const client = runtimeFixture((options, request) => {
    requests++;
    assert.equal(options.url, "/global/health");
    assert.equal(options.query.directory, "/fixture");
    assert.equal(request.method, "GET");
    return jsonResponse({ healthy: true, version: "1.18.30" });
  });
  const options = defaults(); delete options.readHost;
  const check = createDiagnostics({ ...options, client });
  assert.equal((await check("actual-native")).status, "supported");
  const missing = await check(undefined);
  assert.ok(missing.warnings.includes("host_unavailable"));
  assert.equal(requests, 1, "missing native metadata must not be guessed");
});

test("matching replacement bundle and binary still require a plugin restart", async () => {
  let current = sha;
  const check = createDiagnostics({ ...defaults(), bundleReader: async () => current,
    binaryReader: async () => ({ ...binaryInfo, bundle_digest: current }) });
  assert.equal((await check("native")).status, "supported");
  current = "c".repeat(64);
  const changed = await check("native");
  assert.equal(changed.status, "unavailable");
  assert.ok(changed.warnings.includes("binary_changed_since_load"));
  assert.ok(changed.warnings.includes("bundle_changed_since_load"));
});

test("binary feature capability and exact bundle checks reject stale and modified assets", async () => {
  const root = await mkdtemp(join(tmpdir(), "tracking-diagnostics-"));
  try {
    const binary = join(root, "binary");
    const info = { ...binaryInfo, capabilities: ["activity-context-v1", "existing-only-record", "explicit-issue-outcome"] };
    await writeFile(binary, `#!${process.execPath}\nconsole.log(${JSON.stringify(JSON.stringify({ data: info }))});\n`, { mode: 0o700 });
    assert.deepEqual(await readBinary(binary), binaryInfo);
    delete info.capabilities;
    await writeFile(binary, `#!${process.execPath}\nconsole.log(${JSON.stringify(JSON.stringify({ data: info }))});\n`, { mode: 0o700 });
    await assert.rejects(readBinary(binary), /unavailable/);
    const files = {};
    for (const name of ["issue-tracking.md", "plugin.ts"]) {
      await writeFile(join(root, name), "fixture");
      files[name] = createHash("sha256").update("fixture").digest("hex");
    }
    const digest = createHash("sha256").update(JSON.stringify(files)).digest("hex");
    await writeFile(join(root, "manifest.json"), JSON.stringify({ protocol: 1, files, digest }));
    assert.equal(await readBundle(pathToFileURL(root+"/")), digest);
    await writeFile(join(root, "issue-tracking.md"), "private customization");
    await assert.rejects(readBundle(pathToFileURL(root+"/")), /unavailable/);
  } finally { await rm(root, { recursive: true, force: true }); }
});
