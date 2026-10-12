import assert from "node:assert/strict";
import test from "node:test";
import { createContextReader } from "./runtime.mjs";

const unavailable = {
  version: 1, project_resolution: "unavailable", assignment: "unknown",
  warnings: ["tracking_unavailable"],
};

function fixture() {
  const controller = new AbortController();
  const reader = createContextReader({
    binary: "/unused-context-cli", database: "/unused-context.db",
    runtimeId: "opencode-v2:cancellation-fixture",
  });
  let directoryReads = 0;
  const session = {
    id: "session-b", projectID: "project-b",
    location: { get directory() { directoryReads++; return "/unused-checkout"; } },
  };
  let resolve!: (value: typeof session) => void;
  let reject!: (reason: Error) => void;
  const pending = new Promise<typeof session>((yes, no) => { resolve = yes; reject = no; });
  let sessionReads = 0;
  const api = {
    get(_input: { sessionID: string }, options?: { signal?: AbortSignal }) {
      sessionReads++;
      assert.equal(options?.signal, controller.signal);
      return pending;
    },
  };
  return {
    controller, session, resolve, reject,
    read: () => reader(api as never, "session-b", controller.signal),
    sessionReads: () => sessionReads, directoryReads: () => directoryReads,
  };
}

test("pre-cancelled context never requests the session", { timeout: 1000 }, async () => {
  const f = fixture();
  f.controller.abort();
  assert.deepEqual(await f.read(), unavailable);
  assert.equal(f.sessionReads(), 0);
  assert.equal(f.directoryReads(), 0);
});

test("late session success after cancellation never reaches local lookup", { timeout: 1000 }, async () => {
  const f = fixture();
  const read = f.read();
  f.controller.abort();
  assert.deepEqual(await read, unavailable);
  f.resolve(f.session);
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(f.sessionReads(), 1);
  assert.equal(f.directoryReads(), 0);
});

test("late session rejection remains handled after cancellation", { timeout: 1000 }, async () => {
  const f = fixture();
  const read = f.read();
  f.controller.abort();
  assert.deepEqual(await read, unavailable);
  f.reject(new Error("private session response"));
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(f.directoryReads(), 0);
});

test("cancellation between session resolution and reader continuation blocks lookup", { timeout: 1000 }, async () => {
  const f = fixture();
  const read = f.read();
  f.resolve(f.session);
  // The session promise queues its handler first; abort precedes the reader's await continuation.
  queueMicrotask(() => f.controller.abort());
  assert.deepEqual(await read, unavailable);
  assert.equal(f.sessionReads(), 1);
  assert.equal(f.directoryReads(), 0);
});
