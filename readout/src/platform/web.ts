import { LISTED_EXTENSIONS, resolveInFolder, skipFolder } from '../core/paths';
import type { FileRef, Listing, OpenedFile, Platform } from './types';

// Browser build. Files never leave the machine: they are read and written locally through the
// File System Access API (Chromium browsers) or through file inputs and downloads elsewhere.
// There is no server component.

const MARKDOWN_TYPES = [
  { description: 'Markdown', accept: { 'text/markdown': ['.md', '.markdown', '.mdown', '.mkd'] } },
  { description: 'Text', accept: { 'text/plain': ['.txt'] } },
];
const MAX_LISTED = 20_000;
const MAX_VISITED = 250_000;

type PickerWindow = Window & {
  showOpenFilePicker?: (options: object) => Promise<FileSystemFileHandle[]>;
  showSaveFilePicker?: (options: object) => Promise<FileSystemFileHandle>;
  showDirectoryPicker?: (options: object) => Promise<FileSystemDirectoryHandle>;
};
type IterableDirectory = FileSystemDirectoryHandle & {
  values(): AsyncIterable<FileSystemFileHandle | FileSystemDirectoryHandle>;
};
const picker = window as PickerWindow;
const cancelled = (error: unknown) => error instanceof DOMException && error.name === 'AbortError';

function download(bytes: Uint8Array, name: string, type: string): void {
  const url = URL.createObjectURL(new Blob([bytes as BlobPart], { type }));
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

async function writeHandle(handle: FileSystemFileHandle, bytes: Uint8Array): Promise<void> {
  const writable = await handle.createWritable();
  await writable.write(bytes as BlobPart);
  await writable.close();
}

function pickWithInput(directory: boolean): Promise<File[] | null> {
  return new Promise((resolve) => {
    const input = document.createElement('input');
    input.type = 'file';
    if (directory) input.webkitdirectory = true;
    else input.accept = '.md,.markdown,.mdown,.mkd,.txt,text/markdown,text/plain';
    input.addEventListener('change', () => resolve(input.files ? [...input.files] : null));
    input.addEventListener('cancel', () => resolve(null));
    input.click();
  });
}

async function walk(root: FileSystemDirectoryHandle): Promise<{ files: string[]; truncated: boolean }> {
  const files: string[] = [];
  let visited = 0;
  const stack: [FileSystemDirectoryHandle, string][] = [[root, '']];
  while (stack.length) {
    const [dir, prefix] = stack.pop()!;
    for await (const entry of (dir as IterableDirectory).values()) {
      if (++visited > MAX_VISITED || files.length >= MAX_LISTED) return { files: sortPaths(files), truncated: true };
      const path = prefix + entry.name;
      if (entry.kind === 'directory') {
        if (!skipFolder(entry.name)) stack.push([entry as FileSystemDirectoryHandle, `${path}/`]);
      } else if (LISTED_EXTENSIONS.test(entry.name)) files.push(path);
    }
  }
  return { files: sortPaths(files), truncated: false };
}

const sortPaths = (files: string[]) => files.sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()));

async function fileHandleAt(root: FileSystemDirectoryHandle, folderPath: string): Promise<FileSystemFileHandle> {
  const parts = folderPath.split('/');
  let dir = root;
  for (const part of parts.slice(0, -1)) dir = await dir.getDirectoryHandle(part);
  return dir.getFileHandle(parts.at(-1)!);
}

async function folderFile(listing: Listing, folderPath: string): Promise<{ file: File; handle?: FileSystemFileHandle }> {
  const { handle, entries } = listing.folder;
  if (handle) {
    const fileHandle = await fileHandleAt(handle, folderPath);
    return { file: await fileHandle.getFile(), handle: fileHandle };
  }
  const file = entries?.get(folderPath);
  if (!file) throw new Error(`${folderPath} is not in the open folder`);
  return { file };
}

