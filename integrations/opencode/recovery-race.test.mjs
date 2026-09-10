import assert from "node:assert/strict";
import test from "node:test";
import { createObserver } from "./observer.mjs";
import { createSessionSnapshotReader } from "./session-snapshot.mjs";
import { runtimeFixture, jsonResponse } from "./runtime-fixture.mjs";

const tool = (call, status) => ({ type: "message.part.updated", properties: { part: { type: "tool", tool: "bash", sessionID: "s", id: `part-${call}`, callID: call, state: { status } } } });
const text = (finished = false) => ({ type: "message.part.updated", properties: { part: { type: "text", sessionID: "s", id: "text", time: finished ? { end: 1 } : {} } } });
const empty = () => ({ running: [], closed: [], waits: [], status: "busy" });
function gate() {
  let release;
  const promise = new Promise((resolve) => { release = resolve; });
  return { promise, release };
}
function spans(records, activity) {
  const starts = records.filter((e) => e.kind === "start" && e.activity === activity);
  return starts.map((start) => {
    const stop = records.find((e) => e.kind === "stop" && e.activity === activity && e.segment === start.segment);
    assert.ok(stop, `missing stop for ${activity}`);
    return [start.at, stop.at];
  });
}
function unionMS(intervals) {
  intervals.sort((a, b) => a[0] - b[0]);
  let result = 0; let end = -1;
  for (const [a, b] of intervals) { result += Math.max(0, b - Math.max(a, end)); end = Math.max(end, b); }
  return result;
}

test("normal pending/running receives during recovery preserve two concurrent tool intervals", async () => {
  const records = []; let time = 1; let reads = 0;
  const entered = gate(); const finish = gate();
  const observer = createObserver({ runtime: "r", clock: () => time, record: async (e) => records.push(e), readSessionState: async () => {
    reads++; entered.release(); await finish.promise;
    // The lab's tool updates arrived after the last messages GET. The queued
    // live starts, not invented snapshot entries, must preserve coverage.
    return empty();
  } });
  const startup = observer.event({ type: "session.status", properties: { sessionID: "s", status: { type: "busy" } } });
  await entered.promise;
  time = 20;
  // OpenCode publishes pending/running parts before awaiting execute.before.
  // Bodies finish only after both before hooks have returned.
  const queued = [observer.event(tool("a", "pending")), observer.event(tool("a", "running")), observer.before({ sessionID: "s", callID: "a", tool: "bash" }), observer.event(tool("b", "pending")), observer.event(tool("b", "running")), observer.before({ sessionID: "s", callID: "b", tool: "bash" })];
  time = 100; finish.release(); await Promise.all([startup, ...queued]);
  time = 120; await Promise.all([observer.event(tool("a", "running")), observer.event(tool("b", "running"))]);
  time = 3100; await observer.event(tool("a", "completed"));
  time = 3400; await observer.event(tool("b", "completed"));
  assert.equal(reads, 1);
  assert.equal(spans(records, "a").length, 1); assert.equal(spans(records, "b").length, 1);
  assert.equal(unionMS([...spans(records, "a"), ...spans(records, "b")]), 3300);
  assert.ok(records.every((e) => e.session === "s"));
  await observer.dispose();
});

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

test("a fresh before hook retries a failed recovery once; duplicates and live events cannot spin", async () => {
  const records = []; let reads = 0; let time = 1;
  const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e), readSessionState: async () => { if (++reads === 1) throw new Error("transient"); return empty(); } });
  await observer.before({ sessionID: "s", callID: "first", tool: "bash" });
  await observer.event(tool("first", "running")); await observer.event(tool("first", "completed"));
  assert.equal(reads, 1);
  await observer.before({ sessionID: "s", callID: "second", tool: "bash" });
  await observer.before({ sessionID: "s", callID: "second", tool: "bash" });
  await observer.event(tool("second", "running")); time += 200; await observer.event(tool("second", "completed"));
  assert.equal(reads, 2); assert.equal(spans(records, "second").length, 1);
  assert.ok(records.some((e) => e.kind === "gap"));
  await observer.dispose();
});

