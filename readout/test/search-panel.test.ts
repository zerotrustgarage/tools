import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { EditorView } from '@codemirror/view';
import type { Listing, OpenedFile, Platform } from '../src/platform/types';
import { SearchPanel, describe as describeSummary, type SearchScope } from '../src/ui/search';
import { App } from '../src/ui/app';

const enc = (text: string) => new TextEncoder().encode(text);
const tick = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function mount(scope: () => SearchScope | null) {
  const open = vi.fn();
  const onClose = vi.fn();
  const panel = new SearchPanel({ scope, open, onClose });
  document.body.append(panel.element);
  panel.show(true);
  const type = async (text: string) => {
    const input = panel.element.querySelector('input')!;
    input.value = text;
    await panel.run();
  };
  return { panel, open, onClose, type };
}
const hits = () => [...document.querySelectorAll<HTMLElement>('.search-hit')];
const statusText = () => document.querySelector('.search-status')!.textContent;

afterEach(() => (document.body.innerHTML = ''));

describe('search panel', () => {
  const files = { 'a.md': 'one\nthe Needle here', 'docs/b.md': 'needle', 'c.md': 'nothing' };
  const scope = (): SearchScope => ({ files: Object.keys(files), read: async (p) => enc(files[p as keyof typeof files]) });

  it('groups results by file with line numbers and a highlighted snippet', async () => {
    const { type } = mount(scope);
    await type('needle');
    expect([...document.querySelectorAll('.search-file-name')].map((h) => h.textContent)).toEqual(['a.md (1)', 'docs/b.md (1)']);
    expect(hits()[0]!.querySelector('.search-line')!.textContent).toBe('2');
    expect(hits()[0]!.querySelector('mark')!.textContent).toBe('Needle');
    expect(hits()[0]!.textContent).toBe('2the Needle here');
    expect(statusText()).toBe('2 matches in 3 files searched.');
  });

  it('reports the file and match when a result is chosen', async () => {
    const { type, open } = mount(scope);
    await type('needle');
    hits()[1]!.click();
    expect(open).toHaveBeenCalledWith('docs/b.md', expect.objectContaining({ line: 0, column: 0, length: 6 }));
  });

  it('says so when nothing matches, and for an empty query shows nothing', async () => {
    const { type } = mount(scope);
    await type('zzz');
    expect(document.querySelector('.search-results')!.textContent).toBe('No matches.');
    await type('   ');
    expect(document.querySelector('.search-results')!.textContent).toBe('');
  });

  it('reports skipped files', async () => {
    const { type } = mount(() => ({
      files: ['bin.md', 'bad.md', 'ok.md'],
      read: async (p) => {
        if (p === 'bin.md') return new Uint8Array([0, 1, 2]);
        if (p === 'bad.md') throw new Error('denied');
        return 'needle';
      },
    }));
    await type('needle');
    expect(statusText()).toBe('1 match in 1 file searched. Skipped 1 binary, 1 unreadable.');
  });

  it('a new query cancels the running one, and the old one cannot draw anything', async () => {
    const slow = deferred<string>();
    const reads: string[] = [];
    const { panel } = mount(() => ({
      files: ['a.md'],
      read: async (p) => {
        reads.push(p);
        return reads.length === 1 ? slow.promise : 'second only';
      },
    }));
    const input = panel.element.querySelector('input')!;
    input.value = 'first';
    const first = panel.run();
    await tick();
    input.value = 'second';
    await panel.run();
    expect(hits().map((h) => h.textContent)).toEqual(['1second only']);
    slow.resolve('first first first');
    await first;
    await tick();
    expect(hits().map((h) => h.textContent)).toEqual(['1second only']);
    expect(statusText()).toBe('1 match in 1 file searched.');
  });

  it('a folder change resets the query and results and cancels what is running', async () => {
    const slow = deferred<string>();
    const { panel } = mount(() => ({ files: ['a.md'], read: () => slow.promise }));
    const input = panel.element.querySelector('input')!;
    input.value = 'needle';
    const running = panel.run();
    await tick();
    expect(document.querySelector<HTMLElement>('.search-bar .text-button')!.hidden).toBe(false);
    panel.reset();
    slow.resolve('needle');
    await running;
    await tick();
    expect(input.value).toBe('');
    expect(hits()).toEqual([]);
    expect(statusText()).toBe('');
    expect(document.querySelector<HTMLElement>('.search-bar .text-button')!.hidden).toBe(true);
  });

  it('Stop cancels and keeps what was found so far', async () => {
    const slow = deferred<string>();
    const { panel } = mount(() => ({ files: ['a.md', 'b.md', 'c.md', 'd.md', 'e.md'], read: (p) => (p === 'e.md' ? slow.promise : Promise.resolve('needle')) }));
    panel.element.querySelector('input')!.value = 'needle';
    const running = panel.run();
    await tick(10);
    expect(hits()).toHaveLength(4);
    document.querySelector<HTMLButtonElement>('.search-bar .text-button')!.click();
    slow.resolve('needle');
    await running;
    expect(statusText()).toBe('Search stopped. 4 matches found so far.');
    expect(hits()).toHaveLength(4);
  });

  it('asks for a folder when none is open', async () => {
    const { type } = mount(() => null);
    await type('x');
    expect(statusText()).toBe('Open a folder to search it.');
  });

  it('moves through results with the arrow keys from the field and back', async () => {
    const { type } = mount(scope);
    await type('needle');
    const input = document.querySelector('input')!;
    input.focus();
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    expect(document.activeElement).toBe(hits()[0]);
    hits()[0]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    expect(document.activeElement).toBe(hits()[1]);
    hits()[1]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }));
    hits()[0]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }));
    expect(document.activeElement).toBe(input);
    expect(hits().filter((h) => h.tabIndex === 0)).toHaveLength(1);
  });

  it('closes on Escape', async () => {
    const { onClose } = mount(scope);
    document.querySelector('input')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(onClose).toHaveBeenCalled();
  });

  it('words the closing line for each bound', () => {
    const base = { filesScanned: 5, matchedLines: 2, skippedBinary: 0, skippedLarge: 0, skippedUnreadable: 0, cancelled: false };
    expect(describeSummary({ ...base, stoppedBy: 'time' })).toContain('time limit');
    expect(describeSummary({ ...base, stoppedBy: 'files' })).toContain('file limit');
    expect(describeSummary({ ...base, stoppedBy: 'results' })).toContain('result limit');
    expect(describeSummary({ ...base, skippedLarge: 3, stoppedBy: null })).toBe('2 matches in 5 files searched. Skipped 3 too large.');
  });
});

