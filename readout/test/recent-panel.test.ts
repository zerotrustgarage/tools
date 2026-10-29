import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { EditorView } from '@codemirror/view';
import type { FileRef, Listing, OpenedFile, Platform, RecentEntry, RecentFiles } from '../src/platform/types';
import { App } from '../src/ui/app';
import { RecentPanel } from '../src/ui/recent';

const enc = (text: string) => new TextEncoder().encode(text);
const tick = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));

// ---- the panel on its own -----------------------------------------------------------------

function mount(entries: RecentEntry[]) {
  const handlers = { open: vi.fn(), remove: vi.fn(), clear: vi.fn(), onClose: vi.fn() };
  const panel = new RecentPanel(handlers);
  document.body.append(panel.element);
  panel.show(true);
  panel.update(entries);
  return { panel, ...handlers };
}
const rows = () => [...document.querySelectorAll<HTMLElement>('.recent-row')];
const openButtons = () => [...document.querySelectorAll<HTMLButtonElement>('.recent-item')];
const key = (target: Element, name: string) => target.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true }));
const three: RecentEntry[] = [
  { id: '/x/a.md', name: 'a.md', detail: '/x' },
  { id: '/x/b.md', name: 'b.md', detail: '/x', missing: true },
  { id: '/y/c.md', name: 'c.md' },
];

afterEach(() => (document.body.innerHTML = ''));

describe('recent files panel', () => {
  it('lists each file with its folder, and says which are not found', () => {
    mount(three);
    expect(openButtons().map((b) => b.querySelector('.recent-name')!.textContent)).toEqual(['a.md', 'b.md', 'c.md']);
    expect(openButtons()[0]!.querySelector('.recent-detail')!.textContent).toBe('/x');
    expect(openButtons()[1]!.textContent).toContain('Not found');
    expect(openButtons()[0]!.textContent).not.toContain('Not found');
    expect(document.querySelector('[role="status"]')!.textContent).toBe('3 files, 1 not found');
  });

  it('has an accessible name for the panel, the list and every remove button', () => {
    mount(three);
    expect(document.querySelector('aside')!.getAttribute('aria-label')).toBe('Recent files');
    expect(document.querySelector('ul')!.getAttribute('aria-label')).toBe('Recent files');
    expect([...document.querySelectorAll('.recent-remove')].map((b) => b.getAttribute('aria-label'))).toEqual([
      'Remove a.md from the list',
      'Remove b.md from the list',
      'Remove c.md from the list',
    ]);
    // Everything is a native button: reachable and operable from the keyboard.
    expect(document.querySelectorAll('.recent-row button')).toHaveLength(6);
  });

  it('opens a line on click, missing ones included, so the app can report them', () => {
    const { open } = mount(three);
    openButtons()[1]!.click();
    expect(open).toHaveBeenCalledWith(three[1]);
  });

  it('removes a line from its button, and from Delete on a focused line', () => {
    const { remove } = mount(three);
    document.querySelectorAll<HTMLButtonElement>('.recent-remove')[2]!.click();
    expect(remove).toHaveBeenLastCalledWith(three[2]);
    key(openButtons()[0]!, 'Delete');
    expect(remove).toHaveBeenLastCalledWith(three[0]);
  });

  it('moves between files with the arrow keys, Home and End', () => {
    mount(three);
    openButtons()[0]!.focus();
    key(openButtons()[0]!, 'ArrowDown');
    expect(document.activeElement).toBe(openButtons()[1]);
    key(openButtons()[1]!, 'End');
    expect(document.activeElement).toBe(openButtons()[2]);
    key(openButtons()[2]!, 'ArrowDown');
    expect(document.activeElement).toBe(openButtons()[2]);
    key(openButtons()[2]!, 'Home');
    expect(document.activeElement).toBe(openButtons()[0]);
  });

  it('closes on Escape', () => {
    const { onClose } = mount(three);
    openButtons()[0]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(onClose).toHaveBeenCalled();
  });

  it('offers Clear list when there is something to clear', () => {
    const { clear } = mount(three);
    const button = [...document.querySelectorAll('button')].find((b) => b.textContent === 'Clear list')!;
    expect(button.disabled).toBe(false);
    button.click();
    expect(clear).toHaveBeenCalledOnce();
  });

  it('explains an empty list and disables Clear list', () => {
    mount([]);
    expect([...document.querySelectorAll('button')].find((b) => b.textContent === 'Clear list')!.disabled).toBe(true);
    expect(document.querySelector('.tool-empty')!.hasAttribute('hidden')).toBe(false);
    expect(document.querySelector('.recent-list')!.hasAttribute('hidden')).toBe(true);
  });

  it('keeps focus in the list after a removal, on the line that took its place', () => {
    const { panel } = mount(three);
    openButtons()[1]!.focus();
    panel.update([three[0]!, three[2]!]);
    expect(document.activeElement).toBe(openButtons()[1]);
    expect(openButtons()[1]!.textContent).toContain('c.md');
    panel.update([]);
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Close recent files');
  });

  it('shows names as text, never as markup', () => {
    mount([{ id: '1', name: '<img src=x onerror=alert(1)>.md', detail: '<b>x</b>' }]);
    expect(document.querySelector('img')).toBeNull();
    expect(document.querySelector('.recent-name')!.textContent).toBe('<img src=x onerror=alert(1)>.md');
  });
});

