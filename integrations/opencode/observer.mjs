import { randomUUID } from "node:crypto";

const idPattern = /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$/;
const validID = (value) => typeof value === "string" && idPattern.test(value);

// This module never receives a transport for raw runtime objects. Every call to
// record below constructs an explicit scalar allowlist. It does not log payloads.
export function createObserver({ record, clock = Date.now, runtime = randomUUID() }) {
  const sessions = new Map();
  const dispatches = new Map();
  const childParents = new Map();
  let tail = Promise.resolve();
  let pending = 0;
  const markLost = (at) => {
    for (const s of sessions.values()) s.lossAt = Math.min(s.lossAt ?? at, at);
  };

  const serial = (fn, at) => {
    if (pending >= 256) {
      markLost(at);
      return Promise.reject(new Error("Observer queue full; activity is incomplete"));
    }
    pending++;
    const result = tail.then(fn);
    tail = result.catch(() => { markLost(at); }).finally(() => { pending--; });
    return result;
  };

  const emit = async (s, fields) => {
    await record({ id: randomUUID(), runtime, session: s.id, segment: s.segment, ...fields });
  };

  const bind = async (id, at, assignment = {}) => {
    if (!validID(id)) throw new Error("Missing authoritative runtime session ID");
    const s = { id, segment: randomUUID(), at, active: new Map(), closed: new Set(), assignment };
    await emit(s, { kind: "bind", at, ...assignment });
    sessions.set(id, s);
    return s;
  };

  const gap = (s, reason, at) => emit(s, { kind: "gap", at, reason });

  const ensure = async (id, at) => {
    let s = sessions.get(id);
    if (!s) {
      s = await bind(id, at, { resume: true });
      await gap(s, childParents.has(id) ? "uncorrelated_child" : "observer_restart", at);
    }
    if (s.lossAt !== undefined) {
      await gap(s, "event_loss", Math.max(s.at, s.lossAt));
      s.lossAt = undefined;
    }
    return s;
  };

  const begin = async (s, activity, category, at, target) => {
    if (!validID(activity) || (target && !validID(target))) return;
    if (s.active.has(activity) || s.closed.has(activity)) return;
    const fields = { activity, category, ...(target ? { target } : {}) };
    await emit(s, { kind: "start", at, ...fields });
    s.active.set(activity, fields);
  };

  const end = async (s, activity, at) => {
    const fields = s.active.get(activity);
    if (!fields) {
      if (!s.closed.has(activity)) {
        await gap(s, "unsupported_lifecycle", at);
        s.closed.add(activity);
      }
      return;
    }
    await emit(s, { kind: "stop", at, ...fields });
    s.active.delete(activity);
    s.closed.add(activity);
  };

  // Association must be supplied by task metadata, never by titles or matching
  // prompts. Late correlation does not rewrite an already observed child.
  const correlate = async (parentID, callID, childID, at) => {
    if (!validID(childID) || childID === parentID) return;
    const d = dispatches.get(`${parentID}\0${callID}`);
    if (!d) return;
    if (d.child === childID) return;
    const previous = sessions.get(childID);
    if (previous && at <= previous.at) return;
    const child = await bind(childID, at, {
      parent_session: parentID,
      parent_segment: d.segment,
      dispatch_at: d.at,
    });
    d.child = childID;
    if (previous) await gap(child, "uncorrelated_child", at);
  };

  return {
    assign(context, project, issue) {
      const at = clock();
      const session = context.sessionID;
      // Only the plugin wrapper calls this with the actual ToolContext. There
      // is deliberately no session argument in the model-facing tool schema.
      return serial(async () => {
        if (!validID(session)) throw new Error("Missing authoritative session context");
        const previous = sessions.has(session) ? await ensure(session, at) : undefined;
        if (previous && at <= previous.at) throw new Error("Assignment timestamp has not advanced; retry explicitly");
        const running = previous ? [...previous.active.values()] : [];
        const s = await bind(session, at, { project, issue });
        s.retry = previous?.retry;
        if (previous) for (const item of running) await end(previous, item.activity, at);
        // A switch splits this session's intervals, not its descendants'.
        for (const item of running) await begin(s, item.activity, item.category, at, item.target);
        await gap(s, "uncertain_inference", at);
        return "Issue assignment recorded for the current runtime session. Timing remains experimental.";
      }, at);
    },
    before(input) {
      const at = clock();
      if (input.tool !== "task" || !validID(input.sessionID) || !validID(input.callID)) return Promise.resolve();
      const session = input.sessionID;
      const call = input.callID;
      return serial(async () => {
        const s = await ensure(session, at);
        if (!dispatches.has(`${session}\0${call}`)) dispatches.set(`${session}\0${call}`, { segment: s.segment, at });
      }, at);
    },
    after(input, output) {
      const at = clock();
      if (input.tool !== "task") return Promise.resolve();
      // Read only the correlation scalar. Never stringify metadata or output.
      const child = output?.metadata?.sessionId;
      const session = input.sessionID;
      const call = input.callID;
      if (!validID(child) || !validID(session) || !validID(call)) return Promise.resolve();
      return serial(() => correlate(session, call, child, at), at);
    },
    event(event) {
      const at = clock();
      // Extract scalars before queueing; no raw payload survives in the queue.
      const type = event?.type;
      const p = event?.properties;
      if (type === "session.created") {
        const id = p?.info?.id;
        const parent = p?.info?.parentID;
        if (validID(id) && validID(parent)) childParents.set(id, parent);
        return Promise.resolve();
      }
      if (type === "message.part.updated") {
        const part = p?.part;
        const session = part?.sessionID;
        const partType = part?.type;
        const activity = partType === "tool" ? part?.callID : part?.id;
        if (!validID(session) || !validID(activity)) return Promise.resolve();
        if (!["tool", "text", "reasoning"].includes(partType)) return Promise.resolve();
        if (part?.tool === "tools_programmer_assign") return Promise.resolve();
        const isTask = part?.tool === "task";
        const category = isTask ? "delegation" : part?.tool === "question" ? "question" : "tool";
        const status = part?.state?.status;
        const child = validID(part?.state?.metadata?.sessionId) ? part.state.metadata.sessionId : undefined;
        const finished = typeof part?.time?.end === "number";
        return serial(async () => {
          const s = await ensure(session, at);
          if (partType === "tool") {
            if (isTask) await correlate(session, activity, child, at);
            if (status === "running") await begin(s, activity, category, at);
            if (status === "completed" || status === "error") await end(s, activity, at);
          } else if (partType === "text" || partType === "reasoning") {
            // Only observed streaming spans, not busy time or inferred provider
            // latency. Missing final updates leave an explicit open gap.
            if (!finished) await begin(s, activity, "inference", at);
            else await end(s, activity, at);
          }
        }, at);
      }
      const session = p?.sessionID;
      if (!validID(session)) return Promise.resolve();
      const request = p?.id ?? p?.requestID;
      const target = p?.tool?.callID;
      const status = p?.status?.type;
      const recognized = ["permission.asked", "permission.replied", "question.asked", "question.replied", "question.rejected", "session.status", "session.error"];
      if (!recognized.includes(type)) return Promise.resolve();
      return serial(async () => {
        const s = await ensure(session, at);
        if (type.startsWith("permission.") || type.startsWith("question.")) {
          if (!validID(request)) return;
          if (type.endsWith(".asked")) await begin(s, request, type.split(".")[0], at, validID(target) ? target : undefined);
          else await end(s, request, at);
        } else if (type === "session.status") {
          if (status === "retry") {
            if (!s.retry) s.retry = randomUUID();
            await begin(s, s.retry, "retry", at);
          } else if (s.retry && (status === "busy" || status === "idle")) {
            await end(s, s.retry, at);
            s.retry = undefined;
          }
          if (status === "busy") await gap(s, "uncertain_inference", at);
          // Idle is not a tool terminal event, observation loss, or issue
          // completion. Independently running tools can outlive this status.
        } else if (type === "session.error") {
          await gap(s, "cancelled", at);
        }
      }, at);
    },
    async dispose() {
      await tail;
      // No fabricated stops on unload: dangling starts are queryable unknowns.
      sessions.clear(); dispatches.clear(); childParents.clear();
    },
  };
}
