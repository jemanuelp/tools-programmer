import assert from "node:assert/strict";
import test from "node:test";
import { createObserver } from "./observer.mjs";

const childEvent = (status = "running") => ({
  type: "message.part.updated",
  properties: { part: { sessionID: "child", id: "part", callID: "tool", type: "tool", tool: "bash", state: { status } } },
});

test("authoritative parent snapshot binds first child activity to dispatch-time assignment", async () => {
  const records = []; let time = 1000; const reads = [];
  const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e),
    readParentDispatches: async (parent) => { reads.push(parent); return [{ callID: "dispatch", childSessionID: "child" }]; },
  });
  await observer.assign({ sessionID: "parent" }, "demo", 1);
  const original = records[0].segment;
  await observer.before({ sessionID: "parent", callID: "dispatch", tool: "task" });
  await observer.assign({ sessionID: "parent" }, "demo", 2);
  await observer.event({ type: "session.created", properties: { info: { id: "child", parentID: "parent" } } });
  await Promise.all([observer.event(childEvent()), observer.event(childEvent())]);
  await observer.event(childEvent("completed"));
  const binding = records.find((e) => e.session === "child" && e.kind === "bind");
  assert.equal(binding.parent_segment, original);
  assert.ok(binding.dispatch_at < binding.at);
  assert.equal(records.filter((e) => e.session === "child" && e.kind === "bind").length, 1);
  assert.equal(records.filter((e) => e.session === "child" && e.kind === "start").length, 1);
  assert.deepEqual(reads, ["parent"]);
  await observer.dispose();
});

for (const scenario of ["ambiguous", "unavailable", "missing-local-dispatch"]) {
  test(`${scenario} snapshot never guesses child ownership or loops reads`, async () => {
    const records = []; let time = 1000; let reads = 0;
    const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e),
      readParentDispatches: async () => {
        reads++;
        if (scenario === "unavailable") throw new Error("PRIVATE_RAW_ERROR");
        return [{ callID: "d1", childSessionID: "child" }, { callID: "d2", childSessionID: "child" }];
      },
    });
    await observer.assign({ sessionID: "parent" }, "demo", 1);
    if (scenario !== "missing-local-dispatch") {
      await observer.before({ sessionID: "parent", callID: "d1", tool: "task" });
      await observer.before({ sessionID: "parent", callID: "d2", tool: "task" });
    }
    await observer.event({ type: "session.created", properties: { info: { id: "child", parentID: "parent" } } });
    await observer.event(childEvent());
    await observer.event(childEvent());
    const binding = records.find((e) => e.session === "child" && e.kind === "bind");
    assert.equal(binding.parent_segment, undefined);
    assert.ok(records.some((e) => e.session === "child" && e.reason === "uncorrelated_child"));
    assert.ok(reads <= 1);
    if (scenario === "missing-local-dispatch") assert.equal(reads, 0);
    assert.equal(JSON.stringify(records).includes("PRIVATE_RAW_ERROR"), false);
    await observer.dispose();
  });
}

test("quiescent reused child resolves a new dispatch before first new activity without rewriting prior segment", async () => {
  const records = []; let time = 1000; let callID = "d1";
  const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e),
    readParentDispatches: async () => [{ callID, childSessionID: "child" }],
  });
  await observer.assign({ sessionID: "parent" }, "demo", 1);
  const first = records[0].segment;
  await observer.before({ sessionID: "parent", callID: "d1", tool: "task" });
  await observer.event({ type: "session.created", properties: { info: { id: "child", parentID: "parent" } } });
  await observer.event(childEvent()); await observer.event(childEvent("completed"));
  await observer.event({ type: "session.status", properties: { sessionID: "child", status: { type: "idle" } } });
  await observer.assign({ sessionID: "parent" }, "demo", 2);
  const second = records.findLast((e) => e.kind === "bind" && e.session === "parent").segment;
  callID = "d2";
  await observer.before({ sessionID: "parent", callID: "d2", tool: "task" });
  const next = childEvent(); next.properties.part.callID = "new-tool";
  await observer.event(next);
  const bindings = records.filter((e) => e.kind === "bind" && e.session === "child");
  assert.deepEqual(bindings.map((e) => e.parent_segment), [first, second]);
  assert.notEqual(bindings[0].segment, bindings[1].segment);
  assert.equal(records.find((e) => e.activity === "new-tool").segment, bindings[1].segment);
  await observer.dispose();
});

test("overlapping reused-child dispatch is unknown, not a reassignment of already running work", async () => {
  const records = []; let time = 1000;
  const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e) });
  await observer.assign({ sessionID: "parent" }, "demo", 1);
  await observer.before({ sessionID: "parent", callID: "d1", tool: "task" });
  await observer.after({ sessionID: "parent", callID: "d1", tool: "task" }, { metadata: { sessionId: "child" } });
  await observer.event(childEvent());
  await observer.assign({ sessionID: "parent" }, "demo", 2);
  await observer.before({ sessionID: "parent", callID: "d2", tool: "task" });
  await observer.after({ sessionID: "parent", callID: "d2", tool: "task" }, { metadata: { sessionId: "child" } });
  const overlap = childEvent(); overlap.properties.part.callID = "overlap";
  await observer.event(overlap);
  assert.equal(records.filter((e) => e.session === "child" && e.kind === "bind").length, 1);
  assert.equal(records.filter((e) => e.activity === "overlap" && e.kind === "start").length, 0);
  assert.ok(records.some((e) => e.session === "child" && e.reason === "uncorrelated_child"));
  await observer.dispose();
});