// ---- inside the app -----------------------------------------------------------------------

const listing: Listing = { folder: { name: 'repo', root: '/repo' }, files: ['README.md', 'docs/a.md'], truncated: false };
const contents: Record<string, string> = { 'README.md': '# Readme\n', 'docs/a.md': '# A\n' };
const inFolder = (folderPath: string): OpenedFile => ({
  ref: { name: folderPath.split('/').pop()!, path: `/repo/${folderPath}`, folderPath },
  bytes: enc(contents[folderPath]!),
});
const lone = (name: string): OpenedFile => ({ ref: { name, path: `/elsewhere/${name}` }, bytes: enc(`# ${name}\n`) });

/** A recent list the way the platforms keep one: newest first, by path, one line per file. */
function fakeRecent(gone: Set<string>) {
  const lines: RecentEntry[] = [];
  const calls = { add: [] as string[], open: [] as string[], remove: [] as string[], clear: 0 };
  const recent: RecentFiles = {
    list: async () => lines.map((l) => ({ ...l })),
    add: async (ref: FileRef) => {
      if (!ref.path) return;
      calls.add.push(ref.path);
      const at = lines.findIndex((l) => l.id === ref.path);
      if (at >= 0) lines.splice(at, 1);
      lines.unshift({ id: ref.path, name: ref.name });
    },
    open: async (id, open) => {
      calls.open.push(id);
      if (gone.has(id)) return null;
      const under = id.startsWith('/repo/') ? id.slice(6) : undefined;
      if (under && open?.folder.root === '/repo') return inFolder(under);
      return lone(id.split('/').pop()!);
    },
    remove: async (id) => {
      calls.remove.push(id);
      const at = lines.findIndex((l) => l.id === id);
      if (at >= 0) lines.splice(at, 1);
    },
    clear: async () => {
      calls.clear++;
      lines.length = 0;
    },
  };
  return { recent, lines, calls };
}

function appPlatform(options: { withRecent?: boolean; initial?: OpenedFile } = {}) {
  const gone = new Set<string>();
  const { recent, lines, calls } = fakeRecent(gone);
  const platform: Platform = {
    kind: 'desktop',
    ...(options.withRecent === false ? {} : { recent }),
    open: async () => lone('opened.md'),
    openFolder: async () => listing,
    readFolderFile: async (_l, p) => inFolder(p),
    followLink: async () => null,
    reopen: async () => null,
    save: async (ref) => ref,
    saveAs: async (_bytes, name) => ({ name, path: `/elsewhere/${name}` }),
    exportFile: async () => undefined,
    openExternal: async () => undefined,
    copyRichText: async () => undefined,
    loadLocalImage: async () => null,
    initial: async () => (options.initial ? { file: options.initial } : null),
    remember: () => undefined,
    onDrop: () => undefined,
    onCloseRequested: () => undefined,
    setTitle: () => undefined,
  };
  return { platform, gone, lines, calls };
}

