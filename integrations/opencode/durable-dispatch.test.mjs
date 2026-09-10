import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { createObserver } from "./observer.mjs";

const event = (status = "running") => ({ type: "message.part.updated", properties: { part: { type: "tool", tool: "bash", id: "part", sessionID: "child", callID: "work", state: { status } } } });

test("durable before-hook dispatch survives parent switch and observer restart without rewriting child history", async () => {
  const records = []; let time = 1000;
  const base = { runtime: "r", clock: () => ++time, record: async (e) => records.push(e) };
  const first = createObserver(base);
  await first.assign({ sessionID: "parent" }, "demo", 1);
  const original = records[0].segment;
  await first.before({ sessionID: "parent", callID: "dispatch", tool: "task" });
  const marker = "dispatch-" + createHash("sha256").update("r\0parent\0dispatch").digest("hex");
  const persisted = records.find((e) => e.id === `${marker}-start`);
  assert.equal(persisted.segment, original);
  assert.equal(records.find((e) => e.id === `${marker}-stop`).at, persisted.at);
  await first.after({ sessionID: "parent", callID: "dispatch", tool: "task" }, { metadata: { sessionId: "child" } });
  await first.event(event());
  await first.assign({ sessionID: "parent" }, "demo", 2);
  const oldBytes = JSON.stringify(records);
  const oldCount = records.length;
  await first.dispose();
  const second = createObserver({ ...base,
    readParentDispatches: async (parent) => { assert.equal(parent, "parent"); return [{ callID: "dispatch", childSessionID: "child" }]; },
    lookupDispatch: async (runtime, session, call) => ({ runtime, session, call_id: call, segment: persisted.segment, at: persisted.at }),
    readSessionState: async () => ({ parentSessionID: "parent", singleTurn: true, running: [{ activity: "work", category: "tool" }], waits: [], closed: [], status: "busy" }),
  });
  await second.event(event());
  const restored = records.findLast((e) => e.kind === "bind" && e.session === "child" && e.parent_segment);
  assert.equal(restored.parent_segment, original);
  assert.equal(restored.dispatch_at, persisted.at);
  assert.ok(restored.at > persisted.at);
  assert.equal(JSON.stringify(records.slice(0, oldCount)), oldBytes);
  assert.ok(records.slice(oldCount).some((e) => e.reason === "reconnected"));
  await second.event(event("completed"));
  assert.equal(records.findLast((e) => e.kind === "stop" && e.activity === "work").segment, restored.segment);
  await second.dispose();
});

for (const failure of ["missing", "conflict", "ambiguous", "multi-turn"]) {
  test(`${failure} durable provenance stays unknown after restart`, async () => {
    const records = []; let time = 1000; let reads = 0;
    const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e),
      readParentDispatches: async () => failure === "ambiguous" ? [{ callID: "a", childSessionID: "child" }, { callID: "b", childSessionID: "child" }] : [{ callID: "a", childSessionID: "child" }],
      lookupDispatch: async () => { reads++; if (failure === "missing") return null; throw new Error("PRIVATE_CONFLICT"); },
      readSessionState: async () => ({ parentSessionID: "parent", singleTurn: failure !== "multi-turn", running: [{ activity: "work", category: "tool" }], waits: [], closed: [], status: "busy" }),
    });
    await observer.event(event());
    assert.equal(records.filter((e) => e.kind === "start" && e.category === "tool").length, 0);
    assert.ok(records.some((e) => e.reason === "uncorrelated_child"));
    assert.equal(JSON.stringify(records).includes("PRIVATE"), false);
    assert.equal(reads, failure === "ambiguous" || failure === "multi-turn" ? 0 : 1);
    await observer.dispose();
  });
}
