export interface TrackingContext {
  version: number;
  runtime?: string;
  session?: string;
  observed_at?: string;
  project_resolution: string;
  project?: string;
  repository?: string;
  assignment: string;
  segment?: string;
  at?: number;
  parent_session?: string;
  issue?: { id: number; number: number; url: string; outcome: string };
  warnings: string[];
  diagnostics?: import("./diagnostics.mjs").IntegrationDiagnostics;
}
export function unavailableContext(): TrackingContext;
export function createContextLookup(binary: string, database: string, directory: string): (runtime: string, session: string) => Promise<TrackingContext>;
