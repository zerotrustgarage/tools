// What the editor needs from its host. The desktop build talks to the Tauri commands in
// src-tauri/src/lib.rs; the browser build uses the File System Access API where available and
// plain upload and download elsewhere.

export interface FolderRef {
  name: string;
  /** Desktop: absolute path of the granted root. */
  root?: string;
  /** Browser: a directory handle from showDirectoryPicker. */
  handle?: FileSystemDirectoryHandle;
  /** Browser fallback (<input webkitdirectory>): the files, read-only. */
  entries?: Map<string, File>;
}

/** An opened folder: Markdown files as sorted, '/'-separated folder-relative paths. */
export interface Listing {
  folder: FolderRef;
  files: string[];
  truncated: boolean;
}

export interface FileRef {
  name: string;
  /** Desktop: absolute path, known to the Rust side's allow-list. */
  path?: string;
  /** Browser: a handle from a picker or from a directory handle. */
  handle?: FileSystemFileHandle;
  /** Set when the file belongs to the open folder. */
  folderPath?: string;
}

export interface OpenedFile {
  ref: FileRef;
  bytes: Uint8Array;
}

/**
 * A stored session the browser reopens only after the user agrees: access to a folder or file
 * handle has to be granted again with a click.
 */
export interface PendingSession {
  name: string;
  /** Call from a click; asks for access and resolves null when it is refused. */
  open(): Promise<Arrival | null>;
}

/**
 * What the app was started with, or what was dropped on it. With both a listing and a file, the
 * file is in that folder (a restored session) and opens instead of the folder's README.
 */
export interface Arrival {
  file?: OpenedFile;
  listing?: Listing;
  pending?: PendingSession;
}

/** The desktop window's own caption buttons (the native frame is off). */
export interface WindowControls {
  minimize(): Promise<void>;
  toggleMaximize(): Promise<void>;
  /** Asks to close; the close-requested handler can still keep the window open. */
  close(): Promise<void>;
  /** Called now and whenever the window is maximized or restored. */
  onMaximizedChange(handler: (maximized: boolean) => void): void;
}

export interface DropHandlers {
  dragging(active: boolean): void;
  open(arrival: Arrival): void;
  /** A dropped item that cannot be opened; folder is set when a browser refuses a folder. */
  refused(name: string, folder?: boolean): void;
}

export interface Platform {
  kind: 'desktop' | 'web';
  /** Present when the app draws its own title bar. */
  window?: WindowControls;
  /** Shows an open dialog. Resolves null when cancelled. */
  open(): Promise<OpenedFile | null>;
  /** Shows a folder dialog and lists its Markdown files. */
  openFolder(): Promise<Listing | null>;
  /** Reads a file of the open folder by its folder-relative path. */
  readFolderFile(listing: Listing, folderPath: string): Promise<OpenedFile>;
  /**
   * Follows a relative link (decoded path, no fragment) from a document. Inside the open folder
   * the app resolves it to a folder path itself; this is for a lone file's links, and returns
   * null when the platform cannot or the user declines.
   */
  followLink(from: FileRef, target: string): Promise<OpenedFile | null>;
  /** Reads a file opened earlier again (for Back), or null when that is no longer possible. */
  reopen(ref: FileRef): Promise<OpenedFile | null>;
  /** Writes to ref, or behaves like saveAs when ref cannot be written in place. */
  save(ref: FileRef | null, bytes: Uint8Array, suggestedName: string): Promise<FileRef | null>;
  saveAs(bytes: Uint8Array, suggestedName: string): Promise<FileRef | null>;
  /** Saves an export (HTML) through a save dialog or a download. */
  exportFile(bytes: Uint8Array, suggestedName: string, kind: 'html'): Promise<void>;
  /** Opens an http, https or mailto address in the system browser. */
  openExternal(url: string): Promise<void>;
  /** Resolves an image path (decoded, relative to ref or folder-rooted) to an object URL. */
  loadLocalImage(ref: FileRef | null, relative: string, listing: Listing | null): Promise<string | null>;
  /**
   * A file or folder the app was started with (file association or command line), or else what
   * was open last time (the stored session).
   */
  initial(): Promise<Arrival | null>;
  /**
   * Stores what is open so the next start reopens it: the folder, and the document when it is in
   * that folder or no folder is open. Neither forgets the session. Best effort; never throws.
   */
  remember(listing: Listing | null, ref: FileRef | null): void;
  /**
   * Drag and drop onto the window: dragging reports when files are over the window (for the
   * drop overlay), open receives a Markdown file or folder, refused names anything else.
   */
  onDrop(handlers: DropHandlers): void;
  /** Called before the window closes; resolve false to keep it open. */
  onCloseRequested(handler: () => Promise<boolean>): void;
  setTitle(title: string): void;
}
