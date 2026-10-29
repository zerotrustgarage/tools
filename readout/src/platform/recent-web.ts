import { MAX_RECENT, parseRecent, pushRecent, removeRecent, type StoredRecent } from '../core/recent';
import type { FileRef, Listing, OpenedFile, RecentEntry, RecentFiles } from './types';

// The browser's recent-files list. Only a file handle from the File System Access API can be
// opened again later, so a file without one (an upload, a drop, a download-only save) is not kept,
// and a browser without that API has no list at all. The handles live in IndexedDB beside the
// session; access to one is asked for again, with the click that opens it.

/** Where the list is kept. IndexedDB in the page; memory in tests. */
export interface RecentBackend {
  read(): Promise<unknown>;
  write(value: unknown): Promise<void>;
}

/** What the store needs from a handle, so the logic runs without a browser. */
export interface HandleOps {
  isHandle(value: unknown): value is FileSystemFileHandle;
  same(a: FileSystemFileHandle, b: FileSystemFileHandle): Promise<boolean>;
  /** The file; null when it no longer exists; throws when access is refused. */
  read(handle: FileSystemFileHandle): Promise<File | null>;
  /** The '/'-separated path of handle inside folder, or null when it is not inside. */
  within(folder: FileSystemDirectoryHandle, handle: FileSystemFileHandle): Promise<string | null>;
}

type PermissionHandle = FileSystemFileHandle & {
  queryPermission?(options: { mode: 'read' }): Promise<PermissionState>;
  requestPermission?(options: { mode: 'read' }): Promise<PermissionState>;
};

export const browserHandleOps: HandleOps = {
  isHandle: (value): value is FileSystemFileHandle =>
    typeof value === 'object' && value !== null && typeof (value as { getFile?: unknown }).getFile === 'function',
  same: (a, b) => a.isSameEntry(b),
  async read(handle) {
    const h = handle as PermissionHandle;
    if ((await h.queryPermission?.({ mode: 'read' })) !== 'granted' && (await h.requestPermission?.({ mode: 'read' })) !== 'granted') {
      throw new Error('Access to that file was not granted.');
    }
    try {
      return await handle.getFile();
    } catch (error) {
      if (error instanceof DOMException && error.name === 'NotFoundError') return null;
      throw error;
    }
  },
  async within(folder, handle) {
    const parts = await folder.resolve(handle);
    return parts && parts.length > 0 ? parts.join('/') : null;
  },
};

const newId = () => globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
const dirOf = (folderPath: string | undefined) => (folderPath && folderPath.includes('/') ? folderPath.slice(0, folderPath.lastIndexOf('/')) : undefined);

export function createRecentFiles(backend: RecentBackend, ops: HandleOps = browserHandleOps, max = MAX_RECENT): RecentFiles {
  const load = async (): Promise<StoredRecent<FileSystemFileHandle>[]> => parseRecent(await backend.read().catch(() => undefined), ops.isHandle, max);
  // Reads and writes of one list never interleave, or two quick opens could drop one.
  let queue: Promise<unknown> = Promise.resolve();
  const serial = <T>(task: () => Promise<T>): Promise<T> => {
    const run = queue.then(task, task);
    queue = run.catch(() => undefined);
    return run;
  };
  const entry = ({ id, name, detail }: StoredRecent<FileSystemFileHandle>): RecentEntry => ({ id, name, ...(detail ? { detail } : {}) });

  return {
    list: () => serial(async () => (await load()).map(entry)).catch(() => []),

    add: (ref: FileRef) =>
      serial(async () => {
        if (!ref.handle || !ops.isHandle(ref.handle)) return;
        const list = await load();
        // The same file reached another way (the picker, then a folder) is one line.
        let id: string | undefined;
        for (const known of list) {
          if (await ops.same(known.handle, ref.handle).catch(() => false)) {
            id = known.id;
            break;
          }
        }
        const detail = dirOf(ref.folderPath);
        const next = pushRecent(list, { id: id ?? newId(), name: ref.name, ...(detail ? { detail } : {}), handle: ref.handle }, max);
        await backend.write(next);
      }).catch(() => undefined),

    async open(id: string, listing: Listing | null): Promise<OpenedFile | null> {
      const found = (await serial(load)).find((e) => e.id === id);
      if (!found) return null;
      const file = await ops.read(found.handle);
      if (!file) return null;
      const folder = listing?.folder.handle;
      const folderPath = folder ? ((await ops.within(folder, found.handle).catch(() => null)) ?? undefined) : undefined;
      return { ref: { name: file.name, handle: found.handle, folderPath }, bytes: new Uint8Array(await file.arrayBuffer()) };
    },

    remove: (id) => serial(async () => backend.write(removeRecent(await load(), id))).catch(() => undefined),

    clear: () => serial(() => backend.write([])).catch(() => undefined),
  };
}
