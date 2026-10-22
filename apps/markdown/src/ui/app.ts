import type { EditorView } from '@codemirror/view';
import {
  NEW_DOCUMENT_FORMAT,
  decodeDocument,
  encodeDocument,
  readOnlyReason,
  type DocumentFormat,
} from '../core/document';
import { exportHtml } from '../core/export';
import { defaultDocument, documentFor, resolveInFolder, resolveWikiLink, splitHref } from '../core/paths';
import { postprocess } from '../core/postprocess';
import { renderMarkdown } from '../core/render';
import type { Arrival, FileRef, Listing, OpenedFile, Platform } from '../platform/types';
import { el, icon } from './dom';
import { createEditor, documentText, scrollToLine } from './editor';
import { runFormat, type FormatCommand } from './format';
import { Sidebar } from './sidebar';

type ViewMode = 'edit' | 'split' | 'preview';
type ThemeChoice = 'auto' | 'light' | 'dark';

const APP_NAME = 'ZTG Markdown';
const PREVIEW_DELAY_MS = 120;
const MAX_HISTORY = 50;

function stored<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const value = localStorage.getItem(key) as T | null;
    return value && allowed.includes(value) ? value : fallback;
  } catch {
    return fallback;
  }
}
function store(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable (private windows); preferences then last for the session.
  }
}

interface DocumentState {
  ref: FileRef | null;
  format: DocumentFormat;
  readOnly: string | null;
  /** The saved text, so undoing back to it clears the unsaved marker. */
  savedText: string;
}

export class App {
  private view!: EditorView;
  private doc: DocumentState = { ref: null, format: NEW_DOCUMENT_FORMAT, readOnly: null, savedText: '' };
  private listing: Listing | null = null;
  private history: FileRef[] = [];
  private pendingFragment: string | null = null;
  private mode: ViewMode = stored('ztg-md-mode', ['edit', 'split', 'preview'], 'split');
  private theme: ThemeChoice = stored('ztg-md-theme', ['auto', 'light', 'dark'], 'auto');
  private renderTimer: number | undefined;
  private renderGeneration = 0;
  private objectUrls: string[] = [];
  private syncLine = 0;

  private readonly root = el('div', { class: 'app' });
  private readonly titleEl = el('span', { class: 'doc-title' });
  private readonly dirtyEl = el('span', { class: 'dirty-dot', title: 'Unsaved changes', hidden: '' });
  private readonly banner = el('div', { class: 'banner', role: 'status', hidden: '' });
  private readonly editorHost = el('div', { class: 'pane editor-pane' });
  private readonly preview = el('article', { class: 'markdown-body', 'aria-label': 'Preview' });
  private readonly previewPane = el('div', { class: 'pane preview-pane' }, this.preview);
  private readonly status = { cursor: el('span'), words: el('span'), encoding: el('span'), eol: el('span') };
  private readonly modeButtons = new Map<ViewMode, HTMLButtonElement>();
  private readonly formatBar = el('div', { class: 'format-bar', role: 'toolbar', 'aria-label': 'Formatting' });
  private readonly themeButton = el('button', { class: 'icon-button', type: 'button' });
  private readonly backButton = el('button', { class: 'icon-button', type: 'button', title: 'Back (Alt+Left)', 'aria-label': 'Back', disabled: '' }, icon('back'));
  private readonly sidebar = new Sidebar(
    (path) => void this.openFolderPath(path, null, true),
    () => void this.closeFolder(),
  );

  constructor(private readonly platform: Platform) {}

