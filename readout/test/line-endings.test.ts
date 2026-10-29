import { beforeAll, describe, expect, it, vi } from 'vitest';
import {
  canConvertLineEndings,
  convertDocument,
  countLineEndings,
  decodeDocument,
  encodeDocument,
  majorityEnding,
  normalizeLineEndings,
  readOnlyReason,
} from '../src/core/document';
import type { OpenedFile, Platform } from '../src/platform/types';
import { App } from '../src/ui/app';

// RD15: a file with mixed line endings opens read-only, and the reader can convert it. The core
// rules (what counts, what is kept, what is refused) are tested directly; the user-visible flow
// (read-only until the explicit choice, unsaved after it, written only by Save, the reload path)
// is tested through the App with a fake platform.

const enc = (text: string) => new TextEncoder().encode(text);
const dec = (bytes: Uint8Array) => new TextDecoder().decode(bytes);
const BOM = [0xef, 0xbb, 0xbf];

describe('counting and choosing an ending', () => {
  it('counts a CRLF once and a lone CR on its own', () => {
    expect(countLineEndings('a\r\nb\nc\rd\r\n')).toEqual({ crlf: 2, lf: 1, cr: 1 });
    expect(countLineEndings('no break')).toEqual({ crlf: 0, lf: 0, cr: 0 });
  });

  it('chooses the majority ending, LF on a tie, for lone CRs and for no line break', () => {
    expect(majorityEnding({ crlf: 5, lf: 2, cr: 0 })).toBe('\r\n');
    expect(majorityEnding({ crlf: 2, lf: 5, cr: 0 })).toBe('\n');
    expect(majorityEnding({ crlf: 3, lf: 3, cr: 0 })).toBe('\n');
    expect(majorityEnding({ crlf: 0, lf: 0, cr: 4 })).toBe('\n');
    expect(majorityEnding({ crlf: 0, lf: 0, cr: 0 })).toBe('\n');
    expect(majorityEnding({ crlf: 2, lf: 1, cr: 3 })).toBe('\n');
  });

  it('records the counts when a file is decoded', () => {
    expect(decodeDocument(enc('a\r\nb\r\nc\n')).format.endings).toEqual({ crlf: 2, lf: 1, cr: 0 });
  });
});

describe('convertDocument', () => {
  const mixed = 'one\r\ntwo\nthree\rfour\r\n\nlast';

  it('turns a mixed file into LF', () => {
    const { format } = decodeDocument(enc(mixed));
    const out = convertDocument(mixed, format, '\n');
    expect(out.text).toBe('one\ntwo\nthree\nfour\n\nlast');
    expect(out.format).toMatchObject({ eol: '\n', mixedEndings: false, validUtf8: true });
    expect(out.format.endings).toEqual({ crlf: 0, lf: 5, cr: 0 });
  });

  it('turns a mixed file into CRLF, including a lone CR, and keeps a missing final newline', () => {
    const { format } = decodeDocument(enc(mixed));
    const out = convertDocument(mixed, format, '\r\n');
    expect(out.text).toBe('one\r\ntwo\r\nthree\r\nfour\r\n\r\nlast');
    expect(out.format.eol).toBe('\r\n');
    expect(out.format.mixedEndings).toBe(false);
    expect(decodeDocument(encodeDocument(out.text, out.format)).format.mixedEndings).toBe(false);
  });

  it('is already one ending in a file that was all LF or all CRLF, and changes only line endings', () => {
    const lf = decodeDocument(enc('# T\n\n| a | b |\n|---|---|\n'));
    expect(convertDocument(lf.text, lf.format, '\n').text).toBe(lf.text);
    expect(convertDocument(lf.text, lf.format, '\r\n').text).toBe('# T\r\n\r\n| a | b |\r\n|---|---|\r\n');
    const crlf = decodeDocument(enc('a\r\nb\r\n'));
    expect(convertDocument(crlf.text, crlf.format, '\r\n').text).toBe(crlf.text);
    expect(convertDocument(crlf.text, crlf.format, '\n').text).toBe('a\nb\n');
  });

  it('converts a file of lone CRs', () => {
    const { text, format } = decodeDocument(enc('a\rb\rc'));
    expect(format.mixedEndings).toBe(true);
    expect(convertDocument(text, format, '\n').text).toBe('a\nb\nc');
  });

  it('keeps the byte order mark and every other character', () => {
    const bytes = new Uint8Array([...BOM, ...enc('café   \u{1F600}\r\nb\nc')]);
    const { text, format } = decodeDocument(bytes);
    expect(format.bom).toBe(true);
    const out = convertDocument(text, format, '\r\n');
    expect(out.format.bom).toBe(true);
    const saved = encodeDocument(out.text, out.format);
    expect([...saved.subarray(0, 3)]).toEqual(BOM);
    expect(dec(saved.subarray(3))).toBe('café   \u{1F600}\r\nb\r\nc');
  });

  it('refuses a document that is not valid UTF-8', () => {
    const { text, format } = decodeDocument(new Uint8Array([0x61, 0x0d, 0x0a, 0xff, 0x0a]));
    expect(format.validUtf8).toBe(false);
    expect(format.mixedEndings).toBe(true);
    expect(canConvertLineEndings(format)).toBe(false);
    expect(() => convertDocument(text, format, '\n')).toThrow(/not valid UTF-8/);
  });

  it('lifts the read-only reason only through the conversion', () => {
    const { text, format } = decodeDocument(enc('a\r\nb\n'));
    expect(readOnlyReason(format)).toMatch(/mixes line endings/);
    expect(() => encodeDocument(text, format)).toThrow();
    expect(readOnlyReason(convertDocument(text, format, '\n').format)).toBeNull();
  });

  it('normalizeLineEndings leaves text without line breaks alone', () => {
    expect(normalizeLineEndings('plain', '\r\n')).toBe('plain');
  });
});

