export interface IntegrationDiagnostics {
  status: string;
  warnings: string[];
  observed_at: string;
  host_version?: string;
  package_version?: string;
  binary?: { version: string; commit: string; state: string; build: string };
}
export function readBundle(base?: URL): Promise<string>;
export function readBinary(binary: string): Promise<unknown>;
export function createDiagnostics(options: { binary: string; client: unknown; directory: string }): (session?: string) => Promise<IntegrationDiagnostics>;
