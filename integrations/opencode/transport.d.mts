import type { NormalizedEvent } from "./observer.mjs";
export function createRecorder(binary: string, database: string): (event: NormalizedEvent) => Promise<void>;
export type DispatchRecord = { runtime: string; session: string; call_id: string; segment: string; at: number };
export function createDispatchLookup(binary: string, database: string): (runtime: string, session: string, call: string) => Promise<DispatchRecord | null>;
