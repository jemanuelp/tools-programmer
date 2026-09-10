import assert from "node:assert/strict";
import test from "node:test";
import { createParentSnapshotReader } from "./parent-snapshot.mjs";
import { runtimeFixture, jsonResponse } from "./runtime-fixture.mjs";

const task = (overrides = {}) => ({
  type: "tool", tool: "task", sessionID: "parent", callID: "dispatch",
  state: { status: "running", input: { prompt: "PRIVATE_PROMPT" }, metadata: { parentSessionId: "parent", sessionId: "child", model: { secret: "PRIVATE_MODEL" } } },
  ...overrides,
});
const rows = (parts = [task()]) => [{ info: { sessionID: "parent", role: "assistant" }, parts }];
const response = jsonResponse;

test("v1 SDK scoped streaming request normalizes authoritative task metadata only", async () => {
  const requests = [];
  const read = createParentSnapshotReader(runtimeFixture(async (options) => { requests.push(options); return response(rows()); }), "/fixture/project");
  assert.deepEqual(await read("parent"), [{ callID: "dispatch", childSessionID: "child" }]);
  assert.equal(requests.length, 1);
  assert.deepEqual(requests[0].path, { id: "parent" });
  assert.deepEqual(requests[0].query, { directory: "/fixture/project", limit: 20 });
  assert.equal(requests[0].parseAs, "stream");
  assert.ok(requests[0].signal instanceof AbortSignal);
});

for (const [name, data] of [
  ["saturated page", Array(20).fill(rows()[0])],
  ["excessive parts", rows(Array(257).fill(task()))],
  ["wrong parent", [{ info: { sessionID: "unrelated" }, parts: [] }]],
  ["wrong metadata parent", rows([task({ state: { status: "running", metadata: { sessionId: "child", parentSessionId: "other" } } })])],
  ["conflicting call identity", rows([task(), task({ state: { status: "running", metadata: { sessionId: "different-child", parentSessionId: "parent" } } })])],
  ["oversized body", rows([{ type: "text", text: "x".repeat(1024 * 1024) }])],
]) {
  test(`rejects ${name} without exposing payloads`, async () => {
    const read = createParentSnapshotReader(runtimeFixture(async () => response(data)), "/fixture");
    await assert.rejects(read("parent"), { message: "Runtime snapshot incomplete or unavailable" });
  });
}

test("stalled SDK request has one deadline, abort and no retry", async () => {
  let calls = 0; let signal;
  const read = createParentSnapshotReader(runtimeFixture(async (options) => { calls++; signal = options.signal; return new Promise(() => {}); }), "/fixture");
  await assert.rejects(read("parent"), /unavailable/);
  assert.equal(calls, 1);
  assert.equal(signal.aborted, true);
});

test("completed background task metadata remains a valid dispatch reference", async () => {
  const read = createParentSnapshotReader(runtimeFixture(async () => response(rows([task({ state: { status: "completed", output: "PRIVATE_RESULT", metadata: { parentSessionId: "parent", sessionId: "child", background: true } } })]))), "/fixture");
  assert.deepEqual(await read("parent"), [{ callID: "dispatch", childSessionID: "child" }]);
});

test("a pending sibling without metadata is not a guessed child association", async () => {
  const read = createParentSnapshotReader(runtimeFixture(async () => response(rows([task(), task({ callID: "pending-call", state: { status: "pending", input: { prompt: "PRIVATE" } } })]))), "/fixture");
  assert.deepEqual(await read("parent"), [{ callID: "dispatch", childSessionID: "child" }]);
});
