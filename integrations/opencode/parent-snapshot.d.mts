export type ParentDispatch = { callID: string; childSessionID: string };
export function createParentSnapshotReader(client: unknown, directory: string): (sessionID: string) => Promise<ParentDispatch[]>;
