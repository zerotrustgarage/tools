// Bounded text search over the files of the folder the reader already opened. The caller hands in
// the listing's folder-relative paths and a reader for them (the platform's readFolderFile, which
// is what keeps every read inside the folder); this module only decides what to read, in what
// order, and when to stop. It never touches the network and never invents a path: anything that is
// not a plain folder-relative path is counted as unreadable and not read.

export interface SearchBounds {
  /** Files read at most. The rest of the listing is not searched. */
  maxFiles: number;
  /** A file larger than this is skipped (and counted), not searched. */
  maxFileBytes: number;
  /** Matching lines returned in total. */
  maxResults: number;
  /** Matching lines returned from any one file, so one file cannot crowd out the rest. */
  maxPerFile: number;
  /** Wall-clock budget for the whole search. */
  timeBudgetMs: number;
}

export const DEFAULT_BOUNDS: SearchBounds = Object.freeze({
  maxFiles: 2000,
  maxFileBytes: 2 * 1024 * 1024,
  maxResults: 500,
  maxPerFile: 50,
  timeBudgetMs: 10_000,
});

/** Files are read this many at a time; results are still reported in listing order. */
const BATCH = 4;
const SNIPPET_BEFORE = 40;
const SNIPPET_AFTER = 90;

export interface SearchSnippet {
  before: string;
  hit: string;
  after: string;
}

export interface SearchMatch {
  /** Zero-based line. */
  line: number;
  /** Zero-based UTF-16 offset of the match in the line, and its length. */
  column: number;
  length: number;
  snippet: SearchSnippet;
}

export interface FileMatches {
  path: string;
  matches: SearchMatch[];
  /** True when the file has more matching lines than maxPerFile. */
  more: boolean;
}

export type StopReason = 'files' | 'results' | 'time';

export interface SearchSummary {
  filesScanned: number;
  matchedLines: number;
  skippedBinary: number;
  skippedLarge: number;
  skippedUnreadable: number;
  /** Set when a bound ended the search early; the results are then incomplete. */
  stoppedBy: StopReason | null;
  cancelled: boolean;
}

export interface SearchOptions {
  files: readonly string[];
  /** Bytes from the folder, or text already held in memory (an open buffer). Throws or returns null when unreadable. */
  read(path: string): Promise<Uint8Array | string | null>;
  bounds?: Partial<SearchBounds>;
  signal?: AbortSignal;
  /** Called for each file with at least one match, in listing order, never after cancellation. */
  onFile?(file: FileMatches): void;
  /** Clock for the time budget; tests replace it. */
  now?(): number;
}

const escapeRegExp = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** A path that stays inside the folder: relative, '/'-separated, no '..', no drive or backslash. */
export function isFolderRelative(path: string): boolean {
  if (!path || path.startsWith('/') || path.includes('\\') || path.includes('\0') || /^[A-Za-z]:/.test(path)) return false;
  return path.split('/').every((part) => part !== '' && part !== '..' && part !== '.');
}

/** Cuts a window around the match without splitting a surrogate pair. */
export function makeSnippet(line: string, column: number, length: number): SearchSnippet {
  let start = Math.max(0, column - SNIPPET_BEFORE);
  let end = Math.min(line.length, column + length + SNIPPET_AFTER);
  if (start > 0 && /[\udc00-\udfff]/.test(line[start] ?? '')) start++;
  if (end < line.length && /[\ud800-\udbff]/.test(line[end - 1] ?? '')) end--;
  return {
    before: (start > 0 ? '\u2026' : '') + line.slice(start, column).trimStart(),
    hit: line.slice(column, column + length),
    after: line.slice(column + length, end) + (end < line.length ? '\u2026' : ''),
  };
}

/** Matching lines of one text: case-insensitive literal search, one match (the first) per line. */
export function matchLines(text: string, query: string, limit: number): { matches: SearchMatch[]; more: boolean } {
  // The u flag gives Unicode case folding; offsets stay in the original line, which lowercasing the
  // line first would not guarantee (some letters change length).
  const pattern = new RegExp(escapeRegExp(query), 'iu');
  const matches: SearchMatch[] = [];
  const lines = text.split(/\r\n|\r|\n/);
  for (let line = 0; line < lines.length; line++) {
    const found = pattern.exec(lines[line]!);
    if (!found) continue;
    if (matches.length >= limit) return { matches, more: true };
    matches.push({ line, column: found.index, length: found[0].length, snippet: makeSnippet(lines[line]!, found.index, found[0].length) });
  }
  return { matches, more: false };
}

type Loaded = { kind: 'text'; text: string } | { kind: 'binary' } | { kind: 'large' } | { kind: 'unreadable' };

function load(content: Uint8Array | string | null, maxBytes: number): Loaded {
  if (content === null) return { kind: 'unreadable' };
  if (typeof content === 'string') return content.length > maxBytes ? { kind: 'large' } : { kind: 'text', text: content };
  if (content.length > maxBytes) return { kind: 'large' };
  // A NUL early in the file is how every tool tells binary from text.
  if (content.subarray(0, 8192).includes(0)) return { kind: 'binary' };
  try {
    return { kind: 'text', text: new TextDecoder('utf-8', { fatal: true }).decode(content) };
  } catch {
    return { kind: 'binary' };
  }
}

export async function searchFolder(query: string, options: SearchOptions): Promise<SearchSummary> {
  const bounds: SearchBounds = { ...DEFAULT_BOUNDS, ...options.bounds };
  const now = options.now ?? (() => performance.now());
  const started = now();
  const summary: SearchSummary = {
    filesScanned: 0,
    matchedLines: 0,
    skippedBinary: 0,
    skippedLarge: 0,
    skippedUnreadable: 0,
    stoppedBy: null,
    cancelled: false,
  };
  const aborted = () => options.signal?.aborted === true;
  if (query === '') return summary;

  const paths = options.files.slice(0, bounds.maxFiles);
  if (options.files.length > paths.length) summary.stoppedBy = 'files';

  for (let i = 0; i < paths.length; i += BATCH) {
    if (aborted()) return { ...summary, cancelled: true };
    if (now() - started >= bounds.timeBudgetMs) return { ...summary, stoppedBy: 'time' };

    const batch = paths.slice(i, i + BATCH);
    const loaded = await Promise.all(
      batch.map(async (path): Promise<Loaded> => {
        if (!isFolderRelative(path)) return { kind: 'unreadable' };
        try {
          return load(await options.read(path), bounds.maxFileBytes);
        } catch {
          return { kind: 'unreadable' };
        }
      }),
    );
    // The reads above are the only await: check again before anything is reported.
    if (aborted()) return { ...summary, cancelled: true };

    for (let n = 0; n < batch.length; n++) {
      const item = loaded[n]!;
      if (item.kind === 'binary') summary.skippedBinary++;
      else if (item.kind === 'large') summary.skippedLarge++;
      else if (item.kind === 'unreadable') summary.skippedUnreadable++;
      else {
        summary.filesScanned++;
        const room = Math.min(bounds.maxPerFile, bounds.maxResults - summary.matchedLines);
        const { matches, more } = matchLines(item.text, query, room);
        if (matches.length > 0) {
          summary.matchedLines += matches.length;
          options.onFile?.({ path: batch[n]!, matches, more: more && matches.length >= bounds.maxPerFile });
        }
        if (summary.matchedLines >= bounds.maxResults) return { ...summary, stoppedBy: 'results' };
      }
    }
  }
  return summary;
}
