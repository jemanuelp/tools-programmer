import { mock, test, expect } from "bun:test";
import { mkdtemp, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";
import { createParentSnapshotReader } from "./parent-snapshot.mjs";

// Opt-in type/runtime package location, not a live OpenCode server. No package
// download or persistent installation is performed by this fixture.
const modulePath = process.env.OPENCODE_PLUGIN_MODULE;
const enabled = Boolean(modulePath && isAbsolute(modulePath));

(enabled ? test : test.skip)("installed v1 SDK supports the scoped streaming parent snapshot without host access", async () => {
  const sdkPath = new URL("../../sdk/dist/index.js", pathToFileURL(modulePath!));
  const { createOpencodeClient } = await import(sdkPath.href);
  const requests: Request[] = [];
  const client = createOpencodeClient({ baseUrl: "http://fixture.invalid", fetch: async (request: Request) => {
    requests.push(request);
    return new Response(JSON.stringify([{ info: { sessionID: "parent" }, parts: [{ type: "tool", tool: "task", sessionID: "parent", callID: "call", state: { status: "running", metadata: { parentSessionId: "parent", sessionId: "child" } } }] }]), { headers: { "Content-Type": "application/json" } });
  } });
  expect(await createParentSnapshotReader(client, "/fixture/project")("parent")).toEqual([{ callID: "call", childSessionID: "child" }]);
  expect(requests.length).toBe(1);
  const url = new URL(requests[0].url);
  expect(requests[0].method).toBe("GET");
  expect(url.pathname).toBe("/session/parent/message");
  expect(url.searchParams.get("directory")).toBe("/fixture/project");
  expect(url.searchParams.get("limit")).toBe("20");
});

(enabled ? test : test.skip)("installed SDK cannot parse an unbounded non-success body through the scoped reader", async () => {
  const sdkPath = new URL("../../sdk/dist/index.js", pathToFileURL(modulePath!));
  const { createOpencodeClient } = await import(sdkPath.href);
  let cancelled = false; let pulls = 0; let authorization = "";
  const client = createOpencodeClient({ baseUrl: "http://fixture.invalid", headers: { authorization: "fixture-credential" }, fetch: async (request: Request) => {
    authorization = request.headers.get("authorization") ?? "";
    return new Response(new ReadableStream({ pull(controller) { pulls++; controller.enqueue(new TextEncoder().encode("PRIVATE")); }, cancel() { cancelled = true; } }), { status: 503 });
  } });
  await expect(createParentSnapshotReader(client, "/fixture")("parent")).rejects.toThrow("Runtime snapshot incomplete or unavailable");
  expect(cancelled).toBe(true);
  expect(pulls).toBeLessThanOrEqual(1);
  expect(authorization).toBe("fixture-credential");
});

(enabled ? test : test.skip)("loads against installed plugin package; assignment uses context, not model ID", async () => {
  const installed = await import(modulePath!);
  mock.module("@opencode-ai/plugin", () => installed);
  const { default: factory } = await import("./plugin.ts");
  const dir = await mkdtemp(join(tmpdir(), "observer-plugin-"));
  try {
    const capture = join(dir, "events.jsonl");
    const binary = join(dir, "recorder");
    await writeFile(binary, `#!${process.execPath}
import { appendFileSync } from 'node:fs';
if (process.argv.includes('info')) {
  console.log(JSON.stringify({data:{protocol:1,bundle_digest:'a'.repeat(64),capabilities:['activity-context-v1','existing-only-record','explicit-issue-outcome'],binary:{version:'0.2.0-dev+unknown.unknown',commit:'unknown',state:'unknown',build:'development'}}}));
} else if (process.argv.includes('context')) {
  const arg = k => process.argv[process.argv.indexOf(k)+1];
  console.log(JSON.stringify({data:{version:1,runtime:arg('--runtime'),session:arg('--session'),observed_at:new Date().toISOString(),project_resolution:'matched',project:'demo',repository:'github.com/example/demo',assignment:'unknown',warnings:['session_unobserved']}}));
} else {
  let data=''; process.stdin.on('data', c=>data+=c); process.stdin.on('end', ()=>appendFileSync(${JSON.stringify(capture)},data+'\\n'));
}
`, { mode: 0o700 });
    const hooks = await factory({ serverUrl: new URL("http://fixture.invalid"), project: { id: "fixture" }, directory: dir, worktree: dir } as never, { binary, database: join(dir, "fixture.db") });
    const assign = hooks.tool!.tools_programmer_assign;
    expect(Object.keys(assign.args).sort()).toEqual(["issue", "project"]);
    let asked = false;
    await assign.execute({ project: "demo", issue: 1, sessionID: "model-invented" } as never, {
      sessionID: "actual-fixture-context", messageID: "message-fixture", ask: async () => { asked = true; },
    } as never);
    expect(asked).toBe(true);
    const records = (await readFile(capture, "utf8")).trim().split("\n").map((line) => JSON.parse(line));
    expect(records.length).toBe(2);
    expect(records.every((e) => e.session === "actual-fixture-context")).toBe(true);
    expect(records[0].issue).toBe(1);
    expect(records[0].runtime).toMatch(/^[a-f0-9]{64}$/);
    await hooks.dispose?.();
  } finally { await rm(dir, { recursive: true, force: true }); }
});
