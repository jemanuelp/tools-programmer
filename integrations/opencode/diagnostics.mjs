import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { lstat, readFile } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import { withRuntimeRead } from "./runtime-read.mjs";

const hash = (b) => createHash("sha256").update(b).digest("hex");
const digest = (s) => typeof s === "string" && /^[a-f0-9]{64}$/.test(s);
const version = (s) => typeof s === "string" && /^\d{1,5}\.\d{1,5}\.\d{1,5}$/.test(s);
const required = ["activity-context-v1", "existing-only-record", "explicit-issue-outcome"];

async function boundedFile(url) {
  const info = await lstat(url);
  if (!info.isFile() || info.size > 1024 * 1024) throw new Error("unavailable");
  return readFile(url);
}

export async function readBundle(base = new URL("./", import.meta.url)) {
  const m = JSON.parse(await boundedFile(new URL("manifest.json", base)));
  if (m.protocol !== 1 || !digest(m.digest) || !m.files || Array.isArray(m.files)) throw new Error("unavailable");
  const names = Object.keys(m.files).sort();
  if (names.length < 1 || names.length > 64 || !names.includes("plugin.ts") || !names.includes("issue-tracking.md")) throw new Error("unavailable");
  const files = {};
  for (const name of names) {
    if (!/^[a-z][a-z0-9.-]{0,100}$/.test(name) || !digest(m.files[name])) throw new Error("unavailable");
    if (hash(await boundedFile(new URL(name, base))) !== m.files[name]) throw new Error("unavailable");
    files[name] = m.files[name];
  }
  if (hash(JSON.stringify(files)) !== m.digest) throw new Error("unavailable");
  return m.digest;
}

async function readPackage() {
  // Observe the installed manifest independently of the live host. Do not infer
  // a server version from a dependency version or accept caller-supplied labels.
  const entry = import.meta.resolve("@opencode-ai/plugin");
  const url = entry.startsWith("file:") ? new URL(entry) : pathToFileURL(entry);
  const p = JSON.parse(await boundedFile(new URL("../package.json", url)));
  if (p.name !== "@opencode-ai/plugin" || !version(p.version)) throw new Error("unavailable");
  return p.version;
}

export async function readBinary(binary) {
  if (typeof binary !== "string" || !isAbsolute(binary)) throw new Error("unavailable");
  const text = await new Promise((resolve, reject) => {
    const child = spawn(binary, ["integration", "info"], { stdio: ["ignore", "pipe", "ignore"], shell: false, env: { PATH: process.env.PATH ?? "" } });
    const chunks = []; let bytes = 0;
    const timer = setTimeout(() => child.kill("SIGKILL"), 3000);
    child.stdout.on("data", (chunk) => { bytes += chunk.length; if (bytes > 8192) child.kill("SIGKILL"); else chunks.push(chunk); });
    child.on("error", () => { clearTimeout(timer); reject(new Error("unavailable")); });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code === 0 && bytes <= 8192) resolve(Buffer.concat(chunks).toString("utf8"));
      else reject(new Error("unavailable"));
    });
  });
  const info = JSON.parse(text).data;
  if (info?.protocol !== 1 || !digest(info.bundle_digest) || !Array.isArray(info.capabilities) || !required.every((c) => info.capabilities.includes(c))) throw new Error("unavailable");
  // Only allow bounded build identity, never arbitrary subprocess strings.
  const b = info.binary;
  if (!b || typeof b.version !== "string" || !/^[a-zA-Z0-9.+-]{1,150}$/.test(b.version) ||
      (b.commit !== "unknown" && !/^[a-f0-9]{40}$/.test(b.commit)) || !["clean", "dirty", "unknown"].includes(b.state) || !["development", "release"].includes(b.build)) throw new Error("unavailable");
  return { protocol: 1, bundle_digest: info.bundle_digest, binary: { version: b.version, commit: b.commit, state: b.state, build: b.build } };
}

export function createDiagnostics({ binary, client, directory, readHost, packageReader = readPackage, bundleReader = readBundle, binaryReader = readBinary }) {
  // Pin observations to this plugin load as well as checking current bytes.
  // A repaired/replaced installation requires restart, not a stale supported flag.
  const loaded = Promise.allSettled([binaryReader(binary), packageReader(), bundleReader()]);
  return async (session) => {
    const result = { status: "unavailable", warnings: [], observed_at: new Date().toISOString() };
    // No cache: an unavailable lookup never leaves a previous supported result.
    const settled = await Promise.allSettled([
      binaryReader(binary), packageReader(), bundleReader(),
      readHost ? readHost(session) : withRuntimeRead(client, directory, (read) => read("health", session)),
    ]);
    const [b, p, bundle, host] = settled.map((s) => s.status === "fulfilled" ? s.value : undefined);
    const [loadedBinary, loadedPackage, loadedBundle] = (await loaded).map((s) => s.status === "fulfilled" ? s.value : undefined);
    if (!b) result.warnings.push("binary_capabilities_unavailable");
    else result.binary = b.binary;
    if (b && (!loadedBinary || JSON.stringify(loadedBinary) !== JSON.stringify(b))) result.warnings.push("binary_changed_since_load");
    if (!version(p)) result.warnings.push("package_unavailable");
    else result.package_version = p;
    if (version(p) && p !== loadedPackage) result.warnings.push("package_changed_since_load");
    if (!digest(bundle)) result.warnings.push("bundle_unmanaged_or_modified");
    else if (b && b.bundle_digest !== bundle) result.warnings.push("binary_bundle_mismatch");
    if (digest(bundle) && bundle !== loadedBundle) result.warnings.push("bundle_changed_since_load");
    if (host?.healthy !== true || !version(host.version)) result.warnings.push("host_unavailable");
    else result.host_version = host.version;
    if (result.host_version && result.package_version && result.host_version !== result.package_version) result.warnings.push("host_package_mismatch");
    // This pair has actual isolated provider/SQLite proof. Other versions remain
    // unverified even when source inspection suggests compatible hook shapes.
    if (result.host_version && result.host_version !== "1.18.30") result.warnings.push("host_unverified");
    if (result.package_version && result.package_version !== "1.18.30") result.warnings.push("package_unverified");
    if (result.warnings.length === 0) result.status = "supported";
    return result;
  };
}
