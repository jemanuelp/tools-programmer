export interface NormalizedEvent {
  id: string;
  runtime: string;
  session: string;
  segment: string;
  kind: string;
  at: number;
  [key: string]: string | number | boolean | undefined;
}
export function createObserver(options: {
  record: (event: NormalizedEvent) => Promise<void>;
  clock?: () => number;
  runtime?: string;
  readParentDispatches?: (sessionID: string) => Promise<Array<{ callID: string; childSessionID: string }>>;
  readSessionState?: (sessionID: string) => Promise<import("./session-snapshot.mjs").SessionSnapshot>;
  lookupDispatch?: (runtime: string, sessionID: string, callID: string) => Promise<import("./transport.mjs").DispatchRecord | null>;
  readContext?: (runtime: string, sessionID: string) => Promise<import("./context.mjs").TrackingContext>;
}): {
  assign(context: { sessionID: string }, project: string, issue: number): Promise<string>;
  event(event: unknown): Promise<void>;
  before(input: unknown): Promise<void>;
  after(input: unknown, output: unknown): Promise<void>;
  dispose(): Promise<void>;
};
