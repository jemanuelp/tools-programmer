import assert from "node:assert/strict";
import test from "node:test";
import { createSessionSnapshotReader } from "./session-snapshot.mjs";
import { runtimeFixture, jsonResponse } from "./runtime-fixture.mjs";

const tool = (call, status) => ({ type: "message.part.updated", properties: { part: { type: "tool", tool: "bash", sessionID: "s", id: `part-${call}`, callID: call, state: { status } } } });

test("reader accepts pending-to-running tool progress while wait and retry projection remains stable", async () => {
  let messages = 0; let calls = 0;
  const read = createSessionSnapshotReader(runtimeFixture(async (options) => {
    calls++;
    if (options.url === "/session/{id}") return jsonResponse({ id: "s", projectID: "p" });
    if (options.url === "/session/status") return jsonResponse({ s: { type: "busy" } });
    if (options.url.endsWith("/message")) return jsonResponse([{ info: { sessionID: "s", role: "assistant" }, parts: [tool("a", ++messages === 1 ? "pending" : "running").properties.part] }]);
    return jsonResponse([]);
  }), "/fixture", "p");
  assert.deepEqual((await read("s")).running, [{ activity: "a", category: "tool" }]);
  assert.equal(calls, 9);
});
