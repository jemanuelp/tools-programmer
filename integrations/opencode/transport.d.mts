import type { NormalizedEvent } from "./observer.mjs";
export function createRecorder(binary: string, database: string): (event: NormalizedEvent) => Promise<void>;
