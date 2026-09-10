import { type Plugin, tool } from "@opencode-ai/plugin";
import { createHash } from "node:crypto";
import { createObserver } from "./observer.mjs";
import { createRecorder } from "./transport.mjs";

// Explicit opt-in only. This file is outside auto-discovery directories.
const plugin: Plugin = async (input, options) => {
  const binary = options?.binary;
  const database = options?.database;
  if (typeof binary !== "string" || typeof database !== "string") {
    throw new Error("tools-programmer observer requires binary and database options");
  }
  const runtime = createHash("sha256").update(JSON.stringify([input.serverUrl.origin, input.project.id, input.worktree])).digest("hex");
  const observer = createObserver({ record: createRecorder(binary, database), runtime });
  const observe = async (operation: Promise<unknown>) => {
    try { await operation; }
    catch { console.error("tools-programmer observer: observation lost; timing is incomplete"); }
  };
  return {
    tool: {
      tools_programmer_assign: tool({
        description: "After explicit user assignment, associate the current runtime session with a registered local issue. Never invent session IDs or infer assignment from titles. Does not confirm estimates or complete issues.",
        args: {
          project: tool.schema.string(),
          issue: tool.schema.number().int().positive(),
        },
        async execute(args, context) {
          await context.ask({ permission: "tools_programmer_assign", patterns: [`${args.project}#${args.issue}`], always: [], metadata: {} });
          return observer.assign(context, args.project, args.issue);
        },
      }),
    },
    event: ({ event }) => observe(observer.event(event)),
    "tool.execute.before": (input) => observe(observer.before(input)),
    "tool.execute.after": (input, output) => observe(observer.after(input, output)),
    dispose: () => observer.dispose(),
  };
};

export default plugin;
