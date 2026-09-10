import assert from "node:assert/strict";
import { mkdtemp, writeFile, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createRecorder } from "./transport.mjs";

test("local transport passes bounded stdin without shell or inherited credentials", async () => {
  const dir = await mkdtemp(join(tmpdir(), "issue-recorder-"));
  const binary = join(dir, "fake recorder");
  const capture = join(dir, "capture.json");
  try {
    await writeFile(binary, `#!${process.execPath}
const fs = require('node:fs');
let input = ''; process.stdin.on('data', c => input += c);
process.stdin.on('end', () => fs.writeFileSync(${JSON.stringify(capture)}, JSON.stringify({args:process.argv.slice(2),keys:Object.keys(process.env),event:JSON.parse(input)})));
`, { mode: 0o700 });
    const recorder = createRecorder(binary, join(dir, "private db"));
    const event = { id: "e", runtime: "r", session: "s", segment: "x", kind: "bind", at: 0 };
    await recorder(event);
    const received = JSON.parse(await readFile(capture, "utf8"));
    assert.deepEqual(received.args, ["--db", join(dir, "private db"), "activity", "record"]);
    assert.deepEqual(received.event, event);
    assert.deepEqual(received.keys, ["PATH"]);
    await assert.rejects(recorder({ id: "x".repeat(17000) }), /bound/);
    await writeFile(binary, `#!${process.execPath}\nprocess.exit(1);\n`, { mode: 0o700 });
    await assert.rejects(recorder(event), /rejected/);
    assert.throws(() => createRecorder("relative", "/db"), /absolute/);
  } finally { await rm(dir, { recursive: true, force: true }); }
});