// ---- through the App -------------------------------------------------------------------------

const MIXED = '# Title\r\nline two\r\nline three\nline four\r\n';
const NOTES = { name: 'notes.md', path: '/elsewhere/notes.md' };
const file = (bytes: Uint8Array): OpenedFile => ({ ref: NOTES, bytes });

interface Rig {
  host: HTMLElement;
  saved: Uint8Array[];
  disk: { bytes: Uint8Array };
  act: { save(as: boolean): Promise<boolean>; watcher: { tick(force?: boolean): Promise<void> } };
  view(): { state: { readOnly: boolean; sliceDoc(): string }; dispatch(spec: unknown): void };
}

async function started(initial: Uint8Array): Promise<Rig> {
  const saved: Uint8Array[] = [];
  const disk = { bytes: initial };
  const platform: Platform = {
    kind: 'desktop',
    open: async () => null,
    openFolder: async () => null,
    readFolderFile: async () => {
      throw new Error('no folder');
    },
    followLink: async () => null,
    reopen: async () => file(disk.bytes),
    save: async (ref, bytes) => {
      saved.push(bytes);
      disk.bytes = bytes;
      return ref;
    },
    saveAs: async () => null,
    exportFile: async () => undefined,
    openExternal: async () => undefined,
    loadLocalImage: async () => null,
    copyRichText: async () => undefined,
    initial: async () => ({ file: file(initial) }),
    remember: () => undefined,
    onDrop: () => undefined,
    onCloseRequested: () => undefined,
    setTitle: () => undefined,
  };
  const host = document.createElement('div');
  document.body.replaceChildren(host);
  const app = new App(platform);
  await app.start(host);
  const internals = app as unknown as { view: ReturnType<Rig['view']> };
  return { host, saved, disk, act: app as unknown as Rig['act'], view: () => internals.view };
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const banner = (host: HTMLElement) => host.querySelector<HTMLElement>('.banner')!;
const title = (host: HTMLElement) => host.querySelector('.doc-title')!.textContent;
const dirtyDot = (host: HTMLElement) => host.querySelector<HTMLElement>('.dirty-dot')!;
const dialogButtons = () => [...document.querySelectorAll<HTMLButtonElement>('dialog.modal button')];
const clickDialog = async (label: RegExp) => {
  const button = dialogButtons().find((b) => label.test(b.textContent ?? ''));
  expect(button, `a dialog button matching ${label}`).toBeDefined();
  button!.click();
  await settle();
};
const status = (host: HTMLElement) => host.querySelector('.status-bar')!.textContent;

beforeAll(() => {
  const query = { matches: false, addEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined };
  window.matchMedia ??= vi.fn(() => query) as unknown as typeof window.matchMedia;
  Element.prototype.scrollTo ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  // jsdom has no modal dialog; the app only needs it to open, close and report the close.
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.removeAttribute('open');
    this.dispatchEvent(new Event('close'));
  };
});

