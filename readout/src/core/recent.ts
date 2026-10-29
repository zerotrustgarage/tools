// The recent-files list as data: ordering, the cap, and reading a stored list that may be
// damaged. The desktop's Rust side keeps its own list under the same rules (src-tauri/src/recent.rs);
// the browser build keeps file handles with these functions (platform/recent-web.ts).

/** How many documents the list keeps. */
export const MAX_RECENT = 10;

/** A stored line: the handle itself cannot be written as text, so only IndexedDB holds it. */
export interface StoredRecent<H> {
  id: string;
  name: string;
  detail?: string;
  handle: H;
}

/** Puts entry first, drops an older line with the same id, and keeps the newest max. */
export function pushRecent<T extends { id: string }>(list: readonly T[], entry: T, max = MAX_RECENT): T[] {
  return [entry, ...list.filter((e) => e.id !== entry.id)].slice(0, max);
}

export function removeRecent<T extends { id: string }>(list: readonly T[], id: string): T[] {
  return list.filter((e) => e.id !== id);
}

/**
 * Reads what storage returned. Anything that is not a list, and every line that is not a usable
 * entry (no id, no name, no handle), is dropped; repeated ids keep the first (newest); the cap
 * applies. Never throws, so damaged storage only ever means a shorter list.
 */
export function parseRecent<H>(raw: unknown, isHandle: (value: unknown) => value is H, max = MAX_RECENT): StoredRecent<H>[] {
  if (!Array.isArray(raw)) return [];
  const seen = new Set<string>();
  const out: StoredRecent<H>[] = [];
  for (const item of raw as unknown[]) {
    if (out.length >= max) break;
    if (typeof item !== 'object' || item === null) continue;
    const { id, name, detail, handle } = item as Record<string, unknown>;
    if (typeof id !== 'string' || id === '' || typeof name !== 'string' || name === '' || seen.has(id) || !isHandle(handle)) continue;
    seen.add(id);
    out.push({ id, name, ...(typeof detail === 'string' && detail !== '' ? { detail } : {}), handle });
  }
  return out;
}