  async start(host: HTMLElement): Promise<void> {
    host.replaceChildren(this.buildChrome());
    this.applyTheme();
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => this.applyTheme());
    this.load(null, '', NEW_DOCUMENT_FORMAT);
    this.platform.onDrop((arrival) => void this.arrive(arrival, false));
    this.platform.onCloseRequested(() => this.confirmDiscard());
    window.addEventListener('keydown', (event) => this.globalShortcut(event));
    this.preview.addEventListener('click', (event) => void this.previewClick(event));
    this.preview.addEventListener('dblclick', (event) => this.previewDoubleClick(event));
    try {
      const initial = await this.platform.initial();
      if (initial) await this.arrive(initial, true);
    } catch (error) {
      this.notify(`Could not open: ${message(error)}`);
    }
  }

  // ---- chrome ---------------------------------------------------------------------------

  private buildChrome(): HTMLElement {
    const button = (label: string, title: string, action: () => void, iconName?: string) => {
      const b = el('button', { type: 'button', class: iconName ? 'icon-button' : 'text-button', title, 'aria-label': title });
      if (iconName) b.append(icon(iconName));
      else b.textContent = label;
      b.addEventListener('click', action);
      return b;
    };

    const modes = el('div', { class: 'segmented', role: 'group', 'aria-label': 'View' });
    for (const [mode, label] of [
      ['edit', 'Edit'],
      ['split', 'Split'],
      ['preview', 'Preview'],
    ] as const) {
      const b = button(label, `${label} view`, () => this.setMode(mode));
      this.modeButtons.set(mode, b);
      modes.append(b);
    }

    this.themeButton.addEventListener('click', () => {
      this.theme = this.theme === 'auto' ? 'light' : this.theme === 'light' ? 'dark' : 'auto';
      store('ztg-md-theme', this.theme);
      this.applyTheme();
    });
    this.backButton.addEventListener('click', () => void this.back());

    const top = el(
      'header',
      { class: 'top-bar' },
      el('span', { class: 'brand', 'aria-hidden': 'true' }, el('span', { class: 'brand-mark' }, 'ZTG'), ' Markdown'),
      this.backButton,
      el('div', { class: 'file-name' }, this.titleEl, this.dirtyEl),
      el(
        'div',
        { class: 'actions' },
        button('New', 'New document (Ctrl+N)', () => void this.newDocument(), 'new'),
        button('Open', 'Open file (Ctrl+O)', () => void this.open(), 'open'),
        button('Open folder', 'Open folder or repository (Ctrl+Shift+O)', () => void this.openFolder(), 'open-folder'),
        button('Save', 'Save (Ctrl+S)', () => void this.save(false), 'save'),
        button('Save as', 'Save as (Ctrl+Shift+S)', () => void this.save(true), 'save-as'),
        el('span', { class: 'divider' }),
        modes,
        el('span', { class: 'divider' }),
        button('Export', 'Export as HTML', () => void this.exportHtml(), 'export'),
        button('Print', 'Print or save as PDF (Ctrl+P)', () => void this.print(), 'print'),
        this.themeButton,
      ),
    );

    const formats: [FormatCommand, string, string][] = [
      ['bold', 'Bold (Ctrl+B)', 'B'],
      ['italic', 'Italic (Ctrl+I)', 'I'],
      ['strike', 'Strikethrough (Ctrl+Shift+X)', 'S'],
      ['code', 'Inline code (Ctrl+E)', '<>'],
      ['link', 'Link (Ctrl+K)', 'Link'],
      ['h1', 'Heading 1 (Ctrl+1)', 'H1'],
      ['h2', 'Heading 2 (Ctrl+2)', 'H2'],
      ['h3', 'Heading 3 (Ctrl+3)', 'H3'],
      ['bullet', 'Bulleted list', '•'],
      ['numbered', 'Numbered list', '1.'],
      ['task', 'Task list', '☐'],
      ['quote', 'Quote', '❝'],
      ['table', 'Table', 'Table'],
      ['codeBlock', 'Code block', '{ }'],
      ['rule', 'Horizontal rule', '―'],
    ];
    for (const [name, title, label] of formats) {
      const b = button(label, title, () => runFormat(this.view, name));
      b.dataset.format = name;
      this.formatBar.append(b);
    }

    const s = this.status;
    const statusBar = el('footer', { class: 'status-bar' }, s.cursor, s.words, el('span', { class: 'spacer' }), s.encoding, s.eol);
    const main = el('main', { class: 'workspace' }, this.sidebar.element, this.editorHost, this.previewPane);
    this.root.append(top, this.formatBar, this.banner, main, statusBar);
    return this.root;
  }

  private applyTheme(): void {
    const dark = this.theme === 'dark' || (this.theme === 'auto' && window.matchMedia('(prefers-color-scheme: dark)').matches);
    document.documentElement.dataset.theme = dark ? 'dark' : 'light';
    this.themeButton.replaceChildren(icon(this.theme === 'auto' ? 'theme-auto' : dark ? 'theme-dark' : 'theme-light'));
    this.themeButton.title = `Theme: ${this.theme} (click to change)`;
    this.themeButton.setAttribute('aria-label', this.themeButton.title);
    if (this.view) this.schedulePreview(0);
  }

  private get dark(): boolean {
    return document.documentElement.dataset.theme === 'dark';
  }

  private setMode(mode: ViewMode): void {
    this.mode = mode;
    store('ztg-md-mode', mode);
    this.root.dataset.mode = mode;
    for (const [m, b] of this.modeButtons) b.setAttribute('aria-pressed', String(m === mode));
    this.formatBar.hidden = mode === 'preview' || this.doc.readOnly !== null;
    if (mode !== 'edit') this.schedulePreview(0);
    if (mode !== 'preview') this.view?.requestMeasure();
  }

  private notify(text: string | null): void {
    this.banner.hidden = text === null;
    this.banner.textContent = text ?? '';
  }

  // ---- documents ------------------------------------------------------------------------

  private load(ref: FileRef | null, text: string, format: DocumentFormat): void {
    const readOnly = readOnlyReason(format);
    this.doc = { ref, format, readOnly, savedText: text };
    this.view?.destroy();
    this.view = createEditor({
      parent: this.editorHost,
      text,
      format,
      readOnly: readOnly !== null,
      onChange: () => {
        this.updateDirty();
        this.schedulePreview(PREVIEW_DELAY_MS);
        this.updateWords();
      },
      onCursor: (line, column) => {
        this.status.cursor.textContent = `Ln ${line}, Col ${column}`;
      },
      onScroll: (line) => this.syncPreview(line),
    });
    this.notify(readOnly);
    this.syncLine = 0;
    this.previewPane.scrollTo({ top: 0 });
    this.status.cursor.textContent = 'Ln 1, Col 1';
    this.status.encoding.textContent = format.validUtf8 ? (format.bom ? 'UTF-8 with BOM' : 'UTF-8') : 'Not UTF-8';
    this.status.encoding.title = 'Saved exactly as opened';
    this.status.eol.textContent = format.mixedEndings ? 'Mixed line endings' : format.eol === '\r\n' ? 'CRLF' : 'LF';
    this.sidebar.select(ref?.folderPath);
    this.updateWords();
    this.updateDirty();
    this.setMode(this.mode);
    this.schedulePreview(0);
  }

  private get dirty(): boolean {
    return documentText(this.view.state) !== this.doc.savedText;
  }

  private updateDirty(): void {
    const dirty = this.dirty;
    this.dirtyEl.hidden = !dirty;
    document.body.dataset.dirty = String(dirty);
    const name = this.doc.ref?.folderPath ?? this.doc.ref?.name ?? 'Untitled.md';
    this.titleEl.textContent = name + (this.doc.readOnly ? ' (read-only)' : '');
    const folder = this.listing ? `${this.listing.folder.name} › ` : '';
    this.platform.setTitle(`${dirty ? '● ' : ''}${folder}${this.doc.ref?.name ?? 'Untitled.md'} - ${APP_NAME}`);
  }

  private updateWords(): void {
    const text = this.view.state.sliceDoc();
    const words = text.match(/[\p{L}\p{N}][\p{L}\p{N}'’_-]*/gu)?.length ?? 0;
    this.status.words.textContent = `${words.toLocaleString()} words`;
  }

  /** Resolves true when it is fine to replace or close the current document. */
  private async confirmDiscard(): Promise<boolean> {
    if (!this.dirty) return true;
    const choice = await confirmDialog(
      `Save changes to ${this.doc.ref?.name ?? 'Untitled.md'}?`,
      'Your changes will be lost if you do not save them.',
      ['Save', "Don't save", 'Cancel'],
    );
    if (choice === 'Cancel' || choice === null) return false;
    if (choice === 'Save') return this.save(false);
    return true;
  }

  /** Shows a file, optionally remembering the current one for Back. */
  private show(file: OpenedFile, fragment: string | null, remember: boolean): void {
    if (remember && this.doc.ref) {
      this.history.push(this.doc.ref);
      if (this.history.length > MAX_HISTORY) this.history.shift();
    }
    this.backButton.disabled = this.history.length === 0;
    this.pendingFragment = fragment;
    const decoded = decodeDocument(file.bytes);
    this.load(file.ref, decoded.text, decoded.format);
  }

  private async arrive(arrival: Arrival, startup: boolean): Promise<void> {
    if (!startup && !(await this.confirmDiscard())) return;
    if (arrival.listing) await this.showFolder(arrival.listing);
    else if (arrival.file) {
      this.setListing(null);
      this.show(arrival.file, null, false);
    }
  }

  private async newDocument(): Promise<void> {
    if (!(await this.confirmDiscard())) return;
    this.history = [];
    this.backButton.disabled = true;
    this.load(null, '', NEW_DOCUMENT_FORMAT);
    this.setMode(this.mode === 'preview' ? 'split' : this.mode);
  }

  private async open(): Promise<void> {
    if (!(await this.confirmDiscard())) return;
    try {
      const file = await this.platform.open();
      if (!file) return;
      this.setListing(null);
      this.show(file, null, false);
    } catch (error) {
      this.notify(`Could not open the file: ${message(error)}`);
    }
  }

  private setListing(listing: Listing | null): void {
    this.listing = listing;
    this.history = [];
    this.backButton.disabled = true;
    this.sidebar.show(listing);
    this.root.classList.toggle('has-folder', listing !== null);
  }

  private async openFolder(): Promise<void> {
    if (!(await this.confirmDiscard())) return;
    try {
      const listing = await this.platform.openFolder();
      if (listing) await this.showFolder(listing);
    } catch (error) {
      this.notify(`Could not open the folder: ${message(error)}`);
    }
  }

  /** A folder opens for reading: its README in the preview, the tree beside it. */
  private async showFolder(listing: Listing): Promise<void> {
    this.setListing(listing);
    const first = defaultDocument(listing.files);
    if (!first) {
      this.load(null, '', NEW_DOCUMENT_FORMAT);
      this.notify(`No Markdown files were found in ${listing.folder.name}.`);
      return;
    }
    if (this.mode === 'edit') this.setMode('preview');
    await this.openFolderPath(first, null, false, true);
    if (listing.truncated) this.notify('This folder is very large; the file list shows the first 20,000 Markdown files.');
  }

  private async closeFolder(): Promise<void> {
    if (!(await this.confirmDiscard())) return;
    this.setListing(null);
    this.load(null, '', NEW_DOCUMENT_FORMAT);
  }

  private async openFolderPath(folderPath: string, fragment: string | null, remember: boolean, confirmed = false): Promise<void> {
    if (!this.listing) return;
    if (folderPath === this.doc.ref?.folderPath) {
      this.scrollToFragment(fragment);
      return;
    }
    if (!confirmed && !(await this.confirmDiscard())) return;
    try {
      this.show(await this.platform.readFolderFile(this.listing, folderPath), fragment, remember);
    } catch (error) {
      this.notify(`Could not open ${folderPath}: ${message(error)}`);
    }
  }

  private async back(): Promise<void> {
    const previous = this.history.at(-1);
    if (!previous || !(await this.confirmDiscard())) return;
    this.history.pop();
    try {
      const file =
        previous.folderPath !== undefined && this.listing
          ? await this.platform.readFolderFile(this.listing, previous.folderPath)
          : await this.platform.reopen(previous);
      if (file) this.show(file, null, false);
      else this.notify(`${previous.name} can no longer be opened.`);
    } catch (error) {
      this.notify(`Could not go back: ${message(error)}`);
    }
    this.backButton.disabled = this.history.length === 0;
  }

  /** Resolves true when the document was written. */
  private async save(as: boolean): Promise<boolean> {
    if (this.doc.readOnly) {
      this.notify(this.doc.readOnly);
      return false;
    }
    const text = documentText(this.view.state);
    try {
      const bytes = encodeDocument(text, this.doc.format);
      const name = this.doc.ref?.name ?? 'Untitled.md';
      const ref = as || !this.doc.ref ? await this.platform.saveAs(bytes, name) : await this.platform.save(this.doc.ref, bytes, name);
      if (!ref) return false;
      // Saving somewhere else makes the file independent of the open folder.
      const kept = ref === this.doc.ref ? ref : { ...ref, folderPath: undefined };
      this.doc = { ...this.doc, ref: kept, savedText: text };
      this.sidebar.select(kept.folderPath);
      this.updateDirty();
      this.notify(null);
      return true;
    } catch (error) {
      this.notify(`Could not save: ${message(error)}`);
      return false;
    }
  }

  private async exportHtml(): Promise<void> {
    try {
      const name = (this.doc.ref?.name ?? 'Untitled.md').replace(/\.(md|markdown|mdown|mkd|mkdn|txt)$/i, '') + '.html';
      const html = await exportHtml(documentText(this.view.state), {
        title: name.replace(/\.html$/, ''),
        loadLocalImage: (src) => this.loadImage(src),
      });
      await this.platform.exportFile(new TextEncoder().encode(html), name, 'html');
    } catch (error) {
      this.notify(`Could not export: ${message(error)}`);
    }
  }

  /** The preview is not kept current in edit mode, so it is rendered before printing. */
  private async print(): Promise<void> {
    window.clearTimeout(this.renderTimer);
    await this.renderPreview();
    window.print();
  }

  private globalShortcut(event: KeyboardEvent): void {
    if (event.altKey && event.key === 'ArrowLeft' && !this.backButton.disabled) {
      event.preventDefault();
      void this.back();
      return;
    }
    if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
    const key = event.key.toLowerCase();
    const actions: Record<string, () => Promise<unknown>> = {
      s: () => this.save(event.shiftKey),
      o: () => (event.shiftKey ? this.openFolder() : this.open()),
      n: () => this.newDocument(),
      p: () => this.print(),
    };
    const action = actions[key];
    if (!action) return;
    event.preventDefault();
    void action();
  }

  // ---- preview --------------------------------------------------------------------------

  /** Resolves an image source written in the document to a displayable URL. */
  private async loadImage(src: string): Promise<string | null> {
    const split = splitHref(src);
    if (!split?.path) return null;
    return this.platform.loadLocalImage(this.doc.ref, split.path, this.listing);
  }

  private schedulePreview(delay: number): void {
    if (this.mode === 'edit') return;
    window.clearTimeout(this.renderTimer);
    this.renderTimer = window.setTimeout(() => void this.renderPreview(), delay);
  }

  private async renderPreview(): Promise<void> {
    const generation = ++this.renderGeneration;
    const container = document.createElement('div');
    container.innerHTML = renderMarkdown(documentText(this.view.state));
    const urls: string[] = [];
    await postprocess(container, {
      mathOutput: 'htmlAndMathml',
      dark: this.dark,
      loadLocalImage: async (src) => {
        const url = await this.loadImage(src);
        if (url) urls.push(url);
        return url;
      },
    });
    if (generation !== this.renderGeneration) {
      urls.forEach((u) => URL.revokeObjectURL(u));
      return;
    }
    this.objectUrls.forEach((u) => URL.revokeObjectURL(u));
    this.objectUrls = urls;
    this.preview.replaceChildren(...container.childNodes);
    if (this.pendingFragment !== null) {
      this.scrollToFragment(this.pendingFragment);
      this.pendingFragment = null;
    } else this.syncPreview(this.syncLine);
  }

  private scrollToFragment(fragment: string | null): void {
    if (!fragment) return;
    const target =
      this.preview.querySelector(`[id="${CSS.escape(fragment)}"]`) ??
      this.preview.querySelector(`[id="${CSS.escape(fragment.toLowerCase())}"]`);
    target?.scrollIntoView({ block: 'start' });
  }

  /** Scrolls the preview so the block at the editor's top line sits at the top. */
  private syncPreview(line: number): void {
    this.syncLine = line;
    if (this.mode !== 'split') return;
    let target: HTMLElement | null = null;
    for (const node of this.preview.querySelectorAll<HTMLElement>('[data-line]')) {
      if (Number(node.dataset.line) > line) break;
      target = node;
    }
    const top = target ? target.offsetTop - this.preview.offsetTop : 0;
    this.previewPane.scrollTo({ top: line === 0 ? 0 : top });
  }

  private async previewClick(event: MouseEvent): Promise<void> {
    const anchor = (event.target as Element).closest('a');
    if (!anchor) return;
    event.preventDefault();
    const ref = this.doc.ref;
    const inFolder = this.listing !== null && ref?.folderPath !== undefined;

    const wiki = anchor.dataset.wikilink;
    if (wiki !== undefined) {
      const [name = '', fragment = null] = wiki.split('#');
      const target = inFolder ? resolveWikiLink(ref!.folderPath!, name, this.listing!.files) : null;
      if (target) await this.openFolderPath(target, fragment, true);
      else this.notify(`No file named "${name}" in the open folder.`);
      return;
    }

    const href = anchor.getAttribute('href') ?? '';
    if (href.startsWith('#')) {
      this.scrollToFragment(splitHref(href)?.fragment ?? null);
      return;
    }
    if (/^(https?:|mailto:)/i.test(href)) {
      void this.platform.openExternal(href);
      return;
    }
    const split = splitHref(href);
    if (!split || !split.path) return;

    if (inFolder) {
      const resolved = resolveInFolder(ref!.folderPath!, split.path);
      const target = resolved === null ? null : documentFor(resolved, this.listing!.files);
      if (target !== null) await this.openFolderPath(target, split.fragment || null, true);
      else if (resolved === null) this.notify(`That link leaves the open folder: ${href}`);
      else this.notify(`Only Markdown files open here: ${href}`);
      return;
    }
    if (!ref || !(await this.confirmDiscard())) return;
    try {
      const file = await this.platform.followLink(ref, split.path);
      if (file) this.show(file, split.fragment || null, true);
      else if (this.platform.kind === 'web') this.notify('Open the folder (Ctrl+Shift+O) to follow links between files.');
    } catch (error) {
      this.notify(`Could not open ${href}: ${message(error)}`);
    }
  }

  /** Double-clicking rendered text moves the editor to its source line. */
  private previewDoubleClick(event: MouseEvent): void {
    if (this.doc.readOnly) return;
    const block = (event.target as Element).closest<HTMLElement>('[data-line]');
    if (!block) return;
    if (this.mode === 'preview') this.setMode('split');
    scrollToLine(this.view, Number(block.dataset.line));
  }
}

function message(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function confirmDialog(title: string, body: string, choices: string[]): Promise<string | null> {
  return new Promise((resolve) => {
    const dialog = el('dialog', { class: 'modal' }) as HTMLDialogElement;
    const buttons = choices.map((choice, i) => {
      const b = el('button', { type: 'button', class: i === 0 ? 'primary' : 'text-button' }, choice);
      b.addEventListener('click', () => {
        dialog.close();
        resolve(choice);
      });
      return b;
    });
    dialog.append(el('h2', {}, title), el('p', {}, body), el('div', { class: 'modal-actions' }, ...buttons));
    dialog.addEventListener('cancel', () => resolve(null));
    dialog.addEventListener('close', () => dialog.remove());
    document.body.append(dialog);
    dialog.showModal();
    buttons[0]?.focus();
  });
}
