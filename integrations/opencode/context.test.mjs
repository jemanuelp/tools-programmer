import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, writeFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createContextLookup } from "./context.mjs";

test("UnavailableIntegrationWarnsWithoutCreatingState or exposing stale binary output", async () => {
  const dir = await mkdtemp(join(tmpdir(), "advisory-context-"));
  try {
    const binary = join(dir, "binary");
    const database = join(dir, "missing", "history.db");
    const lookup = createContextLookup(binary, database, dir);
    for (const body of [undefined, `console.error('PRIVATE');process.exit(1)`, `console.log('PRIVATE')`, `console.log(JSON.stringify({data:{version:99,assignment:'unassigned'}}))`]) {
      if (body) await writeFile(binary, `#!${process.execPath}\n${body}\n`, { mode: 0o700 });
      const result = await lookup("r", "native");
      assert.equal(result.assignment, "unknown");
      assert.equal(result.project_resolution, "unavailable");
      assert.ok(!JSON.stringify(result).includes("PRIVATE"));
      await assert.rejects(stat(database), { code: "ENOENT" });
    }
    await writeFile(binary, `#!${process.execPath}
const arg=k=>process.argv[process.argv.indexOf(k)+1];
console.log(JSON.stringify({data:{version:1,runtime:arg('--runtime'),session:arg('--session'),observed_at:new Date().toISOString(),project_resolution:'matched',project:'demo',repository:'github.com/example/demo',assignment:'unassigned',segment:'a',at:1,warnings:['PRIVATE'],private_payload:'PRIVATE'}}));
`, { mode: 0o700 });
    const result = await lookup("r", "native");
    assert.equal(result.assignment, "unassigned");
    assert.ok(!JSON.stringify(result).includes("PRIVATE"));
  } finally { await rm(dir, { recursive: true, force: true }); }
});
