import { randomUUID, createHash } from "node:crypto";

const idPattern = /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$/;
const validID = (value) => typeof value === "string" && idPattern.test(value);

// This module never receives a transport for raw runtime objects. Every call to
// record below constructs an explicit scalar allowlist. It does not log payloads.
export function createObserver({ record, clock = Date.now, runtime = randomUUID(), readParentDispatches, readSessionState, lookupDispatch }) {
  const sessions = new Map();
  const dispatches = new Map();
  const childParents = new Map();
  const freshChildren = new Set();
  const correlationAttempts = new Set();
  const generations = new Map();
  const terminals = new Map();
  const waitReceipts = new Map();
  const statusReceipts = new Map();
  const recoveryHooks = new Set();
  let receipt = 0;
  const received = (session, activity, terminal = false, invalidates = false) => {
    if (!validID(session)) return;
    const order = ++receipt;
    if (invalidates) generations.set(session, (generations.get(session) ?? 0) + 1);
    if (terminal && validID(activity)) {
      if (!terminals.has(session)) terminals.set(session, new Map());
      if (!terminals.get(session).has(activity)) terminals.get(session).set(activity, order);
    }
    return order;
  };
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
    const s = { id, segment: randomUUID(), at, active: new Map(), closed: new Set(), assignment,
      recoveryAttempted: !readSessionState, recoveryOK: !readSessionState, untargetedWaits: new Set() };
    await emit(s, { kind: "bind", at, ...assignment });
    sessions.set(id, s);
    return s;
  };

  const gap = (s, reason, at) => emit(s, { kind: "gap", at, reason });

  const restoreDispatch = async (parent, id, at, allowDurable = false) => {
    if (!readParentDispatches) return false;
    const pending = [...dispatches.entries()].filter(([key, d]) => key.startsWith(`${parent}\0`) && !d.child);
    const attempt = `${id}\0${pending.map(([key]) => key).sort().join("\0")}`;
    if ((!pending.length && (!lookupDispatch || !allowDurable)) || correlationAttempts.has(attempt)) return false;
    correlationAttempts.add(attempt);
    let snapshot;
    try { snapshot = await readParentDispatches(parent); } catch { return false; }
    const matches = new Set(snapshot.filter((item) => item.childSessionID === id && (!pending.length || pending.some(([key]) => key === `${parent}\0${item.callID}`))).map((item) => item.callID));
    if (matches.size !== 1) return false;
    const call = [...matches][0]; const key = `${parent}\0${call}`;
    let fromStore = false;
    if (!dispatches.has(key)) {
      if (!allowDurable) return false;
      let saved;
      try { saved = await lookupDispatch(runtime, parent, call); } catch { return false; }
      if (!saved || saved.runtime !== runtime || saved.session !== parent || saved.call_id !== call || !validID(saved.segment) || !Number.isSafeInteger(saved.at) || saved.at < 0 || saved.at > at) return false;
      dispatches.set(key, { segment: saved.segment, at: saved.at });
      fromStore = true;
    }
    const before = sessions.get(id);
    // Persistence failures propagate; no fallback after a partial local write.
    await correlate(parent, call, id, at);
    if (fromStore && sessions.get(id) !== before) await gap(sessions.get(id), "reconnected", at);
    return sessions.get(id) !== before;
  };

  const ensure = async (id, at) => {
    let s = sessions.get(id);
    if (readParentDispatches && childParents.has(id) && (!s || (s.observedIdle && s.active.size === 0))) {
      const parent = childParents.get(id);
      const pending = [...dispatches.entries()].filter(([key, d]) => key.startsWith(`${parent}\0`) && !d.child);
      if (await restoreDispatch(parent, id, at, freshChildren.has(id))) s = sessions.get(id);
      else if (s && pending.length) {
        s = await bind(id, at);
        await gap(s, "uncorrelated_child", at);
      }
    }
    freshChildren.delete(id);
    if (!s) {
      s = await bind(id, at, { resume: true });
      await gap(s, childParents.has(id) ? "uncorrelated_child" : "observer_restart", at);
    }
    if (s.lossAt !== undefined) {
      await gap(s, "event_loss", Math.max(s.at, s.lossAt));
      s.lossAt = undefined;
      if (readSessionState && at > s.at) {
        s = await bind(id, at, s.assignment.parent_session ? s.assignment : { resume: true });
        await gap(s, "reconnected", at);
      }
    }
    if (!s.recoveryAttempted) s = await recover(s);
    return s;
  };

  const terminalBeforeSnapshot = (s, activity) => {
    const order = terminals.get(s.id)?.get(activity);
    return order !== undefined && order <= s.recoveredThrough;
  };

  const begin = async (s, activity, category, at, target, fromSnapshot = false) => {
    if (!validID(activity) || (target && !validID(target))) return;
    // Receive-ahead tombstones protect snapshot imports, not ordered live
    // starts whose corresponding stop is merely waiting behind persistence.
    if (fromSnapshot && terminalBeforeSnapshot(s, activity)) return;
    if (["tool", "inference", "retry"].includes(category) && s.attributionUnknown) return;
    if ((category === "tool" || category === "inference") && (!s.recoveryOK || s.untargetedWaits.size)) return;
    if (category === "inference" && readSessionState && !s.inferenceKnown) return;
    if (s.active.has(activity) || s.closed.has(activity)) return;
    const fields = { activity, category, ...(target ? { target } : {}) };
    await emit(s, { kind: "start", at, ...fields });
    s.active.set(activity, { ...fields, startAt: at });
  };

  const end = async (s, activity, at) => {
    s.untargetedWaits.delete(activity);
    const active = s.active.get(activity);
    if (!active) {
      if (!s.closed.has(activity)) {
        await gap(s, "unsupported_lifecycle", at);
        s.closed.add(activity);
      }
      return;
    }
    const { startAt, ...fields } = active;
    await emit(s, { kind: "stop", at: Math.max(at, startAt), ...fields });
    s.active.delete(activity);
    s.closed.add(activity);
  };

  const recover = async (s) => {
    s.recoveryAttempted = true;
    const generation = generations.get(s.id);
    let snapshot;
    try {
      snapshot = await readSessionState(s.id);
      if (generation !== generations.get(s.id)) throw new Error("Concurrent runtime observations");
    } catch {
      s.recoveryOK = false;
      await gap(s, "unsupported_lifecycle", Math.max(s.at, clock()));
      return s;
    }
    s.allowDurableProvenance = snapshot.singleTurn === true;
    if (snapshot.parentSessionID && !s.assignment.parent_session && lookupDispatch && s.allowDurableProvenance) {
      childParents.set(s.id, snapshot.parentSessionID);
      if (await restoreDispatch(snapshot.parentSessionID, s.id, clock(), true)) s = sessions.get(s.id);
      s.recoveryAttempted = true;
      if (generation !== generations.get(s.id)) {
        s.recoveryOK = false;
        await gap(s, "unsupported_lifecycle", Math.max(s.at, clock()));
        return s;
      }
    }
    const at = Math.max(s.at, clock());
    s.recoveredAt = at;
    s.recoveredThrough = receipt;
    s.recoveredActive = snapshot.status === "busy" || snapshot.status === "retry";
    s.recoveryOK = true;
    if (snapshot.parentSessionID && !s.assignment.parent_session) {
      childParents.set(s.id, snapshot.parentSessionID);
      s.attributionUnknown = true;
      await gap(s, "uncorrelated_child", at);
    }
    s.inferenceKnown = snapshot.status !== "unknown";
    for (const id of snapshot.closed) s.closed.add(id);
    for (const wait of snapshot.waits) {
      if (terminalBeforeSnapshot(s, wait.activity)) continue;
      if (!wait.target) s.untargetedWaits.add(wait.activity);
      await begin(s, wait.activity, wait.category, at, wait.target, true);
    }
    if (snapshot.status === "retry") {
      s.retry = randomUUID();
      await begin(s, s.retry, "retry", at);
    }
    if (snapshot.status === "busy" || snapshot.status === "retry") {
      for (const tool of snapshot.running) await begin(s, tool.activity, tool.category, at, undefined, true);
    } else if (snapshot.running.length) {
      // A persisted running part is not proof that a process-local job
      // survived a server restart. Require a currently active session status.
      await gap(s, "unsupported_lifecycle", at);
    }
    return s;
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
    if (previous?.active.size) {
      if (!d.ambiguous) await gap(previous, "uncorrelated_child", at);
      previous.attributionUnknown = true;
      d.ambiguous = true;
      return;
    }
    const child = await bind(childID, at, {
      parent_session: parentID,
      parent_segment: d.segment,
      dispatch_at: d.at,
    });
    d.child = childID;
    childParents.set(childID, parentID);
    if (previous) await gap(child, "uncorrelated_child", at);
  };

  return {
    assign(context, project, issue) {
      const at = clock();
      const session = context.sessionID;
      received(session, undefined, false, true);
      // Only the plugin wrapper calls this with the actual ToolContext. There
      // is deliberately no session argument in the model-facing tool schema.
      return serial(async () => {
        if (!validID(session)) throw new Error("Missing authoritative session context");
        const previous = sessions.has(session) ? await ensure(session, at) : undefined;
        if (previous && at <= previous.at) throw new Error("Assignment timestamp has not advanced; retry explicitly");
        const running = previous ? [...previous.active.values()] : [];
        const s = await bind(session, at, { project, issue });
        if (previous) {
          s.recoveryAttempted = previous.recoveryAttempted;
          s.recoveryOK = previous.recoveryOK;
          s.inferenceKnown = previous.inferenceKnown;
          s.untargetedWaits = new Set(previous.untargetedWaits);
        }
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
      if (!validID(input.sessionID) || !validID(input.callID) || input.tool === "tools_programmer_assign") return Promise.resolve();
      const session = input.sessionID;
      const call = input.callID;
      const task = input.tool === "task";
      received(session);
      const recoveryKey = `${session}\0${call}`;
      const freshHook = !recoveryHooks.has(recoveryKey);
      recoveryHooks.add(recoveryKey);
      return serial(async () => {
        const previous = sessions.get(session);
        // One attempt at an actual new execution boundary, never per update,
        // duplicate hook, timeout loop, or inferred heartbeat activity.
        if (freshHook && previous && !previous.recoveryOK) previous.recoveryAttempted = false;
        const s = await ensure(session, at);
        if (task && !dispatches.has(`${session}\0${call}`)) {
          const marker = "dispatch-" + createHash("sha256").update(`${runtime}\0${session}\0${call}`).digest("hex");
          // An instantaneous delegation marker uses existing immutable event
          // storage. It adds no active time and survives observer-memory loss.
          for (const kind of ["start", "stop"]) await emit(s, { id: `${marker}-${kind}`, kind, at, activity: marker, category: "delegation" });
          dispatches.set(`${session}\0${call}`, { segment: s.segment, at });
        }
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
        if (validID(id) && validID(parent) && !childParents.has(id)) { childParents.set(id, parent); freshChildren.add(id); }
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
        const order = received(session, activity, partType === "tool" ? status === "completed" || status === "error" : finished);
        return serial(async () => {
          const s = await ensure(session, at);
          const predates = s.recoveredThrough !== undefined && order <= s.recoveredThrough;
          if (predates && terminalBeforeSnapshot(s, activity) && !s.active.has(activity)) return;
          if (predates && !s.recoveredActive) return;
          const startAt = Math.max(at, s.at, s.recoveredAt ?? s.at);
          const endAt = Math.max(at, s.at);
          if (partType === "tool") {
            if (isTask) await correlate(session, activity, child, at);
            if (status === "running") await begin(s, activity, category, startAt);
            if (status === "completed" || status === "error") await end(s, activity, endAt);
          } else if (partType === "text" || partType === "reasoning") {
            // Only observed streaming spans, not busy time or inferred provider
            // latency. Missing final updates leave an explicit open gap.
            if (!finished) await begin(s, activity, "inference", startAt);
            else await end(s, activity, endAt);
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
      let invalidates = false;
      const isWait = type.startsWith("permission.") || type.startsWith("question.");
      if (isWait && validID(request)) {
        const key = `${session}\0${request}`;
        const next = type.endsWith(".asked") ? `${type.split(".")[0]}:${validID(target) ? target : ""}` : "closed";
        const previous = waitReceipts.get(key);
        invalidates = previous !== "closed" && previous !== next;
        if (invalidates) waitReceipts.set(key, next);
      } else if (type === "session.status" && ["busy", "idle", "retry"].includes(status)) {
        const previous = statusReceipts.get(session);
        invalidates = previous !== status;
        statusReceipts.set(session, status);
      } else if (type === "session.error") invalidates = true;
      const order = received(session, request, type.endsWith(".replied") || type.endsWith(".rejected"), invalidates);
      return serial(async () => {
        const s = await ensure(session, at);
        if (type.startsWith("permission.") || type.startsWith("question.")) {
          if (!validID(request)) return;
          if (type.endsWith(".asked")) await begin(s, request, type.split(".")[0], Math.max(at, s.at), validID(target) ? target : undefined);
          else await end(s, request, Math.max(at, s.at));
        } else if (type === "session.status") {
          if (s.recoveredThrough !== undefined && order <= s.recoveredThrough) return;
          s.observedIdle = status === "idle";
          if (status === "retry") {
            if (!s.retry) s.retry = randomUUID();
            await begin(s, s.retry, "retry", at);
          } else if (s.retry && (status === "busy" || status === "idle")) {
            await end(s, s.retry, at);
            s.retry = undefined;
          }
          if (status === "busy") await gap(s, "uncertain_inference", at);
          if (status === "busy" && s.recoveryOK) s.inferenceKnown = true;
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
      sessions.clear(); dispatches.clear(); childParents.clear(); freshChildren.clear(); correlationAttempts.clear(); generations.clear(); terminals.clear(); waitReceipts.clear(); statusReceipts.clear(); recoveryHooks.clear();
    },
  };
}
