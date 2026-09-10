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
