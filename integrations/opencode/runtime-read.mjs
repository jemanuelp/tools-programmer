const idPattern = /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$/;
export const validRuntimeID = (id) => typeof id === "string" && idPattern.test(id);

// Version-pinned bridge to 1.18.29's generated client's transport. The public
// v1 facade lacks permission/question GETs. Capability-check the private seam,
// preserve its configured fetch and headers, and never reconfigure the client.
export async function withRuntimeRead(client, directory, operation) {
  const transport = client?._client;
  if (!transport?.get || !transport?.getConfig || typeof directory !== "string" || !directory) throw new Error("Unsupported runtime read transport");
  const fetchTransport = transport.getConfig().fetch ?? globalThis.fetch;
  const controller = new AbortController();
  let reader; let timer; let total = 0; let requests = 0;
  const deadline = new Promise((_, reject) => {
    timer = setTimeout(() => {
      controller.abort(); void reader?.cancel().catch(() => {});
      reject(new Error("Runtime read deadline exceeded"));
    }, 3000);
  });
  const read = async (kind, session) => {
    if (!validRuntimeID(session) || ++requests > 9 || controller.signal.aborted) throw new Error("Runtime read bound exceeded");
    const paths = { messages: "/session/{id}/message", session: "/session/{id}", permissions: "/permission", questions: "/question", status: "/session/status" };
    if (!Object.hasOwn(paths, kind)) throw new Error("Unsupported runtime read");
    const result = await transport.get({
      url: paths[kind], path: { id: session }, query: { directory, ...(kind === "messages" ? { limit: 20 } : {}) },
      parseAs: "stream", responseStyle: "fields", signal: controller.signal, redirect: "error",
      fetch: async (request) => {
        const response = await fetchTransport(request);
        // Cancel before the generated SDK's unbounded error-body parser.
        if (!response.ok || controller.signal.aborted) {
          void response.body?.cancel().catch(() => {});
          throw new Error("Runtime response unavailable");
        }
        return response;
      },
    });
    if (controller.signal.aborted) { void result?.data?.cancel?.().catch(() => {}); throw new Error("Runtime read expired"); }
    if (!result?.data?.getReader) throw new Error("Runtime stream unavailable");
    reader = result.data.getReader(); const chunks = []; let size = 0;
    for (;;) {
      const { value, done } = await reader.read(); if (done) break;
      size += value.byteLength; total += value.byteLength;
      if (size > 1024 * 1024 || total > 2 * 1024 * 1024) throw new Error("Runtime body exceeds bound");
      chunks.push(value);
    }
    reader.releaseLock(); reader = undefined;
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    return JSON.parse(new TextDecoder().decode(bytes));
  };
  try { return await Promise.race([operation(read), deadline]); }
  catch { throw new Error("Runtime snapshot incomplete or unavailable"); }
  finally { clearTimeout(timer); controller.abort(); void reader?.cancel().catch(() => {}); }
}

export function messageParts(rows, session) {
  if (!Array.isArray(rows) || rows.length >= 20) throw new Error("Incomplete messages");
  const parts = [];
  for (const row of rows) {
    if (row?.info?.sessionID !== session || !Array.isArray(row.parts)) throw new Error("Wrong session messages");
    if (parts.length + row.parts.length > 256) throw new Error("Too many parts");
    parts.push(...row.parts);
  }
  return parts;
}
