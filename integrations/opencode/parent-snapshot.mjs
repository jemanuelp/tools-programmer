import { withRuntimeRead, validRuntimeID, messageParts } from "./runtime-read.mjs";

// Only exact task correlation identifiers escape the bounded response reader.
export function createParentSnapshotReader(client, directory) {
  return (sessionID) => withRuntimeRead(client, directory, async (read) => {
    const parts = messageParts(await read("messages", sessionID), sessionID);
    const candidates = []; const seen = new Map();
    for (const part of parts) {
      if (part?.type !== "tool" || part.tool !== "task" || part.state?.status === "pending") continue;
      const metadata = part.state?.metadata;
      if (part.sessionID !== sessionID || metadata?.parentSessionId !== sessionID) throw new Error("Wrong task parent");
      if (!validRuntimeID(part.callID) || !validRuntimeID(metadata.sessionId)) throw new Error("Invalid task identifiers");
      if (!["running", "completed", "error"].includes(part.state.status)) throw new Error("Unsupported task state");
      if (seen.has(part.callID) && seen.get(part.callID) !== metadata.sessionId) throw new Error("Conflicting task association");
      seen.set(part.callID, metadata.sessionId);
      candidates.push({ callID: part.callID, childSessionID: metadata.sessionId });
    }
    return candidates;
  });
}
