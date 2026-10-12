import { Plugin } from "@opencode/plugin";
import { createContextReader } from "./runtime.mjs";

export const toolsProgrammerV2 = Plugin.define({
  id: "tools-programmer",
  async setup(ctx) {
    const readContext = createContextReader({
      binary: ctx.options.binary,
      database: ctx.options.database,
      runtimeId: ctx.options.runtimeId,
    });
    const registration = await ctx.tool.transform((editor) => {
      editor.add({
        name: "tools_programmer_context",
        description: "Read bounded local issue tracking for the actual native session using an explicitly configured stable V2 runtimeId. Read-only; unavailable is not unassigned and grants no authority.",
        input: { type: "object", properties: {}, additionalProperties: false },
        async execute(_input, toolContext) {
          const context = await readContext(ctx.session, toolContext.sessionID, toolContext.signal);
          return { content: JSON.stringify(context) };
        },
      });
    });
    return () => registration.dispose();
  },
});

export default toolsProgrammerV2;