export const webPlatform: Platform = {
  kind: 'web',

  async open() {
    if (!picker.showOpenFilePicker) {
      const file = (await pickWithInput(false))?.[0];
      return file ? { ref: { name: file.name }, bytes: new Uint8Array(await file.arrayBuffer()) } : null;
    }
    try {
      const [handle] = await picker.showOpenFilePicker({ types: MARKDOWN_TYPES, multiple: false });
      if (!handle) return null;
      const file = await handle.getFile();
      return { ref: { name: file.name, handle }, bytes: new Uint8Array(await file.arrayBuffer()) };
    } catch (error) {
      if (cancelled(error)) return null;
      throw error;
    }
  },

  async openFolder() {
    if (picker.showDirectoryPicker) {
      try {
        const handle = await picker.showDirectoryPicker({ id: 'ztg-readout', mode: 'read' });
        return { folder: { name: handle.name, handle }, ...(await walk(handle)) };
      } catch (error) {
        if (cancelled(error)) return null;
        throw error;
      }
    }
    // Other browsers: a folder upload. Files are read-only; saving downloads a copy.
    const picked = await pickWithInput(true);
    if (!picked?.length) return null;
    const entries = new Map<string, File>();
    let name = 'Folder';
    for (const file of picked) {
      const [top = '', ...rest] = file.webkitRelativePath.split('/');
      name = top || name;
      if (rest.slice(0, -1).some(skipFolder)) continue;
      entries.set(rest.join('/'), file);
    }
    const files = sortPaths([...entries.keys()].filter((p) => LISTED_EXTENSIONS.test(p)));
    return { folder: { name, entries }, files, truncated: false };
  },

  async readFolderFile(listing, folderPath) {
    const { file, handle } = await folderFile(listing, folderPath);
    return { ref: { name: file.name, handle, folderPath }, bytes: new Uint8Array(await file.arrayBuffer()) };
  },

  // A lone file opened in a browser grants no access to its folder.
  async followLink() {
    return null;
  },

  async reopen(ref) {
    if (!ref.handle) return null;
    const file = await ref.handle.getFile();
    return { ref, bytes: new Uint8Array(await file.arrayBuffer()) };
  },

  async save(ref, bytes, suggestedName) {
    if (!ref?.handle) return this.saveAs(bytes, ref?.name ?? suggestedName);
    await writeHandle(ref.handle, bytes);
    return ref;
  },

  async saveAs(bytes, suggestedName) {
    if (!picker.showSaveFilePicker) {
      download(bytes, suggestedName, 'text/markdown');
      return { name: suggestedName };
    }
    try {
      const handle = await picker.showSaveFilePicker({ suggestedName, types: MARKDOWN_TYPES });
      await writeHandle(handle, bytes);
      return { name: handle.name, handle } satisfies FileRef;
    } catch (error) {
      if (cancelled(error)) return null;
      throw error;
    }
  },

  async exportFile(bytes, suggestedName) {
    download(bytes, suggestedName, 'text/html');
  },

  async openExternal(url) {
    window.open(url, '_blank', 'noopener,noreferrer');
  },

  async loadLocalImage(ref, relative, listing) {
    if (!listing || ref?.folderPath === undefined) return null;
    const path = resolveInFolder(ref.folderPath, relative);
    if (path === null) return null;
    try {
      const { file } = await folderFile(listing, path);
      return URL.createObjectURL(file);
    } catch {
      return null;
    }
  },

  async initial() {
    return null;
  },

  onDrop(handler) {
    window.addEventListener('dragover', (event) => event.preventDefault());
    window.addEventListener('drop', async (event) => {
      event.preventDefault();
      const file = event.dataTransfer?.files[0];
      if (!file) return;
      handler({ file: { ref: { name: file.name }, bytes: new Uint8Array(await file.arrayBuffer()) } });
    });
  },

  // A page cannot show its own dialog while unloading; the browser's prompt is used instead,
  // whenever the app has marked the document as having unsaved changes.
  onCloseRequested() {
    window.addEventListener('beforeunload', (event) => {
      if (document.body.dataset.dirty === 'true') event.preventDefault();
    });
  },

  setTitle(title) {
    document.title = title;
  },
};
