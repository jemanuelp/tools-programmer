import type { SessionApi } from "@opencode/client";
import type { TrackingContext } from "../opencode/context.mjs";

export function createContextReader(options: {
  binary: unknown;
  database: unknown;
  runtimeId: unknown;
}): (
  session: Pick<SessionApi, "get">,
  sessionID: string,
  signal: AbortSignal,
) => Promise<TrackingContext>;
