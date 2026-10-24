// External changes: another program (git pull, an AI agent, a sync client) rewrites, renames or
// deletes the open file or changes the open folder. This module holds the decisions and the
// polling loop; it knows nothing about the DOM, Tauri or the browser, so every rule is tested
// with a fake host and fake timers.
//
// Why polling and not a file-system watcher: the repository has no fs-watch plugin or crate
// (none in package-lock.json or Cargo.lock), and a new dependency is out of scope. Polling
// works through the platform calls that already exist, which keeps the WebView free of file
// system permission and keeps every read inside the folder the user chose.
//
// Rules:
// - A clean buffer follows the disk. A dirty buffer never changes without a deliberate choice.
// - A change is acted on only once the same disk state has been seen on two consecutive polls,
//   so a burst (git pull rewriting many files, a half-written file) settles into one action.
// - Nothing here writes. The disk is never touched, and unchanged bytes are never re-encoded.

export type Decision = 'none' | 'reload' | 'conflict' | 'missing';
export type Choice = 'mine' | 'disk' | null;

export const sameBytes = (a: Uint8Array | null, b: Uint8Array | null): boolean => {
  if (a === null || b === null) return a === b;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
};

/** A cheap key (length and FNV-1a) used only to see whether two polls saw the same state. */
export function fingerprint(bytes: Uint8Array | null): string {
  if (bytes === null) return 'missing';
  let h = 0x811c9dc5;
  for (let i = 0; i < bytes.length; i++) h = Math.imul(h ^ bytes[i]!, 0x01000193) >>> 0;
  return `${bytes.length}:${h.toString(16)}`;
}

/**
 * What to do about the disk version of the open file. baseline is the bytes the buffer was last
 * loaded from or saved as; acknowledged is a disk version the user already chose to keep their
 * edits against, which is not asked about again.
 */
export function decide(input: {
  baseline: Uint8Array;
  disk: Uint8Array | null;
  dirty: boolean;
  acknowledged?: Uint8Array | null;
}): Decision {
  if (input.disk === null) return 'missing';
  if (sameBytes(input.disk, input.baseline)) return 'none';
  if (!input.dirty) return 'reload';
  return sameBytes(input.disk, input.acknowledged ?? null) ? 'none' : 'conflict';
}

export function diffFiles(previous: readonly string[], next: readonly string[]): { added: string[]; removed: string[] } {
  const before = new Set(previous);
  const after = new Set(next);
  return { added: next.filter((f) => !before.has(f)), removed: previous.filter((f) => !after.has(f)) };
}

/** The new path of a renamed file: an added file whose bytes equal the baseline. */
export function findRename(baseline: Uint8Array, candidates: readonly { path: string; bytes: Uint8Array }[]): string | null {
  return candidates.find((c) => sameBytes(c.bytes, baseline))?.path ?? null;
}

export interface WatchedListing {
  files: string[];
  truncated: boolean;
}

/** Everything the watcher needs from the app. Reads return null when the file cannot be read. */
export interface WatchHost {
  /** True while a file or folder is open that can be watched. */
  active(): boolean;
  /** Nothing is checked while paused (hidden page, a dialog open). */
  paused(): boolean;
  /** The open folder-relative path, or null for a lone file or no file. */
  currentPath(): string | null;
  hasFile(): boolean;
  isDirty(): boolean;
  folderFiles(): readonly string[] | null;
  /** Lists the open folder again; null when the platform cannot or the folder is unreadable. */
  relist(): Promise<WatchedListing | null>;
  readCurrent(): Promise<Uint8Array | null>;
  /** Reads another file of the open folder (a rename candidate). */
  readFolderFile(path: string): Promise<Uint8Array | null>;
  applyListing(listing: WatchedListing): void;
  /** Clean buffer: show the disk version. */
  reload(bytes: Uint8Array): void;
  /** Dirty buffer: ask. Resolve null when the question is dismissed. */
  resolveConflict(bytes: Uint8Array): Promise<Choice>;
  /** The user dismissed or kept: say that the edits are kept and offer to review. */
  conflictDeferred(): void;
  /** The file was renamed inside the folder: follow it without touching the buffer. */
  renamed(path: string): Promise<void> | void;
  /** The file is gone; the buffer stays open. */
  gone(): void;
}

export interface WatchOptions {
  intervalMs?: number;
  /** The folder is listed every this many polls (a large tree is costly to walk). */
  listEvery?: number;
  /** Rename candidates read at most. */
  maxCandidates?: number;
}

export const POLL_MS = 1500;
export const LIST_EVERY = 4;
export const MAX_RENAME_CANDIDATES = 20;

export class ExternalChangeWatcher {
  private timer: ReturnType<typeof setInterval> | undefined;
  private generation = 0;
  private busy = false;
  private polls = 0;
  private baseline: Uint8Array | null = null;
  private acknowledged: Uint8Array | null = null;
  private goneReported = false;
  private fileSeen: string | null = null;
  private listSeen: string | null = null;
  private readonly intervalMs: number;
  private readonly listEvery: number;
  private readonly maxCandidates: number;