for (const category of ["permission", "question"]) {
  test(`${category} and complete live operations survive terminal receipts overtaking persistence`, async () => {
    const records = []; let time = 1;
    const entered = gate(); const finish = gate();
    const observer = createObserver({ runtime: "r", clock: () => time, readSessionState: async () => empty(), record: async (e) => {
      if (e.activity === "independent" && e.kind === "start") { entered.release(); await finish.promise; }
      records.push(e);
    } });
    await observer.before({ sessionID: "s", callID: "independent", tool: "bash" });
    await observer.before({ sessionID: "s", callID: "blocked", tool: "bash" });
    time = 100; const first = observer.event(tool("independent", "running")); await entered.promise;
    time = 110; const inference = observer.event(text());
    time = 120; const blocked = observer.event(tool("blocked", "running"));
    time = 150; const asked = observer.event({ type: `${category}.asked`, properties: { id: "request", sessionID: "s", tool: { callID: "blocked" } } });
    time = 250; const replied = observer.event({ type: `${category}.replied`, properties: { requestID: "request", sessionID: "s" } });
    time = 300; const ends = [observer.event(text(true)), observer.event(tool("blocked", "completed")), observer.event(tool("independent", "completed"))];
    finish.release(); await Promise.all([first, inference, blocked, asked, replied, ...ends]);
    assert.deepEqual(spans(records, "independent"), [[100, 300]]);
    assert.deepEqual(spans(records, "blocked"), [[120, 300]]);
    assert.deepEqual(spans(records, "text"), [[110, 300]]);
    assert.deepEqual(spans(records, "request"), [[150, 250]]);
    assert.equal(records.find((e) => e.activity === "request" && e.kind === "start").target, "blocked");
    assert.ok(records.every((e) => !Object.hasOwn(e, "startAt")));
    await observer.dispose();
  });
}

test("duplicate pending requests do not invalidate a stable recovery", async () => {
  const records = []; let time = 1; let reads = 0;
  const entered = gate(); const finish = gate();
  const wait = { activity: "request", category: "permission", target: "blocked" };
  const observer = createObserver({ runtime: "r", clock: () => time, record: async (e) => records.push(e), readSessionState: async () => {
    reads++; entered.release(); await finish.promise;
    return { ...empty(), waits: [wait], running: [{ activity: "independent", category: "tool" }] };
  } });
  const asked = { type: "permission.asked", properties: { sessionID: "s", id: "request", tool: { callID: "blocked" } } };
  const first = observer.event(asked); await entered.promise;
  time = 20; const duplicate = observer.event(asked);
  time = 100; finish.release(); await Promise.all([first, duplicate]);
  time = 300; await observer.event(tool("independent", "completed"));
  assert.equal(reads, 1); assert.deepEqual(spans(records, "independent"), [[100, 300]]);
  assert.equal(records.filter((e) => e.activity === "request" && e.kind === "start").length, 1);
  await observer.dispose();
});

test("wait transitions during a read remain recorded and require stable evidence at a fresh hook", async () => {
  const records = []; let time = 1; let reads = 0;
  const entered = gate(); const finish = gate();
  const observer = createObserver({ runtime: "r", clock: () => time, record: async (e) => records.push(e), readSessionState: async () => {
    if (++reads === 1) { entered.release(); await finish.promise; }
    return empty();
  } });
  const first = observer.event({ type: "session.status", properties: { sessionID: "s", status: { type: "busy" } } });
  await entered.promise;
  time = 20; const asked = observer.event({ type: "question.asked", properties: { sessionID: "s", id: "request", tool: { callID: "blocked" } } });
  time = 40; const replied = observer.event({ type: "question.replied", properties: { sessionID: "s", requestID: "request" } });
  time = 100; finish.release(); await Promise.all([first, asked, replied]);
  assert.deepEqual(spans(records, "request"), [[20, 40]]);
  time = 150; await observer.before({ sessionID: "s", callID: "fresh", tool: "bash" });
  time = 160; await observer.event(tool("fresh", "running")); time = 360; await observer.event(tool("fresh", "completed"));
  assert.equal(reads, 2); assert.deepEqual(spans(records, "fresh"), [[160, 360]]);
  await observer.dispose();
});
