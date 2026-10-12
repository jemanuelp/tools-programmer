import { expect, test } from "bun:test";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createContextReader } from "./runtime.mjs";
import { toolsProgrammerV2 } from "./plugin.ts";

async function writeFakeContextCLI(binary: string, requestFile: string) {
  await writeFile(binary, `#!${process.execPath}
import { writeFileSync } from "node:fs";
const args = process.argv.slice(2);
const value = (key) => args[args.indexOf(key) + 1];
writeFileSync(${JSON.stringify(requestFile)}, JSON.stringify(args));
const data = {
  version: 1, runtime: value("--runtime"), session: value("--session"),
  observed_at: "2026-10-06T00:00:00.000Z", project_resolution: "matched",
  project: "demo", repository: "github.com/example/demo", assignment: "unknown", warnings: ["session_unobserved"],
};
console.log(JSON.stringify({ data }));
`, { mode: 0o700 });
}

test("V2 exposes only read-only context using the configured runtime and native session checkout", async () => {
  const temporary = await mkdtemp(join(tmpdir(), "opencode-v2-context-"));
  try {
    const pluginDirectory = join(temporary, "plugin-a");
    const sessionDirectory = join(temporary, "session-b");
    const binary = join(temporary, "fake-cli");
    const requestFile = join(temporary, "request.json");
    const runtimeId = `opencode-v2:${"r".repeat(188)}`;
    await mkdir(pluginDirectory);
    await mkdir(sessionDirectory);
    await writeFakeContextCLI(binary, requestFile);

    const sessionRequests: string[] = [];
    type RegisteredTool = {
      name: string;
      input: unknown;
      execute: (input: unknown, context: { sessionID: string; signal: AbortSignal }) => Promise<{ content?: string }>;
    };
    const names: string[] = [];
    const registered = new Map<string, RegisteredTool>();
    let requestedSignal: AbortSignal | undefined;
    let disposed = false;
    const session = {
      async get(input: { sessionID: string }, options?: { signal?: AbortSignal }) {
        sessionRequests.push(input.sessionID);
        requestedSignal = options?.signal;
        return { id: "session-b", projectID: "project-b", location: { directory: sessionDirectory } };
      },
    };
    const cleanup = await toolsProgrammerV2.setup({
      app: { version: "2.0.23" },
      location: { directory: pluginDirectory },
      options: { binary, database: join(temporary, "history.db"), runtimeId },
      session,
      tool: {
        async transform(transform: (editor: { add: (tool: unknown) => void }) => void) {
          transform({ add(tool) {
            const item = tool as RegisteredTool;
            names.push(item.name);
            registered.set(item.name, item);
          } });
          return { async dispose() { disposed = true; } };
        },
      },
    } as never);
    const signal = new AbortController().signal;
    const result = await registered.get("tools_programmer_context")!.execute({}, { sessionID: "session-b", signal });

    const request = JSON.parse(await readFile(requestFile, "utf8")) as string[];
    const context = JSON.parse(result.content!) as { project_resolution: string; assignment: string; warnings: string[] };
    expect(names).toEqual(["tools_programmer_context"]);
    expect(registered.has("tools_programmer_assign")).toBe(false);
    expect(registered.has("tools_programmer_unassign")).toBe(false);
    expect(sessionRequests).toEqual(["session-b"]);
    expect(requestedSignal).toBe(signal);
    expect(request[request.indexOf("--runtime") + 1]).toBe(runtimeId);
    expect(request[request.indexOf("--session") + 1]).toBe("session-b");
    expect(request[request.indexOf("--directory") + 1]).toBe(sessionDirectory);
    expect(context.project_resolution).toBe("matched");
    expect(context.assignment).toBe("unknown");
    expect(context.warnings).toEqual(["session_unobserved"]);
    await cleanup?.();
    expect(disposed).toBe(true);
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
});

test("missing or invalid V2 runtime IDs stay unavailable without invoking the CLI", async () => {
  const temporary = await mkdtemp(join(tmpdir(), "opencode-v2-runtime-id-"));
  try {
    const binary = join(temporary, "fake-cli");
    const requestFile = join(temporary, "request.json");
    await writeFakeContextCLI(binary, requestFile);
    const sessionReads: string[] = [];
    const session = {
      async get({ sessionID }: { sessionID: string }) {
        sessionReads.push(sessionID);
        return { id: sessionID, projectID: "project-b", location: { directory: temporary } };
      },
    };
    const unavailable = { version: 1, project_resolution: "unavailable", assignment: "unknown", warnings: ["tracking_unavailable"] };
    const invalidRuntimeIDs = [undefined, "realm-a", "a".repeat(64), "opencode-v2:", `opencode-v2:${"x".repeat(200)}`, "opencode-v2:unsafe value"];

    for (const runtimeId of invalidRuntimeIDs) {
      const reader = createContextReader({ binary, database: join(temporary, "history.db"), runtimeId });
      expect(await reader(session as never, "session-b", new AbortController().signal)).toEqual(unavailable);
    }

    expect(sessionReads).toEqual([]);
    await expect(readFile(requestFile, "utf8")).rejects.toThrow();
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
});

test("missing, failed, cancelled, and malformed native sessions stay unavailable", async () => {
  const reader = createContextReader({ binary: "/unused", database: "/unused.db", runtimeId: "opencode-v2:realm-a" });
  const unavailable = { version: 1, project_resolution: "unavailable", assignment: "unknown", warnings: ["tracking_unavailable"] };
  const cases = [
    async () => undefined,
    async () => { throw new Error("private session transport error"); },
    async () => ({ id: "different-session", projectID: "project-b", location: { directory: "/session-b" } }),
    async () => ({ id: "session-b", projectID: "project-b", location: { root: "/session-b" } }),
    async () => ({ id: "session-b", projectID: "project-b", location: { directory: "relative/session" } }),
    async () => ({ id: "session-b", projectID: "project-b", location: { directory: "/session-b", root: "/other" } }),
  ];
  for (const get of cases) {
    const result = await reader({ get } as never, "session-b", new AbortController().signal);
    expect(result).toEqual(unavailable);
  }

  const controller = new AbortController();
  let requestedSignal: AbortSignal | undefined;
  const pending = reader({ get: async (_input: unknown, options: { signal?: AbortSignal }) => {
    requestedSignal = options.signal;
    return new Promise(() => {});
  } } as never, "session-b", controller.signal);
  controller.abort();
  expect(requestedSignal).toBe(controller.signal);
  expect(await pending).toEqual(unavailable);
});
