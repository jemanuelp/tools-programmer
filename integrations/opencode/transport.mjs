import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

export function createRecorder(binary, database) {
  if (!isAbsolute(binary) || !isAbsolute(database)) throw new Error("Observer requires absolute binary and database paths");
  return async (event) => {
    const input = JSON.stringify(event);
    if (Buffer.byteLength(input) > 16384) throw new Error("Normalized event exceeds recorder bound");
    await new Promise((resolve, reject) => {
      const child = spawn(binary, ["--db", database, "activity", "record"], {
        stdio: ["pipe", "pipe", "ignore"], shell: false,
        // No provider credentials are needed by the local recorder.
        env: { PATH: process.env.PATH ?? "" },
      });
      let bytes = 0;
      const timer = setTimeout(() => child.kill("SIGKILL"), 10000);
      child.stdout.on("data", (chunk) => { bytes += chunk.length; if (bytes > 32768) child.kill("SIGKILL"); });
      child.stdin.on("error", () => {});
      child.on("error", () => { clearTimeout(timer); reject(new Error("Local recorder unavailable; observations incomplete")); });
      child.on("close", (code) => {
        clearTimeout(timer);
        if (code === 0 && bytes <= 32768) resolve();
        else reject(new Error("Local recorder rejected an observation; timing is incomplete"));
      });
      child.stdin.end(input);
    });
  };
}

// This subprocess has only the read-only dispatch-show CLI surface. It does not
// initialize/migrate state, enumerate projects or expose arbitrary SQL.
export function createDispatchLookup(binary, database) {
  if (!isAbsolute(binary) || !isAbsolute(database)) throw new Error("Dispatch lookup requires absolute paths");
  const validID = (id) => typeof id === "string" && /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$/.test(id);
  return async (runtime, session, call) => {
    if (![runtime, session, call].every(validID)) throw new Error("Invalid dispatch lookup identifiers");
    const output = await new Promise((resolve, reject) => {
      const child = spawn(binary, ["--db", database, "activity", "dispatch-show", "--runtime", runtime, "--session", session, "--call", call], {
        stdio: ["ignore", "pipe", "ignore"], shell: false, env: { PATH: process.env.PATH ?? "" },
      });
      const chunks = []; let bytes = 0;
      const timer = setTimeout(() => child.kill("SIGKILL"), 3000);
      child.stdout.on("data", (chunk) => { bytes += chunk.length; if (bytes > 4096) child.kill("SIGKILL"); else chunks.push(chunk); });
      child.on("error", () => { clearTimeout(timer); reject(new Error("Local dispatch lookup unavailable")); });
      child.on("close", (code) => {
        clearTimeout(timer);
        if (code === 0 && bytes <= 4096) resolve(Buffer.concat(chunks).toString("utf8"));
        else reject(new Error("Local dispatch lookup failed"));
      });
    });
    let result;
    try { result = JSON.parse(output).data; } catch { throw new Error("Invalid dispatch lookup response"); }
    if (result === null) return null;
    if (!result || Object.keys(result).length !== 5 || result.runtime !== runtime || result.session !== session || result.call_id !== call || !validID(result.segment) || !Number.isSafeInteger(result.at) || result.at < 0 || result.at > 4102444800000) throw new Error("Mismatched dispatch lookup response");
    return { runtime, session, call_id: call, segment: result.segment, at: result.at };
  };
}