describe('a file with mixed line endings in the app', () => {
  it('opens read-only and unsaved-free, and offers the conversion without doing it', async () => {
    const rig = await started(enc(MIXED));
    expect(title(rig.host)).toContain('(read-only)');
    expect(rig.view().state.readOnly).toBe(true);
    // Opening a file is not a change: no unsaved marker.
    expect(dirtyDot(rig.host).hidden).toBe(true);
    expect(banner(rig.host).hidden).toBe(false);
    expect(banner(rig.host).textContent).toContain('mixes line endings');
    expect(banner(rig.host).querySelector('button')!.textContent).toBe('Convert line endings');
    expect(status(rig.host)).toContain('Mixed line endings');
    // Saving is refused while it is read-only.
    expect(await rig.act.save(false)).toBe(false);
    expect(rig.saved).toEqual([]);
  });

  it('does not offer the conversion for a file that is not valid UTF-8', async () => {
    const rig = await started(new Uint8Array([0x23, 0x20, 0xff, 0x0d, 0x0a, 0x61, 0x0a]));
    expect(banner(rig.host).textContent).toContain('not valid UTF-8');
    expect(banner(rig.host).querySelector('button')).toBeNull();
  });

  it('offers the majority ending first, and Cancel changes nothing', async () => {
    const rig = await started(enc(MIXED));
    banner(rig.host).querySelector('button')!.click();
    await settle();
    const labels = dialogButtons().map((b) => b.textContent);
    expect(labels).toEqual(['Convert to CRLF (most lines)', 'Convert to LF', 'Cancel']);
    expect(dialogButtons()[0]!.className).toBe('primary');
    expect(document.querySelector('dialog.modal')!.textContent).toContain('3 CRLF, 1 LF');
    await clickDialog(/Cancel/);
    expect(rig.view().state.readOnly).toBe(true);
    expect(title(rig.host)).toContain('(read-only)');
    expect(dirtyDot(rig.host).hidden).toBe(true);
    expect(rig.saved).toEqual([]);
  });

  it('offers LF first when LF is the majority', async () => {
    const rig = await started(enc('a\nb\nc\r\nd\n'));
    banner(rig.host).querySelector('button')!.click();
    await settle();
    expect(dialogButtons().map((b) => b.textContent)).toEqual(['Convert to LF (most lines)', 'Convert to CRLF', 'Cancel']);
    await clickDialog(/Cancel/);
  });

  it('converts in memory: editable and unsaved, and nothing is written until Save', async () => {
    const original = new Uint8Array([...BOM, ...enc(MIXED.slice(0, -2))]);
    const rig = await started(original);
    banner(rig.host).querySelector('button')!.click();
    await settle();
    await clickDialog(/Convert to CRLF/);

    expect(rig.view().state.readOnly).toBe(false);
    expect(rig.host.querySelector('.cm-content')!.getAttribute('contenteditable')).toBe('true');
    expect(title(rig.host)).not.toContain('read-only');
    expect(dirtyDot(rig.host).hidden).toBe(false);
    expect(document.body.dataset.dirty).toBe('true');
    expect(status(rig.host)).toContain('CRLF (converted, not saved)');
    expect(banner(rig.host).textContent).toContain('unchanged until you save');
    expect(rig.view().state.sliceDoc()).toBe('# Title\r\nline two\r\nline three\r\nline four');
    // Never silently: the disk still has the original bytes and no save happened.
    expect(rig.saved).toEqual([]);
    expect(rig.disk.bytes).toBe(original);

    expect(await rig.act.save(false)).toBe(true);
    expect(rig.saved).toHaveLength(1);
    const bytes = rig.saved[0]!;
    expect([...bytes.subarray(0, 3)]).toEqual(BOM);
    expect(dec(bytes.subarray(3))).toBe('# Title\r\nline two\r\nline three\r\nline four');
    expect(dirtyDot(rig.host).hidden).toBe(true);
    expect(status(rig.host)).not.toContain('converted');
    expect(status(rig.host)).toContain('UTF-8 with BOM');
  });

  it('stays unsaved after the conversion even when the text is unchanged by it, and after an edit and undo', async () => {
    // Only LF and a lone CR: converting to LF changes the bytes but the editor text is the same.
    const rig = await started(enc('a\nb\rc\n'));
    banner(rig.host).querySelector('button')!.click();
    await settle();
    await clickDialog(/Convert to LF/);
    expect(dirtyDot(rig.host).hidden).toBe(false);
    expect(await rig.act.save(false)).toBe(true);
    expect(dec(rig.saved[0]!)).toBe('a\nb\nc\n');
  });

  it('converts to LF when the reader picks the other ending', async () => {
    const rig = await started(enc(MIXED));
    banner(rig.host).querySelector('button')!.click();
    await settle();
    await clickDialog(/Convert to LF/);
    expect(rig.view().state.sliceDoc()).toBe('# Title\nline two\nline three\nline four\n');
    expect(status(rig.host)).toContain('LF (converted, not saved)');
    await rig.act.save(false);
    expect(dec(rig.saved[0]!)).toBe('# Title\nline two\nline three\nline four\n');
  });
});

