import { beforeAll, describe, expect, it, vi } from 'vitest';
import type { OpenedFile, Platform } from '../src/platform/types';
import { App } from '../src/ui/app';

// RD11 in the app: the Copy as rich text button copies the selection in the preview, or the whole
// document when nothing is selected, through the platform's clipboard call, and never touches the
// document. The markup itself is tested in richtext.test.ts.

const enc = (text: string) => new TextEncoder().encode(text);
const SOURCE = '# Title\n\nFirst paragraph.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n> [!NOTE]\n> Careful.\n';
const lone = (text: string): OpenedFile => ({ ref: { name: 'notes.md', path: '/elsewhere/notes.md' }, bytes: enc(text) });

async function started(text: string, copy: Platform['copyRichText'] = async () => undefined) {
  const copied: { html: string; text: string }[] = [];
  const platform: Platform = {
    kind: 'web',
    open: async () => null,
    openFolder: async () => null,
    readFolderFile: async () => {
      throw new Error('no folder');
    },
    followLink: async () => null,
    reopen: async () => null,
    save: async (ref) => ref,
    saveAs: async () => null,
    exportFile: async () => undefined,
    openExternal: async () => undefined,
    loadLocalImage: async () => null,
    copyRichText: async (html, plain) => {
      copied.push({ html, text: plain });
      await copy(html, plain);
    },
    initial: async () => ({ file: lone(text) }),
    remember: () => undefined,
    onDrop: () => undefined,
    onCloseRequested: () => undefined,
    setTitle: () => undefined,
  };
  const host = document.createElement('div');
  document.body.replaceChildren(host);
  await new App(platform).start(host);
  const button = host.querySelector<HTMLButtonElement>('button[aria-label="Copy as rich text"]')!;
  return { host, copied, button };
}

const until = async (done: () => boolean) => {
  for (let i = 0; i < 200 && !done(); i++) await new Promise((resolve) => setTimeout(resolve, 5));
  expect(done()).toBe(true);
};
const banner = (host: HTMLElement) => host.querySelector<HTMLElement>('.banner')!;

beforeAll(() => {
  const query = { matches: false, addEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined };
  window.matchMedia ??= vi.fn(() => query) as unknown as typeof window.matchMedia;
  Element.prototype.scrollTo ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});

describe('Copy as rich text', () => {
  it('copies the whole document when nothing is selected, as HTML with inline styles and as text', async () => {
    window.getSelection()?.removeAllRanges();
    const { host, copied, button } = await started(SOURCE);
    button.click();
    await until(() => copied.length === 1);
    const { html, text } = copied[0]!;
    expect(html).toContain('<table');
    expect(html).toContain('<h1 style=');
    expect(html).toContain('First paragraph.');
    expect(html).toContain('Careful.');
    expect(html).not.toMatch(/class=|var\(|<script/);
    expect(text).toContain('Title');
    expect(text).toContain('a\tb');
    await until(() => banner(host).textContent!.includes('whole document'));
    // A copy is not a change.
    expect(host.querySelector<HTMLElement>('.dirty-dot')!.hidden).toBe(true);
  });

  it('copies the whole document from edit view, where the preview is not kept current', async () => {
    window.getSelection()?.removeAllRanges();
    localStorage.setItem('ztg-readout-mode', 'edit');
    try {
      const { copied, button } = await started(SOURCE);
      button.click();
      await until(() => copied.length === 1);
      expect(copied[0]!.html).toContain('First paragraph.');
    } finally {
      localStorage.removeItem('ztg-readout-mode');
    }
  });

  it('copies only the selection in the preview', async () => {
    const { host, copied, button } = await started(SOURCE);
    await until(() => host.querySelector('.markdown-body p') !== null);
    const paragraph = [...host.querySelectorAll('.markdown-body p')].find((p) => p.textContent === 'First paragraph.')!;
    const range = document.createRange();
    range.selectNodeContents(paragraph);
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    button.click();
    await until(() => copied.length === 1);
    expect(copied[0]!.text).toBe('First paragraph.');
    expect(copied[0]!.html).not.toContain('<table');
    expect(copied[0]!.html).not.toContain('Title');
    await until(() => banner(host).textContent!.includes('selection'));
    selection.removeAllRanges();
  });

  it('ignores a selection in the editor and copies the whole preview', async () => {
    const { host, copied, button } = await started(SOURCE);
    await until(() => host.querySelector('.markdown-body p') !== null);
    const line = host.querySelector('.cm-line')!;
    const range = document.createRange();
    range.selectNodeContents(line);
    const selection = window.getSelection()!;
    selection.removeAllRanges();
    selection.addRange(range);
    button.click();
    await until(() => copied.length === 1);
    expect(copied[0]!.html).toContain('First paragraph.');
    selection.removeAllRanges();
  });

  it('keeps the preview selection when the button is pressed', async () => {
    const { button } = await started(SOURCE);
    const down = new MouseEvent('mousedown', { bubbles: true, cancelable: true });
    button.dispatchEvent(down);
    expect(down.defaultPrevented).toBe(true);
  });

  it('says so when there is nothing to copy, and when the clipboard refuses', async () => {
    window.getSelection()?.removeAllRanges();
    const empty = await started('');
    empty.button.click();
    await until(() => banner(empty.host).textContent!.includes('nothing to copy'));
    expect(empty.copied).toEqual([]);

    const refused = await started(SOURCE, async () => Promise.reject(new Error('The clipboard refused the copy.')));
    refused.button.click();
    await until(() => banner(refused.host).textContent!.includes('Could not copy'));
    expect(banner(refused.host).textContent).toContain('The clipboard refused the copy.');
  });
});
