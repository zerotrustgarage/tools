import { searchFolder, type FileMatches, type SearchMatch, type SearchSummary } from '../core/search';
import { el, icon } from './dom';

// The folder search panel. It owns one search at a time: typing, Enter, a folder change or the
// Stop button cancels the running one first, and a cancelled search can no longer draw anything.
// Which files exist and how they are read comes from the host, so the panel cannot reach outside
// the folder the app has open.

export interface SearchScope {
  /** The open folder's Markdown files, as folder-relative paths. */
  files: readonly string[];
  /** Bytes from the folder, or the text of an open buffer. */
  read(path: string): Promise<Uint8Array | string | null>;
}

export interface SearchHost {
  /** The folder to search now, or null when none is open. */
  scope(): SearchScope | null;
  open(path: string, match: SearchMatch): void;
  onClose(): void;
}

const TYPING_DELAY_MS = 300;

export class SearchPanel {
  readonly element = el('aside', { class: 'tool-panel search-panel', 'aria-label': 'Search folder', hidden: '' });
  private readonly input = el('input', { type: 'search', class: 'sidebar-filter', placeholder: 'Search in folder', 'aria-label': 'Search text in the open folder', spellcheck: 'false' });
  private readonly stop = el('button', { type: 'button', class: 'text-button', hidden: '' }, 'Stop');
  private readonly status = el('div', { class: 'search-status', role: 'status', 'aria-live': 'polite' });
  private readonly results = el('div', { class: 'tool-body search-results' });
  private current: AbortController | null = null;
  private timer: number | undefined;
  private matches = 0;

