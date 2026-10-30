import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import {
  MAX_CLIENT_LENGTH,
  MAX_REMEMBERED,
  PRINT_CLIENT_KEY,
  PRINT_CLIENT_PROPERTY,
  PRINT_DATE_PROPERTY,
  PRINT_TITLE_PROPERTY,
  cleanText,
  clearPrintTemplate,
  cssString,
  forgetClient,
  printDate,
  printKey,
  printTitle,
  rememberClient,
  rememberedClient,
  setPrintTemplate,
} from '../src/core/print';
import { printOptionsDialog } from '../src/ui/print-dialog';
import { dialogState } from '../src/ui/dialog';

// RD12: the print template's text and the remembered client name. The text reaches the page as a
// quoted CSS string, so the point of most of these tests is that nothing in it can end the string.

class MemoryStore {
  data = new Map<string, string>();
  getItem = (key: string) => this.data.get(key) ?? null;
  setItem = (key: string, value: string) => void this.data.set(key, value);
}
const throwing = {
  getItem: () => {
    throw new Error('blocked');
  },
  setItem: () => {
    throw new Error('blocked');
  },
};

describe('cleanText and cssString', () => {
  it('makes one plain line', () => {
    expect(cleanText('  two\n\tlines \u0000here\u007f ', 80)).toBe('two lines here');
    expect(cleanText('a\u2028b\u2029c', 80)).toBe('a b c');
  });

  it('removes characters that reorder text', () => {
    expect(cleanText('Acme \u202eCo\u202c \u2066x\u2069 \u200e', 80)).toBe('Acme Co x');
  });

  it('cuts to a number of characters, not UTF-16 units, and says so', () => {
    expect(cleanText('abcdef', 4)).toBe('abc\u2026');
    expect(cleanText('abcd', 4)).toBe('abcd');
    const emoji = '\u{1F600}'.repeat(6);
    expect([...cleanText(emoji, 4)]).toHaveLength(4);
    expect(cleanText(emoji, 4).startsWith('\u{1F600}\u{1F600}\u{1F600}')).toBe(true);
  });

  it('quotes a string so that quotes, backslashes and newlines cannot end it', () => {
    expect(cssString('plain', 80)).toBe('"plain"');
    expect(cssString('say "hi"', 80)).toBe('"say \\"hi\\""');
    expect(cssString('a\\b', 80)).toBe('"a\\\\b"');
    expect(cssString('x";}@import url(//evil)/*', 80)).toBe('"x\\";}@import url(//evil)/*"');
    expect(cssString('line1\nline2\\', 80)).toBe('"line1 line2\\\\"');
  });

  it('only ever yields one string token, whatever it is given', () => {
    const nasty = ['"', '\\', '\\"', '"\\', '\n"', '\\\n', '";}</style>', '\u0000\u001f"\u2028\\'];
    for (const text of nasty) {
      const quoted = cssString(text, 80);
      // Remove every escape pair; what is left between the outer quotes holds no quote or backslash.
      const inner = quoted.slice(1, -1).replace(/\\[\\"]/g, '');
      expect(quoted.startsWith('"') && quoted.endsWith('"')).toBe(true);
      expect(inner).not.toMatch(/["\\\n\r]/);
    }
  });
});

describe('print date and title', () => {
  it('writes the local calendar date as year-month-day', () => {
    expect(printDate(new Date(2026, 0, 5, 23, 59))).toBe('2026-01-05');
    expect(printDate(new Date(2026, 10, 1, 0, 0))).toBe('2026-11-01');
  });

  it('uses the first heading, else the file name without its extension', () => {
    const preview = document.createElement('div');
    preview.innerHTML = '<p>Intro</p><h1>Quarterly <em>Review</em></h1><h1>Second</h1>';
    expect(printTitle(preview, 'x.md')).toBe('Quarterly Review');
    expect(printTitle(document.createElement('div'), 'Plan 2026.markdown')).toBe('Plan 2026');
    expect(printTitle(document.createElement('div'), 'notes.txt')).toBe('notes');
    const empty = document.createElement('div');
    empty.innerHTML = '<h1>   </h1>';
    expect(printTitle(empty, 'fallback.md')).toBe('fallback');
  });

  it('never lets a heading in the document be markup', () => {
    const preview = document.createElement('div');
    preview.innerHTML = '<h1>&lt;img src=x onerror=alert(1)&gt;</h1>';
    expect(printTitle(preview, 'a.md')).toBe('<img src=x onerror=alert(1)>');
    const root = document.createElement('html');
    setPrintTemplate(root, { title: printTitle(preview, 'a.md'), client: '', date: '2026-10-30' });
    expect(root.querySelector('img')).toBeNull();
  });
});

describe('the template on the page', () => {
  it('sets three custom properties, each a quoted string, and clears them', () => {
    const root = document.createElement('html');
    setPrintTemplate(root, { title: 'Report "A"', client: 'Acme', date: '2026-10-30' });
    expect(root.style.getPropertyValue('--print-title')).toBe('"Report \\"A\\""');
    expect(root.style.getPropertyValue('--print-client')).toBe('"Client: Acme"');
    expect(root.style.getPropertyValue('--print-date')).toBe('"Printed 2026-10-30"');
    clearPrintTemplate(root);
    expect(root.getAttribute('style') ?? '').not.toContain('--print');
  });

  it('prints no client text when there is no client name', () => {
    const root = document.createElement('html');
    setPrintTemplate(root, { title: 'T', client: '   ', date: '2026-10-30' });
    expect(root.style.getPropertyValue('--print-client')).toBe('""');
  });

  it('cuts a long client name and title', () => {
    const root = document.createElement('html');
    setPrintTemplate(root, { title: 't'.repeat(500), client: 'c'.repeat(500), date: '2026-10-30' });
    expect(root.style.getPropertyValue('--print-client').length).toBeLessThanOrEqual(MAX_CLIENT_LENGTH + 20);
    expect(root.style.getPropertyValue('--print-title').length).toBeLessThan(120);
  });
});

describe('remembered client names', () => {
  it('keys a document by its desktop path, else its folder path, else its name', () => {
    expect(printKey({ name: 'a.md', path: 'C:\\docs\\a.md', folderPath: 'a.md' })).toBe('C:\\docs\\a.md');
    expect(printKey({ name: 'a.md', folderPath: 'docs/a.md' })).toBe('docs/a.md');
    expect(printKey({ name: 'a.md' })).toBe('a.md');
    expect(printKey(null)).toBeNull();
    expect(printKey({ name: '' })).toBeNull();
  });

  it('remembers a name per document, and an empty name forgets it', () => {
    const store = new MemoryStore();
    rememberClient('/a.md', 'Acme', store);
    rememberClient('/b.md', 'Beta', store);
    expect(rememberedClient('/a.md', store)).toBe('Acme');
    expect(rememberedClient('/b.md', store)).toBe('Beta');
    expect(rememberedClient('/c.md', store)).toBe('');
    rememberClient('/a.md', '  ', store);
    expect(rememberedClient('/a.md', store)).toBe('');
    expect(rememberedClient('/b.md', store)).toBe('Beta');
    forgetClient('/b.md', store);
    expect(store.getItem(PRINT_CLIENT_KEY)).toBe('[]');
  });

  it('remembers nothing for a document without a key', () => {
    const store = new MemoryStore();
    rememberClient(null, 'Acme', store);
    expect(store.data.size).toBe(0);
    expect(rememberedClient(null, store)).toBe('');
  });

  it('stores a cleaned name, one line, at most the limit', () => {
    const store = new MemoryStore();
    rememberClient('/a.md', `Acme\n${'x'.repeat(300)}`, store);
    const name = rememberedClient('/a.md', store);
    expect(name).not.toContain('\n');
    expect([...name].length).toBeLessThanOrEqual(MAX_CLIENT_LENGTH);
  });

  it('keeps only the most recently used documents', () => {
    const store = new MemoryStore();
    for (let i = 0; i < MAX_REMEMBERED + 5; i++) rememberClient(`/d${i}.md`, `c${i}`, store);
    expect(rememberedClient('/d0.md', store)).toBe('');
    expect(rememberedClient('/d4.md', store)).toBe('');
    expect(rememberedClient('/d5.md', store)).toBe('c5');
    expect(rememberedClient(`/d${MAX_REMEMBERED + 4}.md`, store)).toBe(`c${MAX_REMEMBERED + 4}`);
    // Setting a name again makes it the newest.
    rememberClient('/d5.md', 'again', store);
    rememberClient('/new.md', 'n', store);
    expect(rememberedClient('/d5.md', store)).toBe('again');
    expect(rememberedClient('/d6.md', store)).toBe('');
  });

  it('survives storage that is damaged, shaped wrongly, or unavailable', () => {
    for (const bad of ['{not json', 'null', '{"a":"b"}', '[1,2]', '[["a"]]', '[["a",5]]', '[["__proto__","x"],["a","ok"]]']) {
      const store = new MemoryStore();
      store.setItem(PRINT_CLIENT_KEY, bad);
      expect(() => rememberedClient('a', store)).not.toThrow();
      rememberClient('b', 'Beta', store);
      expect(rememberedClient('b', store)).toBe('Beta');
    }
    const store = new MemoryStore();
    store.setItem(PRINT_CLIENT_KEY, '[["__proto__","x"]]');
    expect(rememberedClient('toString', store)).toBe('');
    expect(rememberedClient('a', throwing)).toBe('');
    expect(() => rememberClient('a', 'x', throwing)).not.toThrow();
    expect(() => forgetClient('a', throwing)).not.toThrow();
    expect(rememberedClient('a', null)).toBe('');
  });

  it('cleans what it reads back, so a hand-edited store cannot add control characters', () => {
    const store = new MemoryStore();
    store.setItem(PRINT_CLIENT_KEY, JSON.stringify([['a', 'Acme\u202e\nCo']]));
    expect(rememberedClient('a', store)).toBe('Acme Co');
  });
});

// ---- the dialog ---------------------------------------------------------------------------------

beforeAll(() => {
  // jsdom has no modal dialog; the dialog only needs it to open, close and report the close.
  HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
    this.removeAttribute('open');
    this.dispatchEvent(new Event('close'));
  };
});
afterEach(() => {
  document.body.innerHTML = '';
  dialogState.open = 0;
});

const dialog = () => document.querySelector<HTMLDialogElement>('dialog.print-options')!;
const field = () => document.querySelector<HTMLInputElement>('#print-options-client')!;
const buttonNamed = (name: string) => [...dialog().querySelectorAll('button')].find((b) => b.textContent === name)!;
const key = (target: Element, name: string, init: KeyboardEventInit = {}) => target.dispatchEvent(new KeyboardEvent('keydown', { key: name, bubbles: true, cancelable: true, ...init }));

describe('the print options dialog', () => {
  it('is named by its heading, labels its field, and starts in the field with the remembered name', () => {
    void printOptionsDialog({ client: 'Acme', remembered: true, onClear: () => undefined });
    expect(dialogState.open).toBe(1);
    const d = dialog();
    expect(d.getAttribute('aria-labelledby')).toBe('print-options-title');
    expect(d.querySelector('#print-options-title')!.textContent).toBe('Print options');
    expect(d.querySelector('label[for="print-options-client"]')!.textContent).toBe('Client name (optional)');
    expect(field().value).toBe('Acme');
    expect(field().maxLength).toBe(MAX_CLIENT_LENGTH);
    expect(document.activeElement).toBe(field());
    expect(d.querySelector('#print-options-body')!.textContent).toContain('never read from the document');
  });

  it('prints with the typed name, cleaned, when Print is chosen or Enter is pressed', async () => {
    const first = printOptionsDialog({ client: '', remembered: true, onClear: () => undefined });
    field().value = '  Contoso \n Ltd ';
    buttonNamed('Print').click();
    expect(await first).toBe('Contoso Ltd');
    expect(dialogState.open).toBe(0);
    expect(document.querySelector('dialog')).toBeNull();

    const second = printOptionsDialog({ client: '', remembered: true, onClear: () => undefined });
    field().value = 'Beta';
    key(field(), 'Enter');
    expect(await second).toBe('Beta');
  });

  it('can print with no client name at all', async () => {
    const result = printOptionsDialog({ client: 'Old', remembered: true, onClear: () => undefined });
    field().value = '';
    buttonNamed('Print').click();
    expect(await result).toBe('');
  });

  it('cancels with the button and with Escape, and prints nothing', async () => {
    const byButton = printOptionsDialog({ client: 'Acme', remembered: true, onClear: () => undefined });
    buttonNamed('Cancel').click();
    expect(await byButton).toBeNull();

    const byEscape = printOptionsDialog({ client: 'Acme', remembered: true, onClear: () => undefined });
    dialog().dispatchEvent(new Event('cancel'));
    dialog().close();
    expect(await byEscape).toBeNull();
  });

  it('clears the field and tells the caller to forget the name, without closing', () => {
    const onClear = vi.fn();
    void printOptionsDialog({ client: 'Acme', remembered: true, onClear });
    buttonNamed('Clear').click();
    expect(onClear).toHaveBeenCalledTimes(1);
    expect(field().value).toBe('');
    expect(document.activeElement).toBe(field());
    expect(dialog().querySelector('[role="status"]')!.textContent).toBe('Client name cleared and forgotten.');
    expect(dialog().open).toBe(true);
  });

  it('says when the name cannot be remembered', () => {
    void printOptionsDialog({ client: '', remembered: false, onClear: () => undefined });
    expect(dialog().querySelector('#print-options-body')!.textContent).toContain('not saved yet');
  });

  it('wraps Tab and Shift+Tab inside the dialog', () => {
    void printOptionsDialog({ client: '', remembered: true, onClear: () => undefined });
    const controls = [...dialog().querySelectorAll<HTMLElement>('input, button')];
    expect(controls.map((c) => c.textContent || c.tagName)).toEqual(['INPUT', 'Clear', 'Cancel', 'Print']);
    controls[3]!.focus();
    const forward = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
    controls[3]!.dispatchEvent(forward);
    expect(forward.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(controls[0]);
    const back = new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true });
    controls[0]!.dispatchEvent(back);
    expect(back.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(controls[3]);
  });

  it('puts the remembered name in the field as text, never as markup', () => {
    void printOptionsDialog({ client: '"><img src=x onerror=alert(1)>', remembered: true, onClear: () => undefined });
    expect(dialog().querySelector('img')).toBeNull();
    expect(field().value).toBe('"><img src=x onerror=alert(1)>');
  });
});

// ---- the stylesheets ------------------------------------------------------------------------------

describe('the print stylesheets', () => {
  const read = (path: string) => readFileSync(join(__dirname, '..', path), 'utf8');
  const printCss = read('src/styles/print.css');
  const previewCss = read('src/styles/preview.css');
  const tokens = Object.values((JSON.parse(readFileSync(join(__dirname, '..', '..', 'design-tokens.json'), 'utf8')) as { tokens: Record<string, string> }).tokens).map((v) => v.toLowerCase());

  it('draws the header and footer in page margin boxes from the properties the app sets', () => {
    for (const box of ['@top-left', '@top-center', '@top-right', '@bottom-left', '@bottom-right']) expect(printCss).toContain(box);
    for (const property of [PRINT_TITLE_PROPERTY, PRINT_CLIENT_PROPERTY, PRINT_DATE_PROPERTY]) expect(printCss).toContain(`var(${property}`);
    expect(printCss).toContain("'Page ' counter(page) ' of ' counter(pages)");
  });

  it('uses only ZTG palette colours, and fetches nothing', () => {
    const colours = [...printCss.matchAll(/#[0-9a-f]{6}\b/gi)].map((m) => m[0].toLowerCase());
    expect(colours.length).toBeGreaterThan(3);
    // White paper is the one colour that is not a token.
    for (const colour of colours) if (colour !== '#ffffff') expect(tokens, colour).toContain(colour);
    expect(printCss).not.toMatch(/url\(|@import/);
  });

  it('never prints the dark colours, and keeps blocks whole', () => {
    expect(previewCss).toMatch(/@media not print \{\s*\[data-theme='dark'\]/);
    const print = previewCss.slice(previewCss.lastIndexOf('@media print'));
    for (const rule of ['break-after: avoid', 'break-inside: avoid', 'print-color-adjust: exact', 'table-header-group', 'white-space: pre-wrap']) expect(print).toContain(rule);
  });
});