// ---- inside the app ---------------------------------------------------------------------

const listing: Listing = { folder: { name: 'repo', root: '/repo' }, files: ['README.md', 'docs/a.md', 'docs/b.md'], truncated: false };
const contents: Record<string, string> = {
  'README.md': '# Readme\n\nintro\n',
  'docs/a.md': '# A\n\nline two\nthe needle is on line four\n',
  'docs/b.md': '# B\n\nneedle too\n',
};

function appPlatform(): { platform: Platform; reads: string[] } {
  const reads: string[] = [];
  const file = (folderPath: string): OpenedFile => ({
    ref: { name: folderPath.split('/').pop()!, path: `/repo/${folderPath}`, folderPath },
    bytes: enc(contents[folderPath]!),
  });
  const platform: Platform = {
    kind: 'desktop',
    open: async () => null,
    openFolder: async () => listing,
    readFolderFile: async (_l, p) => {
      reads.push(p);
      return file(p);
    },
    followLink: async () => null,
    reopen: async () => null,
    save: async (ref) => ref,
    saveAs: async () => null,
    exportFile: async () => undefined,
    openExternal: async () => undefined,
    loadLocalImage: async () => null,
    initial: async () => ({ listing, file: file('README.md') }),
    remember: () => undefined,
    onDrop: () => undefined,
    onCloseRequested: () => undefined,
    setTitle: () => undefined,
  };
  return { platform, reads };
}

beforeAll(() => {
  const query = { matches: false, addEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined };
  window.matchMedia ??= vi.fn(() => query) as unknown as typeof window.matchMedia;
  Element.prototype.scrollTo ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  // CodeMirror measures text with ranges, which jsdom does not lay out.
  const none = { length: 0, item: () => null, [Symbol.iterator]: function* () {} };
  Range.prototype.getClientRects ??= () => none as unknown as DOMRectList;
  Range.prototype.getBoundingClientRect ??= () => new DOMRect();
});
beforeEach(() =>
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: { configurable: true, writable: true, value() { this.open = true; } },
    close: { configurable: true, writable: true, value() { this.dispatchEvent(new Event('close')); } },
  }),
);