  constructor(private readonly host: SearchHost) {
    const close = el('button', { type: 'button', class: 'icon-button', title: 'Close search', 'aria-label': 'Close search' }, icon('close'));
    close.addEventListener('click', () => host.onClose());
    this.stop.addEventListener('click', () => this.cancel('Search stopped.'));
    this.input.addEventListener('input', () => {
      window.clearTimeout(this.timer);
      this.timer = window.setTimeout(() => void this.run(), TYPING_DELAY_MS);
    });
    this.input.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        window.clearTimeout(this.timer);
        void this.run();
      } else if (event.key === 'ArrowDown') {
        this.hits()[0]?.focus();
        event.preventDefault();
      }
    });
    this.results.addEventListener('keydown', (event) => this.key(event));
    this.element.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        host.onClose();
      }
    });
    this.element.append(
      el('div', { class: 'sidebar-head' }, icon('folder'), el('span', { class: 'sidebar-title' }, 'Search folder'), close),
      this.input,
      el('div', { class: 'search-bar' }, this.status, this.stop),
      this.results,
    );
  }

  get hidden(): boolean {
    return this.element.hidden !== false;
  }

  show(visible: boolean): void {
    this.element.hidden = !visible;
    if (visible) this.input.focus();
    else this.cancel(null);
  }

  /** The folder changed or closed: cancel, and forget the query and everything found. */
  reset(): void {
    window.clearTimeout(this.timer);
    this.cancel(null);
    this.input.value = '';
    this.clear();
    this.status.textContent = '';
  }

  /** Runs the typed query now (also what Enter does). */
  async run(): Promise<void> {
    window.clearTimeout(this.timer);
    this.cancel(null);
    this.clear();
    const query = this.input.value;
    const scope = this.host.scope();
    if (query.trim() === '' || !scope) {
      this.status.textContent = scope ? '' : 'Open a folder to search it.';
      return;
    }
    const controller = new AbortController();
    this.current = controller;
    this.stop.hidden = false;
    this.status.textContent = 'Searching...';
    const summary = await searchFolder(query, {
      files: scope.files,
      read: scope.read,
      signal: controller.signal,
      onFile: (file) => {
        if (controller.signal.aborted) return;
        this.matches += file.matches.length;
        this.results.append(this.group(file));
        this.status.textContent = `Searching... ${this.matches} match${this.matches === 1 ? '' : 'es'}`;
      },
    });
    // A newer search, a folder change or Stop got here first: it owns the display now.
    if (this.current !== controller || controller.signal.aborted) return;
    this.current = null;
    this.stop.hidden = true;
    this.status.textContent = describe(summary);
    if (summary.matchedLines === 0) this.results.append(el('p', { class: 'tool-empty' }, 'No matches.'));
  }

  private cancel(message: string | null): void {
    if (this.current) {
      this.current.abort();
      this.current = null;
      this.stop.hidden = true;
      if (message !== null) this.status.textContent = `${message} ${this.matches} match${this.matches === 1 ? '' : 'es'} found so far.`;
    }
  }

  private clear(): void {
    this.matches = 0;
    this.results.replaceChildren();
  }

  private hits(): HTMLElement[] {
    return [...this.results.querySelectorAll<HTMLElement>('.search-hit')];
  }

  private group(file: FileMatches): HTMLElement {
    const list = el('ul', { class: 'search-list' });
    // One tab stop for the whole result list; the arrow keys move within it.
    const firstOverall = this.results.querySelector('.search-hit') === null;
    for (const [index, match] of file.matches.entries()) {
      const { before, hit, after } = match.snippet;
      const button = el(
        'button',
        { type: 'button', class: 'search-hit', tabindex: firstOverall && index === 0 ? '0' : '-1', title: `${file.path}, line ${match.line + 1}`, 'aria-label': `${file.path}, line ${match.line + 1}: ${before}${hit}${after}` },
        el('span', { class: 'search-line' }, String(match.line + 1)),
        el('span', { class: 'search-snippet' }, before, el('mark', {}, hit), after),
      );
      button.addEventListener('click', () => this.host.open(file.path, match));
      list.append(el('li', {}, button));
    }
    const more = file.more ? el('p', { class: 'tool-empty' }, 'More matches in this file than are listed.') : null;
    return el('section', { class: 'search-file' }, el('h3', { class: 'search-file-name', title: file.path }, file.path, el('span', {}, ` (${file.matches.length}${file.more ? '+' : ''})`)), list, ...(more ? [more] : []));
  }

  private key(event: KeyboardEvent): void {
    const hits = this.hits();
    const at = hits.indexOf(document.activeElement as HTMLElement);
    if (at < 0) return;
    if (event.key === 'ArrowUp' && at === 0) {
      this.input.focus();
      event.preventDefault();
      return;
    }
    const next = event.key === 'ArrowDown' ? at + 1 : event.key === 'ArrowUp' ? at - 1 : event.key === 'Home' ? 0 : event.key === 'End' ? hits.length - 1 : null;
    if (next === null) return;
    event.preventDefault();
    const target = hits[Math.min(Math.max(next, 0), hits.length - 1)]!;
    for (const hit of hits) hit.tabIndex = hit === target ? 0 : -1;
    target.focus();
  }
}

/** The closing line: what was found, what was skipped, and whether a bound cut the search short. */
export function describe(s: SearchSummary): string {
  const parts = [`${s.matchedLines} match${s.matchedLines === 1 ? '' : 'es'} in ${s.filesScanned} file${s.filesScanned === 1 ? '' : 's'} searched.`];
  const skipped = [
    s.skippedBinary ? `${s.skippedBinary} binary` : '',
    s.skippedLarge ? `${s.skippedLarge} too large` : '',
    s.skippedUnreadable ? `${s.skippedUnreadable} unreadable` : '',
  ].filter(Boolean);
  if (skipped.length) parts.push(`Skipped ${skipped.join(', ')}.`);
  if (s.stoppedBy === 'files') parts.push('Stopped at the file limit; the rest of the folder was not searched.');
  if (s.stoppedBy === 'results') parts.push('Stopped at the result limit; refine the search to see more.');
  if (s.stoppedBy === 'time') parts.push('Stopped at the time limit; the results are incomplete.');
  return parts.join(' ');
}