describe('a file with mixed line endings that changes on disk', () => {
  it('reloads a clean read-only document and still offers the conversion', async () => {
    const rig = await started(enc(MIXED));
    rig.disk.bytes = enc('x\r\ny\nz\r\n');
    await rig.act.watcher.tick(true);
    expect(rig.view().state.sliceDoc()).toBe('x\ny\nz\n');
    expect(rig.view().state.readOnly).toBe(true);
    expect(banner(rig.host).querySelector('button')!.textContent).toBe('Convert line endings');
    expect(dirtyDot(rig.host).hidden).toBe(true);
  });

  it('drops the read-only state when the disk version is clean, with the ordinary reload notice', async () => {
    const rig = await started(enc(MIXED));
    rig.disk.bytes = enc('x\ny\n');
    await rig.act.watcher.tick(true);
    expect(rig.view().state.readOnly).toBe(false);
    expect(banner(rig.host).textContent).toContain('changed on disk and was reloaded');
    expect(banner(rig.host).querySelector('button')).toBeNull();
  });

  it('treats a converted, unsaved document as having unsaved edits: asks, never reloads silently', async () => {
    const rig = await started(enc(MIXED));
    banner(rig.host).querySelector('button')!.click();
    await settle();
    await clickDialog(/Convert to CRLF/);

    rig.disk.bytes = enc('changed\nelsewhere\n');
    const tick = rig.act.watcher.tick(true);
    await settle();
    expect(document.querySelector('dialog.modal h2')!.textContent).toContain('changed on disk');
    // Keep my version: the conversion stays, still unsaved.
    await clickDialog(/Keep my version/);
    await tick;
    expect(rig.view().state.sliceDoc()).toBe('# Title\r\nline two\r\nline three\r\nline four\r\n');
    expect(dirtyDot(rig.host).hidden).toBe(false);
    expect(rig.saved).toEqual([]);
  });

  it('loading the disk version after a conversion discards it and is read-only again if the disk is mixed', async () => {
    const rig = await started(enc(MIXED));
    banner(rig.host).querySelector('button')!.click();
    await settle();
    await clickDialog(/Convert to LF/);

    rig.disk.bytes = enc('p\r\nq\nr\r\n');
    const tick = rig.act.watcher.tick(true);
    await settle();
    await clickDialog(/Load disk version/);
    await tick;
    expect(rig.view().state.readOnly).toBe(true);
    expect(dirtyDot(rig.host).hidden).toBe(true);
    expect(banner(rig.host).querySelector('button')!.textContent).toBe('Convert line endings');
    expect(status(rig.host)).not.toContain('converted');
  });
});
