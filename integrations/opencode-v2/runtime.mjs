import { isAbsolute } from "node:path";
import { createContextLookup, unavailableContext } from "../opencode/context.mjs";

const activityIdentifier = /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$/;
const runtimePrefix = "opencode-v2:";
const validRuntimeID = (id) => typeof id === "string" && id.startsWith(runtimePrefix) &&
  id.length > runtimePrefix.length && activityIdentifier.test(id);

function untilAbort(promise, signal) {
  if (!signal) return Promise.resolve(promise).catch(unavailableContext);
  if (signal.aborted) return Promise.resolve(unavailableContext());
  return new Promise((resolve) => {
    let settled = false;
    const finish = (result) => {
      if (settled) return;
      settled = true;
      signal.removeEventListener("abort", abort);
      resolve(signal.aborted ? unavailableContext() : result);
    };
    const abort = () => finish(unavailableContext());
    signal.addEventListener("abort", abort, { once: true });
    Promise.resolve(promise).then(finish, () => finish(unavailableContext()));
    if (signal.aborted) abort();
  });
}

export function createContextReader({ binary, database, runtimeId }) {
  return async (sessionAPI, sessionID, signal) => {
    if (!validRuntimeID(runtimeId) || typeof sessionID !== "string" ||
        !activityIdentifier.test(sessionID) || signal?.aborted) {
      return unavailableContext();
    }
    try {
      const session = await untilAbort(sessionAPI.get({ sessionID }, { signal }), signal);
      const location = session?.location;
      if (signal?.aborted || session?.id !== sessionID || typeof session.projectID !== "string" || !session.projectID ||
          !location || typeof location !== "object" || Array.isArray(location) ||
          Object.keys(location).some((key) => key !== "directory") ||
          typeof location.directory !== "string" || !isAbsolute(location.directory)) return unavailableContext();

      const read = createContextLookup(binary, database, location.directory)(runtimeId, sessionID);
      return await untilAbort(read, signal);
    } catch {
      return unavailableContext();
    }
  };
}
