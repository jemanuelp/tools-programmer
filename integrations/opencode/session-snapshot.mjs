import { withRuntimeRead, validRuntimeID, messageParts } from "./runtime-read.mjs";

function waits(rows, session, category) {
  if (!Array.isArray(rows) || rows.length > 128) throw new Error("Incomplete pending requests");
  const result = [];
  for (const row of rows) {
    if (!validRuntimeID(row?.sessionID)) throw new Error("Invalid pending request");
    if (row.sessionID !== session) continue;
    if (!validRuntimeID(row.id) || (row.tool && !validRuntimeID(row.tool.callID))) throw new Error("Invalid wait identity");
    result.push({ activity: row.id, category, ...(row.tool ? { target: row.tool.callID } : {}) });
  }
  return result;
}

export function createSessionSnapshotReader(client, directory, projectID) {
  return (session) => withRuntimeRead(client, directory, async (read) => {
    const info = await read("session", session);
    if (!projectID || info?.id !== session || info.projectID !== projectID) throw new Error("Wrong project session");
    if (info.parentID !== undefined && !validRuntimeID(info.parentID)) throw new Error("Invalid parent session");
    const collect = async () => {
      const running = []; const closed = []; const identities = new Set();
      const messages = await read("messages", session);
      const parts = messageParts(messages, session);
      const users = messages.filter((message) => message.info.role === "user");
      const singleTurn = users.length === 1 && validRuntimeID(users[0].info.id)
        && messages.every((message) => message.info.role === "user" || (message.info.role === "assistant" && message.info.parentID === users[0].info.id))
        && !parts.some((part) => part?.type === "compaction");
      for (const part of parts) {
        if (part?.type !== "tool") continue;
        if (part.sessionID !== session || !validRuntimeID(part.callID) || identities.has(part.callID)) throw new Error("Ambiguous tool identity");
        identities.add(part.callID);
        if (part.tool === "tools_programmer_assign") continue;
        if (part.state?.status === "completed" || part.state?.status === "error") closed.push(part.callID);
        else if (part.state?.status === "running") running.push({ activity: part.callID, category: part.tool === "task" ? "delegation" : part.tool === "question" ? "question" : "tool" });
        else if (part.state?.status !== "pending") throw new Error("Unknown tool state");
      }
      const pending = [...waits(await read("permissions", session), session, "permission"), ...waits(await read("questions", session), session, "question")];
      const statuses = await read("status", session);
      if (!statuses || Array.isArray(statuses) || typeof statuses !== "object" || Object.keys(statuses).length > 128) throw new Error("Incomplete status map");
      const status = Object.hasOwn(statuses, session) ? statuses[session]?.type : "unknown";
      if (!["busy", "idle", "retry", "unknown"].includes(status)) throw new Error("Unknown session state");
      running.sort((a, b) => a.activity.localeCompare(b.activity)); closed.sort(); pending.sort((a, b) => a.activity.localeCompare(b.activity));
      if (new Set(pending.map((w) => w.activity)).size !== pending.length) throw new Error("Ambiguous wait identity");
      return { running, closed, waits: pending, status, singleTurn };
    };
    // No atomic snapshot API exists here. Require stable projected reads and
    // let the observer's receive-generation fence reject concurrent events.
    const first = await collect(); const second = await collect();
    if (JSON.stringify(first) !== JSON.stringify(second)) throw new Error("Runtime state changed during read");
    return { ...second, ...(info.parentID ? { parentSessionID: info.parentID } : {}) };
  });
}
