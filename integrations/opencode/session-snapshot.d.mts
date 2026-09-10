export type SessionSnapshot = {
  parentSessionID?: string;
  singleTurn?: boolean;
  running: Array<{ activity: string; category: string }>;
  closed: string[];
  waits: Array<{ activity: string; category: string; target?: string }>;
  status: "busy" | "idle" | "retry" | "unknown";
};
export function createSessionSnapshotReader(client: unknown, directory: string, projectID: string): (sessionID: string) => Promise<SessionSnapshot>;
