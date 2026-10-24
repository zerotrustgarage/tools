import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';
import { getCurrentWindow } from '@tauri-apps/api/window';
import type { Arrival, FileRef, Listing, OpenedFile, Platform } from './types';

// The WebView has no file system, dialog or shell permission. Every file operation is a Rust
// command that only touches paths the user chose through a native dialog, a file association
// or a drop (src-tauri/src/lib.rs keeps that allow-list and resolves every relative path).

const baseName = (path: string) => path.split(/[\\/]/).pop() ?? path;
const SEPARATOR = /[\\/]/;

interface RustListing {
  root: string;
  name: string;
  files: string[];
  truncated: boolean;
}
interface RustInitial {
  path: string;
  folder: boolean;
  /** A restored session's document inside the folder. */
  document: string | null;
}

/** Joins a folder-relative path onto the root with the platform's separator. */
function inRoot(root: string, folderPath: string): string {
  const sep = root.includes('\\') ? '\\' : '/';
  return root.replace(/[\\/]+$/, '') + sep + folderPath.split('/').join(sep);
}

/** The folder-relative path of an absolute path under root, if it is under root. */
function folderPathOf(root: string | undefined, path: string): string | undefined {
  if (!root) return undefined;
  const r = root.replace(/[\\/]+$/, '').split(SEPARATOR);
  const p = path.split(SEPARATOR);
  const windows = root.includes('\\');
  const same = (a: string, b: string) => (windows ? a.toLowerCase() === b.toLowerCase() : a === b);
  if (p.length <= r.length || !r.every((seg, i) => same(seg, p[i]!))) return undefined;
  return p.slice(r.length).join('/');
}

async function read(path: string, root?: string): Promise<OpenedFile> {
  const buffer = await invoke<ArrayBuffer>('read_document', { path });
  return { ref: { name: baseName(path), path, folderPath: folderPathOf(root, path) }, bytes: new Uint8Array(buffer) };
}

async function list(root: string): Promise<Listing> {
  const listing = await invoke<RustListing>('list_folder', { root });
  return { folder: { name: listing.name, root: listing.root }, files: listing.files, truncated: listing.truncated };
}

async function arrival(initial: RustInitial | null): Promise<Arrival | null> {
  if (!initial) return null;
  if (!initial.folder) return { file: await read(initial.path) };
  const listing = await list(initial.path);
  // A session's document that cannot be read any more leaves the folder on its README.
  const file = initial.document ? await read(initial.document, listing.folder.root).catch(() => undefined) : undefined;
  return { listing, file };
}

const writeVia = (command: string, bytes: Uint8Array, headers: Record<string, string>) =>
  invoke<string | null>(command, bytes, { headers });

const IMAGE_TYPES: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  bmp: 'image/bmp',
  ico: 'image/x-icon',
  avif: 'image/avif',
};

const appWindow = getCurrentWindow();

export const desktopPlatform: Platform = {
  kind: 'desktop',

  window: {
    minimize: () => appWindow.minimize(),
    toggleMaximize: () => appWindow.toggleMaximize(),
    close: () => appWindow.close(),
    onMaximizedChange(handler) {
      const report = () => void appWindow.isMaximized().then(handler, () => undefined);
      report();
      void appWindow.onResized(report);
    },
  },

  async open() {
    const path = await invoke<string | null>('choose_document');
    return path ? read(path) : null;
  },

  async openFolder() {
    const root = await invoke<string | null>('choose_folder');
    return root ? list(root) : null;
  },

  // list_folder accepts only a root the user granted, so polling cannot leave the folder.
  async relist(listing) {
    return listing.folder.root ? list(listing.folder.root) : null;
  },

  // options.maxBytes is accepted but not applied here: the listing carries no sizes and the Rust
  // side is unchanged, so the only cap is read_limited's 64 MiB in lib.rs. Callers that pass a
  // smaller bound must still check the returned length (searchFolder does).
  async readFolderFile(listing, folderPath, _options) {
    const root = listing.folder.root!;
    return read(inRoot(root, folderPath), root);
  },

  async followLink(from, target) {
    if (!from.path) return null;
    const path = await invoke<string | null>('resolve_link', { document: from.path, target });
    return path ? read(path) : null;
  },

  async reopen(ref) {
    return ref.path ? read(ref.path) : null;
  },

  async save(ref, bytes, suggestedName) {
    if (!ref?.path) return this.saveAs(bytes, suggestedName);
    await writeVia('save_document', bytes, { 'x-ztg-path': encodeURIComponent(ref.path) });
    return ref;
  },

  async saveAs(bytes, suggestedName) {
    const path = await writeVia('save_document_as', bytes, { 'x-ztg-name': encodeURIComponent(suggestedName) });
    return path ? { name: baseName(path), path } : null;
  },

  async exportFile(bytes, suggestedName) {
    await writeVia('export_html', bytes, { 'x-ztg-name': encodeURIComponent(suggestedName) });
  },

  async openExternal(url) {
    await invoke('open_external', { url });
  },

  async loadLocalImage(ref: FileRef | null, relative: string) {
    if (!ref?.path) return null;
    const buffer = await invoke<ArrayBuffer>('read_image', { document: ref.path, relative });
    const ext = relative.split('.').pop()?.toLowerCase() ?? '';
    const type = IMAGE_TYPES[ext];
    // SVG is shown through <img>, where scripts inside it never run.
    return type ? URL.createObjectURL(new Blob([buffer], { type })) : null;
  },

  async initial() {
    return arrival(await invoke<RustInitial | null>('initial_document'));
  },

  // Rust keeps the session and accepts only paths it has already granted.
  remember(listing, ref) {
    void invoke('remember_session', { folder: listing?.folder.root ?? null, document: ref?.path ?? null }).catch(
      () => undefined,
    );
  },

  onDrop(handlers) {
    void listen<boolean>('document-drag', (event) => handlers.dragging(event.payload));
    void listen<string>('document-drop-refused', (event) => handlers.refused(event.payload));
    void listen<RustInitial>('document-dropped', async (event) => {
      const arrived = await arrival(event.payload);
      if (arrived) handlers.open(arrived);
    });
  },

  onCloseRequested(handler) {
    void appWindow.onCloseRequested(async (event) => {
      if (!(await handler())) event.preventDefault();
    });
  },

  setTitle(title) {
    void appWindow.setTitle(title);
  },
};
