import { type Plugin, tool } from "@opencode-ai/plugin";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { createObserver } from "./observer.mjs";
import { createRecorder, createDispatchLookup } from "./transport.mjs";
import { createParentSnapshotReader } from "./parent-snapshot.mjs";
import { createSessionSnapshotReader } from "./session-snapshot.mjs";
import { createContextLookup } from "./context.mjs";
import { createAdvisory } from "./advisory.mjs";
import { createDiagnostics } from "./diagnostics.mjs";

// Explicit opt-in only. This file is outside auto-discovery directories.
const plugin: Plugin = async (input, options) => {
  const binary = options?.binary;
  const database = options?.database;
  if (typeof binary !== "string" || typeof database !== "string" || !isAbsolute(binary) || !isAbsolute(database)) {
    console.error("tools-programmer: tracking unavailable; explicit absolute binary/database options required");
    return { "experimental.chat.system.transform": async (_, output) => { output.system.push("tools-programmer tracking unavailable; continue ordinary work without creating replacement state."); } };
  }
  const runtime = createHash("sha256").update(JSON.stringify([input.serverUrl.origin, input.project.id, input.worktree])).digest("hex");
  const lookup = createContextLookup(binary, database, input.directory);
  const diagnostics = createDiagnostics({ binary, client: input.client, directory: input.directory });
  let prompt;
  try { prompt = await readFile(new URL("./issue-tracking.md", import.meta.url), "utf8"); }
  catch { prompt = "tools-programmer advisory prompt unavailable; use the project skill as fallback. Do not infer authorization or create replacement state."; }
  const advisory = createAdvisory({ prompt, lookup: async (runtime, session) => ({
    ...await lookup(runtime, session), diagnostics: await diagnostics(session),
  }), runtime, policy: options?.assignmentPolicy });
  const observer = createObserver({ record: createRecorder(binary, database), runtime,
    readParentDispatches: createParentSnapshotReader(input.client, input.directory),
    readSessionState: createSessionSnapshotReader(input.client, input.directory, input.project.id),
    lookupDispatch: createDispatchLookup(binary, database),
    readContext: lookup,
  });
  const observe = async (operation: Promise<unknown>) => {
    try { await operation; }
    catch { console.error("tools-programmer observer: observation lost; timing is incomplete"); }
  };
  const assign = async (context: { sessionID: string }, project: string, issue: number) => {
    let warning;
    try { await observer.assign(context, project, issue); }
    catch { warning = "Assignment attempt uncertain; persisted readback follows. Do not blindly retry."; }
    return JSON.stringify({ context: await advisory.context(context.sessionID), ...(warning ? { warning } : {}) });
  };
  return {
    tool: {
      tools_programmer_diagnostics: tool({
        description: "Read redacted binary capabilities, bundle integrity and independently observed host/package versions. Does not install, repair, open a database or grant authority. Unsupported/unavailable tracking never blocks ordinary code work.",
        args: {},
        async execute(_, context) { return JSON.stringify(await diagnostics(context.sessionID)); },
      }),
      tools_programmer_context: tool({
        description: "Read bounded current local tracking for the actual native session and registered checkout. Read-only; unavailable is not unassigned and grants no authority.",
        args: {},
        async execute(_, context) { return JSON.stringify(await advisory.context(context.sessionID)); },
      }),
      tools_programmer_assign: tool({
        description: "After explicit user assignment, associate the current runtime session with a registered local issue. Never invent session IDs or infer assignment from titles. Does not confirm estimates or complete issues.",
        args: {
          project: tool.schema.string(),
          issue: tool.schema.number().int().positive(),
        },
        async execute(args, context) {
          await context.ask({ permission: "tools_programmer_assign", patterns: [`${args.project}#${args.issue}`], always: [], metadata: {} });
          return assign(context, args.project, args.issue);
        },
      }),
      tools_programmer_unassign: tool({
        description: "After explicit authorization, end this native session's current issue assignment without changing prior segments or outcomes. Read back uncertain attempts.",
        args: {},
        async execute(_, context) {
          await context.ask({ permission: "tools_programmer_unassign", patterns: ["current-session"], always: [], metadata: {} });
          return assign(context, "", 0);
        },
      }),
    },
    event: ({ event }) => observe(observer.event(event)),
    "experimental.chat.system.transform": advisory.system,
    "experimental.session.compacting": advisory.compact,
    "tool.execute.before": async (input) => { await advisory.before(input); await observe(observer.before(input)); },
    "tool.execute.after": (input, output) => observe(observer.after(input, output)),
    dispose: async () => { advisory.dispose(); await observer.dispose(); },
  };
};

export default plugin;
