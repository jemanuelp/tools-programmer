import assert from "node:assert/strict";
import test from "node:test";
import { createObserver } from "./observer.mjs";

test("authoritative assignment, payload privacy, failure lifecycle and retry separation", async () => {
  const events = [];
  let time = 1000;
  const o = createObserver({ record: async (e) => events.push(e), clock: () => ++time, runtime: "fixture-runtime" });
  await o.assign({ sessionID: "actual-session" }, "project", 1);
  const part = { type: "tool", sessionID: "actual-session", callID: "call-1", tool: "bash", state: { status: "running", input: { secret: "DO_NOT_STORE" } } };
  await o.event({ type: "message.part.updated", properties: { part } });
  await o.event({ type: "permission.asked", properties: { id: "request-1", sessionID: "actual-session", tool: { callID: "call-1" }, metadata: { secret: "DO_NOT_STORE" } } });
  await o.event({ type: "permission.replied", properties: { requestID: "request-1", sessionID: "actual-session" } });
  await o.event({ type: "session.status", properties: { sessionID: "actual-session", status: { type: "retry", message: "DO_NOT_STORE" } } });
  await o.event({ type: "session.status", properties: { sessionID: "actual-session", status: { type: "busy" } } });
  part.state = { status: "error", error: "DO_NOT_STORE" };
  await o.event({ type: "message.part.updated", properties: { part } });
  assert.equal(events.filter((e) => e.activity === "call-1" && e.kind === "stop").length, 1);
  assert.equal(events.filter((e) => e.category === "retry").length, 2);
  assert.equal(events.find((e) => e.category === "permission").target, "call-1");
  assert.equal(JSON.stringify(events).includes("DO_NOT_STORE"), false);
  assert.equal(events[0].session, "actual-session");
  await o.dispose();
});

test("dispatch snapshot survives parent switch; late correlation never rewrites earlier segments", async () => {
  const events = []; let time = 1000;
  const o = createObserver({ record: async (e) => events.push(e), clock: () => ++time, runtime: "fixture" });
  await o.assign({ sessionID: "parent" }, "project", 1);
  const original = events[0].segment;
  await o.before({ tool: "task", sessionID: "parent", callID: "dispatch" });
  await o.assign({ sessionID: "parent" }, "project", 2);
  await o.after({ tool: "task", sessionID: "parent", callID: "dispatch" }, { metadata: { sessionId: "child" }, output: "DO_NOT_STORE" });
  const child = events.find((e) => e.kind === "bind" && e.session === "child");
  assert.equal(child.parent_segment, original);
  assert.ok(child.dispatch_at < child.at);
  await o.event({ type: "session.status", properties: { sessionID: "late-child", status: { type: "busy" } } });
  await o.after({ tool: "task", sessionID: "parent", callID: "dispatch" }, { metadata: { sessionId: "late-child" } });
  assert.equal(events.filter((e) => e.kind === "bind" && e.session === "late-child").length, 2);
  assert.ok(events.some((e) => e.session === "late-child" && e.reason === "uncorrelated_child"));
  await o.dispose();
});

test("silent tools remain open; unload and idle do not fabricate completion", async () => {
  const events = []; let time = 1000;
  const o = createObserver({ record: async (e) => events.push(e), clock: () => ++time, runtime: "fixture" });
  await o.assign({ sessionID: "s" }, "project", 1);
  await o.event({ type: "message.part.updated", properties: { part: { type: "tool", sessionID: "s", callID: "long-test", tool: "bash", state: { status: "running" } } } });
  time += 600000;
  await o.event({ type: "session.status", properties: { sessionID: "s", status: { type: "idle" } } });
  await o.dispose();
  assert.equal(events.filter((e) => e.kind === "stop").length, 0);
  assert.ok(events.some((e) => e.reason === "uncertain_inference"));
});

test("reused child receives a new dispatch snapshot; duplicate before hook cannot change the original", async () => {
  const events = []; let time = 1000;
  const o = createObserver({ record: async (e) => events.push(e), clock: () => ++time, runtime: "fixture" });
  await o.assign({ sessionID: "parent" }, "project", 1);
  const first = events[0].segment;
  await o.before({ tool: "task", sessionID: "parent", callID: "d1" });
  await o.assign({ sessionID: "parent" }, "project", 2);
  const second = events.findLast((e) => e.kind === "bind").segment;
  await o.before({ tool: "task", sessionID: "parent", callID: "d1" });
  await o.after({ tool: "task", sessionID: "parent", callID: "d1" }, { metadata: { sessionId: "child" } });
  await o.before({ tool: "task", sessionID: "parent", callID: "d2" });
  await o.after({ tool: "task", sessionID: "parent", callID: "d2" }, { metadata: { sessionId: "child" } });
  const bindings = events.filter((e) => e.kind === "bind" && e.session === "child");
  assert.deepEqual(bindings.map((e) => e.parent_segment), [first, second]);
  assert.notEqual(bindings[0].segment, bindings[1].segment);
  await o.dispose();
});

test("recorder loss marks every affected session, not just the next session", async () => {
  const events = []; let time = 1000; let fail = false;
  const o = createObserver({ record: async (e) => { if (fail) { fail = false; throw new Error("fixture failure"); } events.push(e); }, clock: () => ++time, runtime: "fixture" });
  await o.assign({ sessionID: "a" }, "project", 1);
  await o.assign({ sessionID: "b" }, "project", 1);
  fail = true;
  await assert.rejects(o.event({ type: "session.status", properties: { sessionID: "a", status: { type: "busy" } } }));
  await o.event({ type: "session.status", properties: { sessionID: "b", status: { type: "busy" } } });
  await o.event({ type: "session.status", properties: { sessionID: "a", status: { type: "busy" } } });
  assert.deepEqual(events.filter((e) => e.reason === "event_loss").map((e) => e.session).sort(), ["a", "b"]);
  await o.dispose();
});