  constructor(
    private readonly host: WatchHost,
    options: WatchOptions = {},
  ) {
    this.intervalMs = options.intervalMs ?? POLL_MS;
    this.listEvery = options.listEvery ?? LIST_EVERY;
    this.maxCandidates = options.maxCandidates ?? MAX_RENAME_CANDIDATES;
  }

  get running(): boolean {
    return this.timer !== undefined;
  }

  /** The bytes the buffer now corresponds to (after a load or a save); null for no file. */
  rebase(bytes: Uint8Array | null): void {
    this.baseline = bytes;
    this.acknowledged = null;
    this.goneReported = false;
    this.reset();
  }

  /** True when disk still holds what the buffer was loaded from or saved as. */
  matchesBaseline(disk: Uint8Array): boolean {
    return this.baseline === null || sameBytes(disk, this.baseline);
  }

  /** Starts polling, or restarts the stability tracking when it already runs. */
  start(): void {
    this.reset();
    if (this.timer === undefined) this.timer = setInterval(() => void this.tick(), this.intervalMs);
  }

  /** Stops polling and discards any read still in flight. */
  stop(): void {
    if (this.timer !== undefined) clearInterval(this.timer);
    this.timer = undefined;
    this.reset();
  }

  /** Runs or stops according to the host: call after anything that opens or closes a file or folder. */
  sync(): void {
    if (this.host.active()) this.start();
    else this.stop();
  }

  /** Ask again about a disk version that was set aside. */
  review(): void {
    this.acknowledged = null;
    void this.tick(true);
  }

  private reset(): void {
    this.generation++;
    this.fileSeen = null;
    this.listSeen = null;
    this.polls = 0;
  }

  /** One poll. With force the stability wait is skipped. Overlapping polls are dropped. */
  async tick(force = false): Promise<void> {
    if (this.busy || this.timer === undefined || this.host.paused()) return;
    const generation = this.generation;
    const stale = () => generation !== this.generation || this.timer === undefined;
    this.busy = true;
    try {
      const listNow = this.polls++ % this.listEvery === 0 || force;
      if (listNow && this.host.folderFiles()) await this.pollListing(force, stale);
      if (stale()) return;
      if (this.host.hasFile() && this.baseline) await this.pollFile(force, stale);
    } catch {
      // A read that fails is treated as "nothing changed"; the next poll tries again.
    } finally {
      this.busy = false;
    }
  }

  private async pollListing(force: boolean, stale: () => boolean): Promise<void> {
    const known = this.host.folderFiles();
    const next = known ? await this.host.relist() : null;
    if (!known || !next || stale()) return;
    const same = next.files.length === known.length && next.files.every((f, i) => f === known[i]);
    if (same) {
      this.listSeen = null;
      return;
    }
    const key = next.files.join('\n');
    // A pull that adds many files shows a different listing on every poll until it finishes.
    if (!force && this.listSeen !== key) {
      this.listSeen = key;
      return;
    }
    this.listSeen = null;
    this.host.applyListing(next);
    const path = this.host.currentPath();
    if (path !== null && this.baseline && !next.files.includes(path)) await this.fileGone(diffFiles(known, next.files).added, stale);
  }

  private async pollFile(force: boolean, stale: () => boolean): Promise<void> {
    const path = this.host.currentPath();
    const files = this.host.folderFiles();
    // A file the listing no longer has is handled by the listing poll (rename or delete).
    if (path !== null && files && !files.includes(path)) return;
    const disk = await this.host.readCurrent();
    if (stale() || !this.baseline) return;
    if (disk === null && path !== null) return; // in a folder, a failed read is not proof of deletion
    const key = fingerprint(disk);
    if (!force && this.fileSeen !== key) {
      this.fileSeen = key;
      return;
    }
    this.fileSeen = key;
    if (disk !== null) this.goneReported = false;
    const action = decide({ baseline: this.baseline, disk, dirty: this.host.isDirty(), acknowledged: this.acknowledged });
    if (action === 'none') {
      if (disk && sameBytes(disk, this.baseline)) this.acknowledged = null;
    } else if (action === 'reload') {
      this.baseline = disk;
      this.acknowledged = null;
      this.host.reload(disk!);
    } else if (action === 'missing') {
      await this.fileGone([], stale);
    } else {
      const choice = await this.host.resolveConflict(disk!);
      if (stale()) return;
      this.acknowledged = disk;
      if (choice === 'disk') {
        this.baseline = disk;
        this.acknowledged = null;
        this.host.reload(disk!);
      } else this.host.conflictDeferred();
    }
  }

  private async fileGone(added: readonly string[], stale: () => boolean): Promise<void> {
    const baseline = this.baseline;
    if (!baseline) return;
    const candidates: { path: string; bytes: Uint8Array }[] = [];
    for (const path of added.slice(0, this.maxCandidates)) {
      const bytes = await this.host.readFolderFile(path);
      if (stale()) return;
      if (bytes) candidates.push({ path, bytes });
    }
    const moved = findRename(baseline, candidates);
    if (moved !== null) {
      this.goneReported = false;
      await this.host.renamed(moved);
    } else if (!this.goneReported) {
      this.goneReported = true;
      this.host.gone();
    }
  }
}
