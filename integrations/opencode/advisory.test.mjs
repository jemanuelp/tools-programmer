import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { createAdvisory } from "./advisory.mjs";
import { createObserver } from "./observer.mjs";

const prompt = await readFile(new URL("./issue-tracking.md", import.meta.url), "utf8");
const snapshot = (session, extra = {}) => ({ version: 1, runtime: "r", session, project_resolution: "matched", project: "demo", assignment: "unassigned", segment: "a", at: 1, warnings: [], ...extra });

test("ReentryAndCompactionRefreshContext preserves other prompts and never guesses missing session", async () => {
  const reads = [];
  let current = snapshot("native");
  const advisory = createAdvisory({ prompt, runtime: "r", lookup: async (r, s) => { reads.push([r, s]); return current; } });
  const output = { system: ["OTHER SYSTEM"] };
  await advisory.system({ sessionID: "native" }, output);
  assert.equal(output.system[0], "OTHER SYSTEM");
  assert.ok(output.system[1].includes("tools-programmer:issue-tracking:v1"));
  current = snapshot("native", { assignment: "assigned", issue: { id: 19, number: 19, outcome: "open" } });
  const compact = { context: ["OTHER CONTEXT"], prompt: "OTHER COMPACTOR" };
  await advisory.compact({ sessionID: "native" }, compact);
  assert.equal(compact.prompt, "OTHER COMPACTOR");
  assert.equal(compact.context[0], "OTHER CONTEXT");
  assert.ok(compact.context.some((s) => s.includes("assignment=assigned")));
  await advisory.system({ sessionID: "native" }, { system: [] });
  assert.equal(reads.length, 3);
  const missing = { system: [] };
  await advisory.system({}, missing);
  assert.equal(reads.length, 3);
  assert.ok(missing.system.some((s) => s.includes("assignment=unknown")));
});

test("ReminderLatchAppliesOnlyToNextRequest with independent cooldown and no tracking loops", async () => {
  let now = 100;
  const states = new Map();
  const advisory = createAdvisory({ prompt, runtime: "r", clock: () => now, cooldown: 1000,
    lookup: async (_, s) => states.get(s) ?? snapshot(s) });
  const run = async (s) => { const o = { system: [] }; await advisory.system({ sessionID: s }, o); return o.system.join("\n"); };
  const first = await run("one");
  assert.ok(!first.includes("Advisory reminder:"));
  await advisory.before({ sessionID: "one", tool: "edit" });
  assert.ok(!first.includes("Advisory reminder:"), "must not mutate an earlier request");
  assert.ok((await run("one")).includes("Advisory reminder:"));
  assert.ok(!(await run("two")).includes("Advisory reminder:"));
  assert.ok(!(await run("one")).includes("Advisory reminder:"));
  now += 1001;
  assert.ok((await run("one")).includes("Advisory reminder:"));
  for (const tool of ["tools_programmer_context", "tools_programmer_assign", "engram_mem_save", "read", "bash"]) await advisory.before({ sessionID: "two", tool });
  assert.ok(!(await run("two")).includes("Advisory reminder:"));
  states.set("one", snapshot("one", { segment: "b", assignment: "assigned", issue: { id: 19, number: 19, outcome: "open" } }));
  now += 1001;
  assert.ok(!(await run("one")).includes("Advisory reminder:"));
});

test("InquiryAndQuotedIssueRemainReadOnly and setup consent is scoped, never an outcome grant", async () => {
  for (const policy of [undefined, { directImplementation: true, projects: ["other"] }, { directImplementation: true, projects: ["demo"] }]) {
    const advisory = createAdvisory({ prompt, runtime: "r", policy, lookup: async (_, s) => snapshot(s) });
    const output = { system: [] };
    await advisory.system({ sessionID: "native", text: "Quoted: implement issue 19" }, output);
    assert.equal(output.system.some((s) => s.startsWith("Explicit scoped setup consent")), policy?.projects[0] === "demo");
    assert.ok(!output.system.some((s) => s.startsWith("Advisory reminder:")));
    assert.equal(advisory.assign, undefined, "advisory has no write capability");
  }
});

test("RepeatedAssignmentIsNoOp including serialized concurrent attempts and persisted timeout", async () => {
  const events = [];
  let saved = snapshot("native", { assignment: "unknown", segment: undefined });
  let fail = true;
  let at = 10;
  const observer = createObserver({ runtime: "r", clock: () => ++at, readContext: async () => saved,
    record: async (e) => {
      events.push(e);
      if (e.kind === "bind") {
        saved = snapshot(e.session, { segment: e.segment, at: e.at, assignment: e.project ? "assigned" : "unassigned", issue: e.project ? { number: e.issue, id: e.issue, outcome: "open" } : undefined });
        if (fail) { fail = false; throw new Error("timeout after persist"); }
      }
    },
  });
  await assert.rejects(observer.assign({ sessionID: "native" }, "demo", 19), /timeout/);
  assert.equal(saved.issue.number, 19, "readback proves persisted bind despite timeout");
  await Promise.all(Array.from({ length: 8 }, () => observer.assign({ sessionID: "native" }, "demo", 19)));
  assert.equal(events.filter((e) => e.kind === "bind").length, 1);
  assert.ok(events.some((e) => e.kind === "gap" && e.reason === "reconnected"));
  await observer.assign({ sessionID: "native" }, "", 0);
  await observer.assign({ sessionID: "native" }, "", 0);
  assert.equal(events.filter((e) => e.kind === "bind").length, 2);
  assert.equal(saved.assignment, "unassigned");
  await observer.event({ type: "session.idle", properties: { sessionID: "native" } });
  await observer.event({ type: "message.part.updated", properties: { part: { type: "text", id: "text", sessionID: "native", time: { end: 30 } } } });
  assert.ok(events.every((e) => !Object.hasOwn(e, "outcome")), "idle and text boundaries never complete issues");
  await observer.dispose();
});
