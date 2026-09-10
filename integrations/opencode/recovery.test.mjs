import assert from "node:assert/strict";
import test from "node:test";
import { createObserver } from "./observer.mjs";

const running = (call = "independent") => ({ type: "message.part.updated", properties: { part: { sessionID: "s", callID: call, id: call, type: "tool", tool: "bash", state: { status: "running" } } } });
const terminal = (call = "independent") => ({ type: "message.part.updated", properties: { part: { sessionID: "s", callID: call, id: call, type: "tool", tool: "bash", state: { status: "completed" } } } });
const snapshot = () => ({ running: [{ activity: "blocked", category: "tool" }, { activity: "independent", category: "tool" }], closed: ["historical"], waits: [{ activity: "permission", category: "permission", target: "blocked" }], status: "busy" });

test("first observed session restores current tools and target waits at observation boundary only", async () => {
  const records = []; let time = 1000; let reads = 0;
  const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e), readSessionState: async () => { reads++; time += 50; return snapshot(); } });
  await observer.event(running());
  assert.equal(reads, 1);
  const starts = records.filter((e) => e.kind === "start");
  assert.deepEqual(starts.map((e) => e.activity).sort(), ["blocked", "independent", "permission"]);
  assert.ok(starts.every((e) => e.at >= 1050));
  assert.ok(!records.some((e) => e.activity === "historical"));
  assert.equal(starts.find((e) => e.activity === "permission").target, "blocked");
  await observer.event(terminal());
  assert.equal(records.filter((e) => e.activity === "independent" && e.kind === "stop").length, 1);
  await observer.dispose();
});

test("live terminal arriving during snapshot prevents stale restart", async () => {
  const records = []; let finish; let started;
  const ready = new Promise((resolve) => { started = resolve; });
  const observer = createObserver({ runtime: "r", record: async (e) => records.push(e), readSessionState: async () => { started(); return new Promise((resolve) => { finish = resolve; }); } });
  const first = observer.event(running());
  await ready;
  const last = observer.event(terminal());
  finish(snapshot());
  await Promise.all([first, last]);
  assert.equal(records.filter((e) => e.kind === "start").length, 0);
  assert.ok(records.some((e) => e.kind === "gap"));
  await observer.dispose();
});

test("failed wait-state read leaves unknown coverage without repeatedly querying", async () => {
  let reads = 0; const records = [];
  const observer = createObserver({ runtime: "r", record: async (e) => records.push(e), readSessionState: async () => { reads++; throw new Error("PRIVATE"); } });
  await observer.event(running()); await observer.event(running()); await observer.event(terminal());
  assert.equal(reads, 1);
  assert.equal(records.filter((e) => e.kind === "start").length, 0);
  assert.ok(records.some((e) => e.kind === "gap"));
  await observer.dispose();
});

test("recording loss creates a new recovery segment, not a stop across missing coverage", async () => {
  const records = []; let time = 1000; let fail = false; let reads = 0;
  const observer = createObserver({ runtime: "r", clock: () => ++time,
    record: async (e) => { if (fail) { fail = false; throw new Error("fixture"); } records.push(e); },
    readSessionState: async () => { reads++; return { running: [{ activity: "independent", category: "tool" }], closed: [], waits: [], status: "busy" }; },
  });
  await observer.event(running());
  fail = true;
  await assert.rejects(observer.event({ type: "session.status", properties: { sessionID: "s", status: { type: "busy" } } }));
  await observer.event(running());
  assert.equal(reads, 2);
  const bindings = records.filter((e) => e.kind === "bind");
  assert.equal(bindings.length, 2);
  assert.equal(bindings[1].resume, true);
  assert.equal(records.filter((e) => e.kind === "stop").length, 0);
  assert.ok(records.some((e) => e.reason === "reconnected"));
  await observer.dispose();
});

test("restart discovering a child without its dispatch snapshot does not invent active issue attribution", async () => {
  const records = [];
  const observer = createObserver({ runtime: "r", record: async (e) => records.push(e),
    readSessionState: async () => ({ ...snapshot(), parentSessionID: "parent" }),
  });
  await observer.event(running());
  assert.equal(records.filter((e) => e.kind === "start" && e.category === "tool").length, 0);
  assert.ok(records.some((e) => e.reason === "uncorrelated_child"));
  await observer.dispose();
});

test("persisted running parts without a currently active session do not prove a surviving job", async () => {
  const records = []; let time = 1000;
  const observer = createObserver({ runtime: "r", clock: () => ++time, record: async (e) => records.push(e), readSessionState: async () => ({ ...snapshot(), status: "unknown" }) });
  await observer.event(running());
  assert.equal(records.filter((e) => e.kind === "start" && e.category === "tool").length, 0);
  assert.ok(records.some((e) => e.reason === "unsupported_lifecycle"));
  await observer.dispose();
});
