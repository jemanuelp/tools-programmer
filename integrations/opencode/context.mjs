import { spawn } from "node:child_process";
import { isAbsolute } from "node:path";

const validID = (id) => typeof id === "string" && /^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,199}$/.test(id);
export const unavailableContext = () => ({ version: 1, project_resolution: "unavailable", assignment: "unknown", warnings: ["tracking_unavailable"] });

// Only the versioned allowlist crosses this subprocess boundary. Errors never
// include stderr, binary paths, database contents or runtime client payloads.
export function createContextLookup(binary, database, directory) {
  return async (runtime, session) => {
    if (![binary, database, directory].every((p) => typeof p === "string" && isAbsolute(p)) || !validID(runtime) || !validID(session)) return unavailableContext();
    try {
      const text = await new Promise((resolve, reject) => {
        const child = spawn(binary, ["--db", database, "activity", "context", "--runtime", runtime, "--session", session, "--directory", directory], {
          stdio: ["ignore", "pipe", "ignore"], shell: false, env: { PATH: process.env.PATH ?? "" },
        });
        const chunks = []; let bytes = 0;
        const timer = setTimeout(() => child.kill("SIGKILL"), 3000);
        child.stdout.on("data", (chunk) => { bytes += chunk.length; if (bytes > 8192) child.kill("SIGKILL"); else chunks.push(chunk); });
        child.on("error", () => { clearTimeout(timer); reject(new Error("unavailable")); });
        child.on("close", (code) => {
          clearTimeout(timer);
          if (code === 0 && bytes <= 8192) resolve(Buffer.concat(chunks).toString("utf8"));
          else reject(new Error("unavailable"));
        });
      });
      const data = JSON.parse(text).data;
      if (data?.version !== 1 || data.runtime !== runtime || data.session !== session ||
          !["matched", "ambiguous", "unavailable"].includes(data.project_resolution) ||
          !["assigned", "unassigned", "unknown"].includes(data.assignment) ||
          typeof data.observed_at !== "string" || !Number.isFinite(Date.parse(data.observed_at)) ||
          (data.segment !== undefined && (!validID(data.segment) || !Number.isSafeInteger(data.at) || data.at < 0)) ||
          (data.parent_session !== undefined && !validID(data.parent_session))) return unavailableContext();
      const result = { version: 1, runtime, session, observed_at: data.observed_at,
        project_resolution: data.project_resolution, assignment: data.assignment,
        warnings: Array.isArray(data.warnings) ? data.warnings.filter((w) => ["checkout_unavailable", "project_unavailable", "project_ambiguous", "session_unobserved", "assignment_project_mismatch"].includes(w)).slice(0, 5) : [],
      };
      if (data.project_resolution === "matched") {
        if (!validID(data.project) || typeof data.repository !== "string" || data.repository.length > 512 || !/^[a-z0-9_.\/-]+$/.test(data.repository)) return unavailableContext();
        result.project = data.project; result.repository = data.repository;
      }
      if (data.segment) { result.segment = data.segment; result.at = data.at; }
      if (data.parent_session) result.parent_session = data.parent_session;
      if (data.assignment === "assigned") {
        const i = data.issue;
        if (data.project_resolution !== "matched" || !data.segment || !Number.isSafeInteger(i?.id) || i.id <= 0 || !Number.isSafeInteger(i.number) || i.number <= 0 || i.number > 2147483647 ||
            typeof i.url !== "string" || (i.url !== "" && i.url !== `https://${data.repository}/issues/${i.number}`) || !["open", "completed", "cancelled", "reopened"].includes(i.outcome)) return unavailableContext();
        result.issue = { id: i.id, number: i.number, url: i.url, outcome: i.outcome };
      }
      return result;
    } catch { return unavailableContext(); }
  };
}
