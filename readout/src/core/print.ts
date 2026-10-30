// The print template (RD12): what the running header and footer of a printed page say, and the
// client name a reader may type for one document.
//
// The text reaches the page as CSS custom properties that hold a quoted string; src/styles/print.css
// shows them in @page margin boxes (`content: var(--print-title)`). No element is created, and the
// strings are cleaned here and quoted by cssString, so nothing a document says can end the string or
// the declaration. The client name is never read from the document: only the reader types it.

import type { FileRef } from '../platform/types';

export const PRINT_CLIENT_KEY = 'ztg-readout-print-client';
export const MAX_CLIENT_LENGTH = 80;
export const MAX_TITLE_LENGTH = 100;
/** Remembered client names are kept for this many documents; the least recently used go first. */
export const MAX_REMEMBERED = 200;

export const PRINT_TITLE_PROPERTY = '--print-title';
export const PRINT_CLIENT_PROPERTY = '--print-client';
export const PRINT_DATE_PROPERTY = '--print-date';

type Store = Pick<Storage, 'getItem' | 'setItem'>;

// Control characters, line and paragraph separators, and the bidirectional overrides and isolates
// that could make a header read differently from what it holds.
// eslint-disable-next-line no-control-regex -- the point is to remove them
const UNSAFE = /[\u0000-\u001f\u007f-\u009f\u2028\u2029\u202a-\u202e\u2066-\u2069\u200e\u200f\u061c]+/g;

/** One line of plain text, at most `max` characters (not UTF-16 units), with no control characters. */
export function cleanText(text: string, max: number): string {
  const line = text.replace(UNSAFE, ' ').replace(/\s+/g, ' ').trim();
  const characters = [...line];
  return characters.length > max ? `${characters.slice(0, max - 1).join('').trimEnd()}\u2026` : line;
}

/** The text as a CSS string token: cleaned, then with backslash and double quote escaped. */
export function cssString(text: string, max: number): string {
  return `"${cleanText(text, max).replace(/[\\"]/g, '\\$&')}"`;
}

/** The ISO date (year-month-day) of a moment in the reader's own time zone. */
export function printDate(now: Date): string {
  const two = (n: number) => String(n).padStart(2, '0');
  return `${now.getFullYear()}-${two(now.getMonth() + 1)}-${two(now.getDate())}`;
}

/** The title for the header: the first heading of the rendered preview, else the file's name. */
export function printTitle(preview: ParentNode, fileName: string): string {
  const heading = cleanText(preview.querySelector('h1')?.textContent ?? '', MAX_TITLE_LENGTH);
  return heading || cleanText(fileName.replace(/\.(md|markdown|mdown|mkd|mkdn|txt)$/i, ''), MAX_TITLE_LENGTH);
}

export interface PrintTemplate {
  title: string;
  /** Empty when the reader gave none. */
  client: string;
  date: string;
}

/** Sets the template on `root` (the document element). Cleared again by clearPrintTemplate. */
export function setPrintTemplate(root: HTMLElement, template: PrintTemplate): void {
  const client = cleanText(template.client, MAX_CLIENT_LENGTH);
  root.style.setProperty(PRINT_TITLE_PROPERTY, cssString(template.title, MAX_TITLE_LENGTH));
  root.style.setProperty(PRINT_CLIENT_PROPERTY, client === '' ? '""' : cssString(`Client: ${client}`, MAX_CLIENT_LENGTH + 8));
  root.style.setProperty(PRINT_DATE_PROPERTY, cssString(`Printed ${template.date}`, 40));
}

export function clearPrintTemplate(root: HTMLElement): void {
  for (const name of [PRINT_TITLE_PROPERTY, PRINT_CLIENT_PROPERTY, PRINT_DATE_PROPERTY]) root.style.removeProperty(name);
}

// ---- remembered client names -----------------------------------------------------------------

/**
 * What a client name is remembered under: the desktop's absolute path, a folder-relative path, or
 * (a file opened on its own in the browser, which has no path) its name. An unsaved document has
 * no key, so nothing is remembered for it.
 */
export function printKey(ref: FileRef | null | undefined): string | null {
  if (!ref) return null;
  const key = ref.path ?? ref.folderPath ?? ref.name;
  return key ? key : null;
}

function defaultStore(): Store | null {
  try {
    return localStorage;
  } catch {
    return null;
  }
}

/** Oldest first. Anything in storage that is not a list of [string, string] pairs is ignored. */
function readAll(store: Store | null): Map<string, string> {
  const all = new Map<string, string>();
  try {
    const parsed: unknown = JSON.parse(store?.getItem(PRINT_CLIENT_KEY) ?? 'null');
    if (!Array.isArray(parsed)) return all;
    for (const entry of parsed) {
      if (Array.isArray(entry) && typeof entry[0] === 'string' && typeof entry[1] === 'string') {
        const client = cleanText(entry[1], MAX_CLIENT_LENGTH);
        if (client !== '') all.set(entry[0], client);
      }
    }
  } catch {
    // Unreadable or unavailable storage: nothing is remembered.
  }
  return all;
}

function writeAll(store: Store | null, all: Map<string, string>): void {
  try {
    store?.setItem(PRINT_CLIENT_KEY, JSON.stringify([...all].slice(-MAX_REMEMBERED)));
  } catch {
    // Storage can be unavailable (private windows); the name then lasts for the session only.
  }
}

export function rememberedClient(key: string | null, store: Store | null = defaultStore()): string {
  return key === null ? '' : (readAll(store).get(key) ?? '');
}

/** Remembers the name for the document; an empty name forgets it. */
export function rememberClient(key: string | null, client: string, store: Store | null = defaultStore()): void {
  if (key === null) return;
  const all = readAll(store);
  const clean = cleanText(client, MAX_CLIENT_LENGTH);
  all.delete(key);
  if (clean !== '') all.set(key, clean);
  writeAll(store, all);
}

export function forgetClient(key: string | null, store: Store | null = defaultStore()): void {
  rememberClient(key, '', store);
}
