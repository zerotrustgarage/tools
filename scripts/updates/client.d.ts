export interface UpdateResult {
  tool: string;
  current: string;
  latest: string;
  status: 'available' | 'current' | 'newer' | 'development' | 'unreleased';
  release_url: string;
}
export function releasesURL(tool: string): string;
export function selectRelease(tool: string, current: string, releases: unknown[]): UpdateResult;
export function checkForUpdates(tool: string, current: string, options?: { fetcher?: typeof fetch; signal?: AbortSignal }): Promise<UpdateResult>;
export function showUpdateDialog(options: { tool: string; current: string; openExternal: (url: string) => void | Promise<void> }): void;
