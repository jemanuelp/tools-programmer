import type { TrackingContext } from "./context.mjs";
export function createAdvisory(options: {
  prompt: string;
  lookup: (runtime: string, session: string) => Promise<TrackingContext>;
  runtime: string;
  policy?: unknown;
  clock?: () => number;
  cooldown?: number;
}): {
  context(session?: string): Promise<TrackingContext>;
  system(input: { sessionID?: string }, output: { system: string[] }): Promise<void>;
  before(input: { sessionID: string; tool: string }): Promise<void>;
  compact(input: { sessionID: string }, output: { context: string[]; prompt?: string }): Promise<void>;
  dispose(): void;
};
