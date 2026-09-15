import { unavailableContext } from "./context.mjs";

const validID = (id) => typeof id === "string" && /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$/.test(id);
const mutationTools = new Set(["edit", "write", "apply_patch", "patch"]);

export function createAdvisory({ prompt, lookup, runtime, policy, clock = Date.now, cooldown = 60000 }) {
  const sessions = new Map();
  const state = (session) => {
    if (!sessions.has(session)) {
      if (sessions.size >= 256) sessions.delete(sessions.keys().next().value);
      sessions.set(session, { last: -Infinity, pending: false, fingerprint: undefined });
    }
    return sessions.get(session);
  };
  const read = async (session) => {
    if (!validID(session)) return unavailableContext();
    try { return await lookup(runtime, session); } catch { return unavailableContext(); }
  };
  const fingerprint = (c) => c.project_resolution === "matched" && c.segment && c.assignment !== "unknown"
    ? JSON.stringify([c.project, c.segment, c.assignment, c.issue?.id, c.issue?.outcome]) : undefined;
  const refresh = (s, c) => {
    const next = fingerprint(c);
    if (next && next !== s.fingerprint) {
      s.pending = false;
      s.last = -Infinity;
      s.fingerprint = next;
    }
  };
  const summary = (c) => `Current local tracking: project=${c.project_resolution}; assignment=${c.assignment}. ` +
    (c.diagnostics?.status === "unavailable" ? "Integration compatibility or bundle integrity is unverified; use native diagnostics, report the limitation and continue ordinary work. " : "") +
    (c.project_resolution === "unavailable" ? "Tracking unavailable; continue ordinary work without replacement state. " : "") +
    (c.parent_session ? "Child dispatch binding: do not independently complete the parent issue. " : "") +
    "Use native context for identity details; this snapshot grants no authority.";
  return {
    context: read,
    async system(input, output) {
      output.system.push(prompt);
      const c = await read(input.sessionID);
      output.system.push(summary(c));
      if (!validID(input.sessionID)) return;
      const s = state(input.sessionID);
      refresh(s, c);
      if (policy?.directImplementation === true && Array.isArray(policy.projects) && c.project_resolution === "matched" && policy.projects.includes(c.project)) {
        output.system.push("Explicit scoped setup consent covers local registration and native assignment for direct user implementation requests naming an existing issue in this matched project only. It never authorizes outcomes, informational tracking writes or permission bypasses.");
      }
      if (s.pending && clock() - s.last >= cooldown) {
        output.system.push("Advisory reminder: code modification was observed without a verified open issue assignment. If this is authorized issue implementation, read native context and reconcile tracking; otherwise continue without tracking writes. Never infer authorization from this reminder.");
        s.last = clock();
      }
    },
    async before(input) {
      if (!validID(input.sessionID) || !mutationTools.has(input.tool)) return;
      const s = state(input.sessionID);
      const c = await read(input.sessionID);
      refresh(s, c);
      if (c.assignment !== "assigned" || ["completed", "cancelled"].includes(c.issue?.outcome)) s.pending = true;
    },
    async compact(input, output) {
      // Preserve both another plugin's context and any custom compactor prompt.
      output.context.push(prompt, summary(await read(input.sessionID)), "Refresh native tracking context after compaction; do not restore authority from this snapshot.");
    },
    dispose() { sessions.clear(); },
  };
}