async function started() {
  const { platform, reads } = appPlatform();
  const host = document.createElement('div');
  document.body.replaceChildren(host);
  const app = new App(platform);
  await app.start(host);
  const view = () => EditorView.findFromDOM(host.querySelector('.cm-editor') as HTMLElement)!;
  const search = async (query: string) => {
    host.querySelector<HTMLButtonElement>('button[title^="Search the open folder"]')!.click();
    const input = host.querySelector<HTMLInputElement>('.search-panel input')!;
    input.value = query;
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await tick(20);
  };
  return { app, host, view, reads, search };
}
const docTitle = (host: HTMLElement) => host.querySelector('.doc-title')!.textContent;

describe('search in the app', () => {
  it('opens the result file with the match selected on its line', async () => {
    const { host, view, search } = await started();
    await search('needle');
    host.querySelector<HTMLElement>('.search-hit')!.click();
    await tick(20);
    expect(docTitle(host)).toBe('docs/a.md');
    const state = view().state;
    expect(state.doc.lineAt(state.selection.main.from).number).toBe(4);
    expect(state.sliceDoc(state.selection.main.from, state.selection.main.to)).toBe('needle');
  });

  it('asks before leaving unsaved edits, and stays put when the reader declines', async () => {
    const { host, view, search } = await started();
    view().dispatch({ changes: { from: 0, insert: 'edited ' } });
    await search('needle');
    host.querySelector<HTMLElement>('.search-hit')!.click();
    await tick(20);
    const dialog = document.querySelector('dialog.modal')!;
    expect(dialog.textContent).toContain('Save changes to README.md?');
    [...dialog.querySelectorAll('button')].find((b) => b.textContent === 'Cancel')!.click();
    await tick(20);
    expect(docTitle(host)).toBe('README.md');
    expect(view().state.sliceDoc(0, 7)).toBe('edited ');
  });

  it('searches the open file as it is in the editor, unsaved edits included', async () => {
    const { host, view, reads, search } = await started();
    view().dispatch({ changes: { from: view().state.doc.length, insert: 'a needle typed here\n' } });
    await search('needle');
    expect(reads).not.toContain('README.md');
    const names = [...host.querySelectorAll('.search-file-name')].map((h) => h.firstChild!.textContent);
    expect(names).toEqual(['README.md', 'docs/a.md', 'docs/b.md']);
    // The hit is on the buffer's own line, so no jump is needed to find it.
    host.querySelector<HTMLElement>('.search-hit')!.click();
    await tick(20);
    expect(docTitle(host)).toBe('README.md');
    const s = view().state;
    expect(s.sliceDoc(s.selection.main.from, s.selection.main.to)).toBe('needle');
  });

  it('reads the open file from the buffer when a refresh swaps the listing while a read is pending', async () => {
    const { app, view, reads } = await started();
    view().dispatch({ changes: { from: view().state.doc.length, insert: 'buffer needle\n' } });
    const internals = app as unknown as { listing: Listing; searchScope(): SearchScope };
    // The search has its scope (and so the old listing object) ...
    const scope = internals.searchScope();
    const gate = deferred<void>();
    const pending = gate.promise.then(() => scope.read('README.md'));
    // ... then READOUT-03's refresh swaps in a new listing object with the same folder, and only
    // afterwards does the read run.
    internals.listing = { ...internals.listing, files: [...internals.listing.files] };
    gate.resolve();
    const text = await pending;
    expect(typeof text).toBe('string');
    expect(text as string).toContain('buffer needle');
    expect(reads).not.toContain('README.md');
  });

  it('is cleared and disabled when the folder is closed', async () => {
    const { host, search } = await started();
    await search('needle');
    expect(host.querySelectorAll('.search-hit').length).toBeGreaterThan(0);
    host.querySelector<HTMLButtonElement>('button[title="Close folder"]')!.click();
    await tick(20);
    expect(host.querySelector<HTMLButtonElement>('button[title^="Search the open folder"]')!.disabled).toBe(true);
    expect(host.querySelector<HTMLElement>('.search-panel')!.hidden).toBe(true);
    expect(host.querySelectorAll('.search-hit')).toHaveLength(0);
    expect(host.querySelector<HTMLInputElement>('.search-panel input')!.value).toBe('');
  });
});
