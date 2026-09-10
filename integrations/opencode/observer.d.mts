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
}): {
  assign(context: { sessionID: string }, project: string, issue: number): Promise<string>;
  event(event: unknown): Promise<void>;
  before(input: unknown): Promise<void>;
  after(input: unknown, output: unknown): Promise<void>;
  dispose(): Promise<void>;
};