beforeAll(() => {
  const query = { matches: false, addEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined };
  window.matchMedia ??= vi.fn(() => query) as unknown as typeof window.matchMedia;
  Element.prototype.scrollTo ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
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

async function started(options?: Parameters<typeof appPlatform>[0]) {
  const rig = appPlatform(options);
  const host = document.createElement('div');
  document.body.replaceChildren(host);
  const app = new App(rig.platform);
  await app.start(host);
  await tick(5);
  const view = () => EditorView.findFromDOM(host.querySelector('.cm-editor') as HTMLElement)!;
  const act = app as unknown as { open(): Promise<void>; openFolder(): Promise<void> };
  const panel = () => host.querySelector<HTMLElement>('.recent-panel')!;
  const button = () => host.querySelector<HTMLButtonElement>('button[aria-label="Recent files"]');
  const lines = () => [...host.querySelectorAll<HTMLButtonElement>('.recent-item .recent-name')].map((n) => n.textContent);
  const choose = async (name: string) => {
    [...host.querySelectorAll<HTMLButtonElement>('.recent-item')].find((b) => b.querySelector('.recent-name')!.textContent === name)!.click();
    await tick(10);
  };
  return { ...rig, host, view, act, panel, button, lines, choose };
}
const docTitle = (host: HTMLElement) => host.querySelector('.doc-title')!.textContent;
const banner = (host: HTMLElement) => host.querySelector<HTMLElement>('.banner')!;
const dialogButton = (label: string) => [...document.querySelectorAll<HTMLButtonElement>('dialog.modal button')].find((b) => b.textContent === label)!;

describe('recent files in the app', () => {
  it('records each document opened or saved elsewhere, newest first', async () => {
    const { act, button, lines, calls, host } = await started();
    await act.open();
    await tick(5);
    await act.openFolder();
    await tick(5);
    expect(calls.add).toEqual(['/elsewhere/opened.md', '/repo/README.md']);
    button()!.click();
    await tick(5);
    expect(lines()).toEqual(['README.md', 'opened.md']);
    expect(docTitle(host)).toBe('README.md');
  });

  it('has no Recent button where the platform cannot keep a list', async () => {
    const { button, host } = await started({ withRecent: false });
    expect(button()).toBeNull();
    // Nothing breaks without it, and the shortcut does nothing.
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'E', ctrlKey: true, shiftKey: true }));
    expect(host.querySelector<HTMLElement>('.recent-panel')!.hidden).toBe(true);
  });

  it('opens from the keyboard with Ctrl+Shift+E, focused on the first file', async () => {
    const { host, act, panel } = await started();
    await act.open();
    await tick(5);
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'E', ctrlKey: true, shiftKey: true, cancelable: true }));
    await tick(10);
    expect(panel().hidden).toBe(false);
    expect(document.activeElement).toBe(host.querySelector('.recent-item'));
    document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(panel().hidden).toBe(true);
  });

  it('opens an entry through the open path and closes the panel', async () => {
    const { host, act, button, panel, choose, view } = await started({ initial: lone('first.md') });
    await act.open();
    await tick(5);
    expect(docTitle(host)).toBe('opened.md');
    button()!.click();
    await tick(5);
    await choose('first.md');
    expect(docTitle(host)).toBe('first.md');
    expect(panel().hidden).toBe(true);
    expect(view().state.doc.toString()).toBe('# first.md\n');
    expect(host.querySelector('.dirty-dot')!.hasAttribute('hidden')).toBe(true);
  });

  it('asks before leaving unsaved edits and stays put on Cancel', async () => {
    const { host, act, button, choose, view, calls } = await started({ initial: lone('first.md') });
    await act.open();
    await tick(5);
    view().dispatch({ changes: { from: 0, insert: 'edited ' } });
    button()!.click();
    await tick(5);
    await choose('first.md');
    expect(document.querySelector('dialog.modal')!.textContent).toContain('Save changes to opened.md?');
    dialogButton('Cancel').click();
    await tick(10);
    expect(calls.open).toEqual([]);
    expect(docTitle(host)).toBe('opened.md');
    expect(view().state.sliceDoc(0, 7)).toBe('edited ');
  });

  it("opens after Don't save, discarding the edits", async () => {
    const { host, act, button, choose, view, calls } = await started({ initial: lone('first.md') });
    await act.open();
    await tick(5);
    view().dispatch({ changes: { from: 0, insert: 'edited ' } });
    button()!.click();
    await tick(5);
    await choose('first.md');
    dialogButton("Don't save").click();
    await tick(10);
    expect(calls.open).toEqual(['/elsewhere/first.md']);
    expect(docTitle(host)).toBe('first.md');
  });

  it('keeps a file of the open folder in the folder, and closes the folder for any other', async () => {
    const { host, act, button, choose } = await started({ initial: lone('first.md') });
    await act.openFolder();
    await tick(5);
    host.querySelector<HTMLButtonElement>('.tree-file[data-path="docs/a.md"]')!.click();
    await tick(10);
    expect(docTitle(host)).toBe('docs/a.md');
    button()!.click();
    await tick(5);
    // README.md is in the open folder: the file list stays beside it.
    await choose('README.md');
    expect(docTitle(host)).toBe('README.md');
    expect(host.querySelector<HTMLElement>('.sidebar')!.hidden).toBe(false);
    // first.md is outside it: opening it replaces the folder, as Open does.
    button()!.click();
    await tick(5);
    await choose('first.md');
    expect(docTitle(host)).toBe('first.md');
    expect(host.querySelector<HTMLElement>('.sidebar')!.hidden).toBe(true);
  });

  it('reports a file that no longer exists, marks it, and lets the reader remove it', async () => {
    const { host, act, button, lines, choose, gone, calls, view } = await started({ initial: lone('first.md') });
    await act.open();
    await tick(5);
    gone.add('/elsewhere/first.md');
    button()!.click();
    await tick(5);
    await choose('first.md');
    expect(banner(host).hidden).toBe(false);
    expect(banner(host).textContent).toContain('first.md no longer exists');
    expect(docTitle(host)).toBe('opened.md');
    expect(host.querySelector<HTMLElement>('.recent-panel')!.hidden).toBe(false);
    const missing = host.querySelector('.recent-item.missing')!;
    expect(missing.textContent).toContain('Not found');
    expect(view().state.doc.toString()).toBe('# opened.md\n');
    host.querySelectorAll<HTMLButtonElement>('.recent-remove')[1]!.click();
    await tick(10);
    expect(calls.remove).toEqual(['/elsewhere/first.md']);
    expect(lines()).toEqual(['opened.md']);
  });

  it('clears the list', async () => {
    const { host, act, button, lines, calls } = await started({ initial: lone('first.md') });
    await act.open();
    await tick(5);
    button()!.click();
    await tick(5);
    expect(lines()).toHaveLength(2);
    [...host.querySelectorAll('button')].find((b) => b.textContent === 'Clear list')!.click();
    await tick(10);
    expect(calls.clear).toBe(1);
    expect(lines()).toEqual([]);
    expect(host.querySelector('.recent-panel .tool-empty')!.hasAttribute('hidden')).toBe(false);
  });

  it('reports a failure to open without leaving the document', async () => {
    const { host, act, button, choose, platform } = await started({ initial: lone('first.md') });
    await act.open();
    await tick(5);
    platform.recent!.open = async () => {
      throw new Error('Access to that file was not granted.');
    };
    button()!.click();
    await tick(5);
    await choose('first.md');
    expect(banner(host).textContent).toContain('Could not open first.md: Access to that file was not granted.');
    expect(docTitle(host)).toBe('opened.md');
  });

  it('keeps the outline, folder search and recent files from stacking', async () => {
    const { host, act, button } = await started();
    await act.openFolder();
    await tick(5);
    host.querySelector<HTMLButtonElement>('button[title^="Outline"]')!.click();
    button()!.click();
    await tick(5);
    expect(host.querySelector<HTMLElement>('.outline-panel')!.hidden).toBe(true);
    expect(host.querySelector<HTMLElement>('.recent-panel')!.hidden).toBe(false);
    host.querySelector<HTMLButtonElement>('button[title^="Search the open folder"]')!.click();
    expect(host.querySelector<HTMLElement>('.recent-panel')!.hidden).toBe(true);
    expect(button()!.getAttribute('aria-pressed')).toBe('false');
  });
});
