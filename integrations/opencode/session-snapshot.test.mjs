import assert from "node:assert/strict";
import test from "node:test";
import { createSessionSnapshotReader } from "./session-snapshot.mjs";
import { createParentSnapshotReader } from "./parent-snapshot.mjs";
import { runtimeFixture, jsonResponse } from "./runtime-fixture.mjs";

function state(options) {
  switch (options.url) {
    case "/session/{id}": return { id: "s", projectID: "project" };
    case "/session/{id}/message": return [{ info: { sessionID: "s" }, parts: [
      { type: "tool", sessionID: "s", callID: "blocked", tool: "bash", state: { status: "running", time: { start: 1 }, input: { secret: "PRIVATE" } } },
      { type: "tool", sessionID: "s", callID: "independent", tool: "bash", state: { status: "running" } },
      { type: "tool", sessionID: "s", callID: "historical", tool: "bash", state: { status: "completed", output: "PRIVATE" } },
    ] }];
    case "/permission": return [{ id: "permission", sessionID: "s", tool: { callID: "blocked", messageID: "msg" }, metadata: { secret: "PRIVATE" } }];
    case "/question": return [{ id: "question", sessionID: "other", questions: ["PRIVATE"] }];
    case "/session/status": return { s: { type: "busy" } };
  }
}

test("bounded stable snapshot projects current tools and targeted waits, never historical time or payloads", async () => {
  const calls = [];
  const reader = createSessionSnapshotReader(runtimeFixture(async (options, request) => {
    calls.push(options.url); assert.equal(request.headers.get("authorization"), "fixture-only");
    assert.equal(options.query.directory, "/fixture"); return jsonResponse(state(options));
  }), "/fixture", "project");
  const result = await reader("s");
  assert.equal(calls.length, 9);
  assert.deepEqual(result.running, [{ activity: "blocked", category: "tool" }, { activity: "independent", category: "tool" }]);
  assert.deepEqual(result.waits, [{ activity: "permission", category: "permission", target: "blocked" }]);
  assert.deepEqual(result.closed, ["historical"]);
  assert.ok(!JSON.stringify(result).includes("PRIVATE"));
  assert.ok(!JSON.stringify(result).includes("start"));
});

for (const failure of ["missing-question", "changed-state", "wrong-project"]) {
  test(`${failure} does not become an empty successful wait snapshot`, async () => {
    let messages = 0;
    const reader = createSessionSnapshotReader(runtimeFixture(async (options) => {
      if (failure === "missing-question" && options.url === "/question") return new Response("PRIVATE", { status: 503 });
      if (failure === "wrong-project" && options.url === "/session/{id}") return jsonResponse({ id: "s", projectID: "other" });
      if (failure === "changed-state" && options.url.endsWith("/message") && ++messages === 2) return jsonResponse([]);
      return jsonResponse(state(options));
    }), "/fixture", "project");
    await assert.rejects(reader("s"), { message: "Runtime snapshot incomplete or unavailable" });
  });
}

test("non-2xx body is cancelled before SDK parsing, including an endless error stream", async () => {
  let cancelled = false; let chunks = 0;
  const client = runtimeFixture(async () => new Response(new ReadableStream({
    pull(controller) { chunks++; controller.enqueue(new TextEncoder().encode("PRIVATE")); },
    cancel() { cancelled = true; },
  }), { status: 500 }));
  await assert.rejects(createParentSnapshotReader(client, "/fixture")("s"), /unavailable/);
  assert.equal(cancelled, true); assert.ok(chunks <= 1);
});

test("single-turn provenance requires complete matching message ancestry without compaction", async () => {
  for (const compacted of [false, true]) {
    const reader = createSessionSnapshotReader(runtimeFixture(async (options) => {
      if (options.url === "/session/{id}/message") {
        const original = state(options)[0];
        original.info = { sessionID: "s", role: "assistant", parentID: "user-message" };
        return jsonResponse([{ info: { sessionID: "s", role: "user", id: "user-message" }, parts: compacted ? [{ type: "compaction" }] : [] }, original]);
      }
      return jsonResponse(state(options));
    }), "/fixture", "project");
    assert.equal((await reader("s")).singleTurn, !compacted);
  }
});
