import { mock, test, expect } from "bun:test";
import { mkdtemp, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, isAbsolute } from "node:path";

// Opt-in type/runtime package location, not a live OpenCode server. No package
// download or persistent installation is performed by this fixture.
const modulePath = process.env.OPENCODE_PLUGIN_MODULE;
const enabled = Boolean(modulePath && isAbsolute(modulePath));

(enabled ? test : test.skip)("loads against installed plugin package; assignment uses context, not model ID", async () => {
  const installed = await import(modulePath!);
  mock.module("@opencode-ai/plugin", () => installed);
  const { default: factory } = await import("./plugin.ts");
  const dir = await mkdtemp(join(tmpdir(), "observer-plugin-"));
  try {
    const capture = join(dir, "events.jsonl");
    const binary = join(dir, "recorder");
    await writeFile(binary, `#!${process.execPath}
import { appendFileSync } from 'node:fs';
let data=''; process.stdin.on('data', c=>data+=c); process.stdin.on('end', ()=>appendFileSync(${JSON.stringify(capture)},data+'\\n'));
`, { mode: 0o700 });
    const hooks = await factory({ serverUrl: new URL("http://fixture.invalid"), project: { id: "fixture" }, worktree: dir } as never, { binary, database: join(dir, "fixture.db") });
    const assign = hooks.tool!.tools_programmer_assign;
    expect(Object.keys(assign.args).sort()).toEqual(["issue", "project"]);
    let asked = false;
    await assign.execute({ project: "demo", issue: 1, sessionID: "model-invented" } as never, {
      sessionID: "actual-fixture-context", messageID: "message-fixture", ask: async () => { asked = true; },
    } as never);
    expect(asked).toBe(true);
    const records = (await readFile(capture, "utf8")).trim().split("\n").map((line) => JSON.parse(line));
    expect(records.length).toBe(2);
    expect(records.every((e) => e.session === "actual-fixture-context")).toBe(true);
    expect(records[0].issue).toBe(1);
    expect(records[0].runtime).toMatch(/^[a-f0-9]{64}$/);
    await hooks.dispose?.();
  } finally { await rm(dir, { recursive: true, force: true }); }
});
