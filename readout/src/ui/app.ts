import type { EditorView } from '@codemirror/view';
import {
  NEW_DOCUMENT_FORMAT,
  canConvertLineEndings,
  convertDocument,
  decodeDocument,
  encodeDocument,
  majorityEnding,
  readOnlyReason,
  type DocumentFormat,
  type LineEnding,
} from '../core/document';
import { exportHtml } from '../core/export';
import { extractOutline } from '../core/outline';
import { ExternalChangeWatcher, type WatchHost } from '../core/reload';
import { defaultDocument, documentFor, LISTED_EXTENSIONS, opensInReadout, resolveInFolder, resolveWikiLink, splitHref } from '../core/paths';
import { postprocess } from '../core/postprocess';
import { clearPrintTemplate, forgetClient, printDate, printKey, printTitle, rememberClient, rememberedClient, setPrintTemplate } from '../core/print';
import { describeRemoteImages, summarizeRemoteImages } from '../core/remote-images';
import { renderMarkdown } from '../core/render';
import { EchoGuard, lineAtOffset, previewBlocks } from '../core/scrollsync';
import { blobUrlToDataUrl, buildRichText, fragmentForRange, isEmptyFragment } from '../core/richtext';
import { DEFAULT_BOUNDS, type SearchMatch } from '../core/search';
import type { Arrival, FileRef, ImageSource, LinkedFileCheck, Listing, OpenedFile, PendingSession, Platform, RecentEntry } from '../platform/types';
import { IMAGE_REFUSED, MAX_IMAGE_BYTES, imageKindFromMime, imageMarkdown, imageProblem } from '../core/images';
import { dialogState, trapTab } from './dialog';
import { el, icon } from './dom';
import { isolateHistory } from '@codemirror/commands';
import { createEditor, documentText, scrollToEnd, scrollToLine, scrollToTopLine, setFocusMode, setTypewriterMode, topLine, type JumpOptions } from './editor';
import { runFormat, type FormatCommand } from './format';
import { OutlinePanel } from './outline';
import { printOptionsDialog } from './print-dialog';
import { rovingToolbar } from './roving';
import { RecentPanel } from './recent';
import { SearchPanel } from './search';
import { Sidebar } from './sidebar';
import { splitter } from './splitter';
import { titleBar } from './titlebar';
import { showUpdateDialog } from '../../../scripts/updates/client.js';
import { version as appVersion } from '../../package.json';

type ViewMode = 'edit' | 'split' | 'preview';
type ThemeChoice = 'auto' | 'light' | 'dark';
type Measure = 'readable' | 'full';

const APP_NAME = 'ZTG Readout';
const PREVIEW_DELAY_MS = 120;
const OUTLINE_DELAY_MS = 150;
const MAX_HISTORY = 50;
const SIDEBAR_DEFAULT = 270;
const SIDEBAR_MIN = 160;
const EDITOR_FRACTION_DEFAULT = 0.5;
const PANE_MIN = 220;

function stored<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const value = localStorage.getItem(key) as T | null;
    return value && allowed.includes(value) ? value : fallback;
  } catch {
    return fallback;
  }
}
function storedNumber(key: string, fallback: number): number {
  try {
    const value = Number(localStorage.getItem(key));
    return Number.isFinite(value) && value > 0 ? value : fallback;
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
  /**
   * True after the reader converted the line endings and until the next save. The converted text
   * differs from the file on disk even when the editor's text equals savedText, so the document
   * stays unsaved (and a save is possible) whatever is typed or undone afterwards.
   */
  converted: boolean;
}

export class App {
  private view!: EditorView;
  private doc: DocumentState = { ref: null, format: NEW_DOCUMENT_FORMAT, readOnly: null, savedText: '', converted: false };
  private listing: Listing | null = null;
  private history: FileRef[] = [];
  private pendingFragment: string | null = null;
  /** A line to bring into view in the preview once it has rendered (zero-based). */
  private pendingLine: number | null = null;
  private outlineTimer: number | undefined;
  private mode: ViewMode = stored('ztg-readout-mode', ['edit', 'split', 'preview'], 'split');
  private theme: ThemeChoice = stored('ztg-readout-theme', ['auto', 'light', 'dark'], 'auto');
  private measure: Measure = stored('ztg-readout-measure', ['readable', 'full'], 'readable');
  private sidebarWidth = storedNumber('ztg-readout-sidebar-width', SIDEBAR_DEFAULT);
  /** The editor's share of the editor and preview area in split view. */
  private editorFraction = storedNumber('ztg-readout-editor-fraction', EDITOR_FRACTION_DEFAULT);
  private renderTimer: number | undefined;
  private renderGeneration = 0;
  private objectUrls: string[] = [];
  private syncLine = 0;
  /** Focus mode and typewriter mode (RD17): remembered per viewer, off by default. */
  private focusMode = stored('ztg-readout-focus', ['on', 'off'], 'off') === 'on';
  private typewriterMode = stored('ztg-readout-typewriter', ['on', 'off'], 'off') === 'on';
  /** Tells a scroll the app caused from the reader's, so editor and preview never chase each other (RD18). */
  private readonly echo = new EchoGuard();
  private previewScrollQueued = false;
  /** Off until the stored session has been read, so the empty first load cannot overwrite it. */
  private remembering = false;

  private readonly root = el('div', { class: 'app' });
  private readonly titleEl = el('span', { class: 'doc-title' });
  private readonly dirtyEl = el('span', { class: 'dirty-dot', title: 'Unsaved changes', hidden: '' }, el('span', { class: 'sr-only' }, 'Unsaved changes'));
  /** The banner is read out by the announcer below, so it is not a live region itself (a region that starts hidden is often missed). */
  private readonly banner = el('div', { class: 'banner', hidden: '' });
  /** Polite and always present: banners, saves and setting changes are said here, never keystrokes. */
  private readonly announcer = el('div', { class: 'sr-only', role: 'status', 'aria-live': 'polite', 'aria-atomic': 'true' });
  private announceTimer: number | undefined;
  /** Set while a print is being prepared; undoes what preparePrint did. */
  private endPrint: (() => void) | null = null;
  private readonly editorHost = el('div', { class: 'pane editor-pane' });
  private readonly preview = el('article', { class: 'markdown-body', 'aria-label': 'Preview' });
  /** Folded callouts and <details> the reader opened or closed in this document, by position. */
  private readonly folds = new Map<number, boolean>();
  // A scrollable region the keyboard can reach (arrow keys, Page Up and Down) even when the
  // document has no link in it.
  private readonly previewPane = el('div', { class: 'pane preview-pane', role: 'region', 'aria-label': 'Preview of the document', tabindex: '0' }, this.preview);
  private readonly panes = el('div', { class: 'panes' });
  private readonly workspace = el('main', { class: 'workspace' });
  private readonly dropOverlay = el('div', { class: 'drop-overlay', hidden: '', 'aria-hidden': 'true' }, el('div', { class: 'drop-card' }, 'Drop a Markdown file or folder to open it, or an image to add it'));
  private readonly measureButton = el('button', { class: 'icon-button', type: 'button' });
  private readonly status = { cursor: el('span'), words: el('span'), encoding: el('span'), eol: el('span'), remote: el('span', { class: 'status-remote', hidden: '' }) };
  private readonly modeButtons = new Map<ViewMode, HTMLButtonElement>();
  private readonly formatBar = el('div', { class: 'format-bar', role: 'toolbar', 'aria-label': 'Formatting' });
  private readonly themeButton = el('button', { class: 'icon-button', type: 'button' });
  private readonly focusButton = el(
    'button',
    { class: 'icon-button', type: 'button', title: 'Focus mode: dim everything but the current paragraph (F8)', 'aria-label': 'Focus mode', 'aria-pressed': 'false' },
    icon('focus'),
  );
  private readonly typewriterButton = el(
    'button',
    { class: 'icon-button', type: 'button', title: 'Typewriter mode: keep the cursor line in the middle (F9)', 'aria-label': 'Typewriter mode', 'aria-pressed': 'false' },
    icon('typewriter'),
  );
  private readonly copyRichButton = el(
    'button',
    {
      class: 'icon-button',
      type: 'button',
      title: 'Copy as rich text: the selection in the preview, or the whole document (pastes into Outlook, Teams and Word)',
      'aria-label': 'Copy as rich text',
    },
    icon('copy'),
  );
  private readonly backButton = el('button', { class: 'icon-button', type: 'button', title: 'Back (Alt+Left)', 'aria-label': 'Back', disabled: '' }, icon('back'));
  private readonly sidebar = new Sidebar(
    (path) => void this.openFolderPath(path, null, true),
    () => void this.closeFolder(),
  );

  private readonly outline = new OutlinePanel(
    (line) => this.outlineJump(line),
    () => this.closeOutline(),
  );
  private readonly search = new SearchPanel({
    scope: () => this.searchScope(),
    open: (path, match) => void this.openSearchHit(path, match),
    onClose: () => this.closeSearch(),
  });
  private readonly recent = new RecentPanel({
    open: (entry) => void this.openRecent(entry),
    remove: (entry) => void this.removeRecent(entry),
    clear: () => void this.clearRecent(),
    onClose: () => this.closeRecent(),
  });
  /** Entries a failed open found gone this session, for platforms that cannot tell beforehand. */
  private readonly recentMissing = new Set<string>();
  private readonly recentButton = el(
    'button',
    { class: 'text-button', type: 'button', title: 'Recent files (Ctrl+Shift+E)', 'aria-pressed': 'false', 'aria-label': 'Recent files' },
    'Recent',
  );
  private readonly searchButton = el('button', { class: 'text-button', type: 'button', title: 'Search the open folder (Ctrl+Shift+F)', 'aria-pressed': 'false', disabled: '' }, 'Search');
  private readonly outlineButton = el('button', { class: 'text-button', type: 'button', title: 'Outline: the headings of this document', 'aria-pressed': 'false' }, 'Outline');

  private readonly watcher: ExternalChangeWatcher;

  constructor(private readonly platform: Platform) {
    this.watcher = new ExternalChangeWatcher(this.watchHost());
  }

  async start(host: HTMLElement): Promise<void> {
    host.replaceChildren(this.buildChrome());
    this.applyTheme();
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => this.applyTheme());
    this.load(null, '', NEW_DOCUMENT_FORMAT);
    this.platform.onDrop({
      dragging: (active) => {
        this.dropOverlay.hidden = !active;
      },
      open: (arrival) => void this.arrive(arrival, false),
      image: this.platform.saveImage ? (name, source, reason) => void this.dropImage(name, source, reason) : undefined,
      refused: (name, folder) =>
        this.notify(
          folder
            ? `${name} is a folder. In the browser, open folders with Open folder (Ctrl+Shift+O).`
            : `Only Markdown files (.md, .markdown, .txt) and folders open here${name ? `: ${name}` : '.'}`,
        ),
    });
    window.addEventListener('resize', () => this.applySizes());
    window.addEventListener('beforeprint', () => this.preparePrint(rememberedClient(printKey(this.doc.ref))));
    window.addEventListener('afterprint', () => this.endPrint?.());
    // Coming back to the window checks at once instead of waiting for the next poll.
    window.addEventListener('focus', () => void this.watcher.tick());
    document.addEventListener('visibilitychange', () => void this.watcher.tick());
    this.platform.onCloseRequested(() => this.confirmDiscard());
    window.addEventListener('keydown', (event) => this.globalShortcut(event));
    this.preview.addEventListener('click', (event) => void this.previewClick(event));
    this.preview.addEventListener('dblclick', (event) => this.previewDoubleClick(event));
    this.previewPane.addEventListener('scroll', () => this.previewScrolled(), { passive: true });
    // toggle does not bubble, so it is caught on the way down.
    this.preview.addEventListener('toggle', (event) => this.rememberFold(event), true);
    try {
      const initial = await this.platform.initial();
      // A file named on the command line is remembered too; only the empty start is not.
      this.remembering = true;
      if (initial) await this.arrive(initial, true);
    } catch (error) {
      this.notify(`Could not open: ${message(error)}`);
    }
    this.remembering = true;
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
      store('ztg-readout-theme', this.theme);
      this.applyTheme();
      this.announce(this.themeButton.title);
    });
    this.backButton.addEventListener('click', () => void this.back());
    this.focusButton.addEventListener('click', () => this.toggleFocusMode());
    this.typewriterButton.addEventListener('click', () => this.toggleTypewriterMode());
    this.showEditingModes();
    // A click on the button must not clear the selection in the preview that it is about to copy.
    this.copyRichButton.addEventListener('mousedown', (event) => event.preventDefault());
    this.copyRichButton.addEventListener('click', () => void this.copyRichText());
    this.outlineButton.addEventListener('click', () => this.toggleOutline());
    this.searchButton.addEventListener('click', () => this.toggleSearch());
    this.recentButton.addEventListener('click', () => this.toggleRecent());

    const top = el(
      'header',
      { class: 'top-bar' },
      el('span', { class: 'brand', 'aria-hidden': 'true' }, el('span', { class: 'brand-mark' }, 'ZTG'), ' Readout'),
      this.backButton,
      el('div', { class: 'file-name' }, this.titleEl, this.dirtyEl),
      el(
        'div',
        { class: 'actions' },
        button('New', 'New document (Ctrl+N)', () => void this.newDocument(), 'new'),
        button('Open', 'Open file (Ctrl+O)', () => void this.open(), 'open'),
        button('Open folder', 'Open folder or repository (Ctrl+Shift+O)', () => void this.openFolder(), 'open-folder'),
        ...(this.platform.recent ? [this.recentButton] : []),
        button('Save', 'Save (Ctrl+S)', () => void this.save(false), 'save'),
        button('Save as', 'Save as (Ctrl+Shift+S)', () => void this.save(true), 'save-as'),
        el('span', { class: 'divider' }),
        modes,
        el('span', { class: 'divider' }),
        this.outlineButton,
        this.searchButton,
        this.copyRichButton,
        button('Export', 'Export as HTML', () => void this.exportHtml(), 'export'),
        button('Print', 'Print or save as PDF (Ctrl+P)', () => void this.print(), 'print'),
        this.focusButton,
        this.typewriterButton,
        this.measureButton,
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
    const statusBar = el('footer', { class: 'status-bar', 'aria-label': 'Document status' }, s.cursor, s.words, s.remote, el('span', { class: 'spacer' }),
      button('Check for updates', 'Check for updates', () => showUpdateDialog({
        tool: 'readout', current: appVersion, openExternal: url => this.platform.openExternal(url),
      })), s.encoding, s.eol);
    this.measureButton.addEventListener('click', () => {
      this.measure = this.measure === 'readable' ? 'full' : 'readable';
      store('ztg-readout-measure', this.measure);
      this.applyMeasure();
      this.announce(this.measureButton.title);
    });
    this.applyMeasure();

    // Dividers: one beside the folder sidebar, one between editor and preview (split view).
    const sidebarSplitter = splitter({
      label: 'Resize the file list',
      current: () => this.sidebar.element.getBoundingClientRect().width,
      apply: (px) => this.setSidebarWidth(px),
      commit: () => store('ztg-readout-sidebar-width', String(Math.round(this.sidebarWidth))),
      reset: () => this.setSidebarWidth(SIDEBAR_DEFAULT),
      range: () => ({ min: SIDEBAR_MIN, max: this.sidebarMax() }),
    });
    sidebarSplitter.classList.add('splitter-sidebar');
    const paneSplitter = splitter({
      label: 'Resize the editor and preview',
      current: () => this.editorHost.getBoundingClientRect().width,
      apply: (px) => this.setEditorWidth(px),
      commit: () => store('ztg-readout-editor-fraction', this.editorFraction.toFixed(4)),
      reset: () => this.setEditorWidth(this.panes.getBoundingClientRect().width * EDITOR_FRACTION_DEFAULT),
      range: () => ({ min: PANE_MIN, max: Math.max(PANE_MIN, this.panes.getBoundingClientRect().width - PANE_MIN) }),
    });
    paneSplitter.classList.add('splitter-panes');
    this.panes.append(this.editorHost, paneSplitter, this.previewPane);
    this.workspace.append(this.sidebar.element, sidebarSplitter, this.outline.element, this.search.element, this.recent.element, this.panes, this.dropOverlay);
    if (this.platform.window) {
      this.root.classList.add('has-titlebar');
      this.root.append(titleBar(this.platform.window, APP_NAME));
    }
    this.root.append(top, this.formatBar, this.banner, this.workspace, statusBar, this.announcer);
    new MutationObserver(() => {
      if (!this.banner.hidden) this.announce(this.banner.textContent ?? '');
    }).observe(this.banner, { childList: true, characterData: true, subtree: true, attributes: true, attributeFilter: ['hidden'] });
    rovingToolbar(this.formatBar);
    this.applySizes();
    return this.root;
  }

  // ---- pane sizes -----------------------------------------------------------------------

  private sidebarMax(): number {
    const available = this.workspace.getBoundingClientRect().width || window.innerWidth;
    return Math.max(SIDEBAR_MIN, Math.min(available * 0.6, available - 2 * PANE_MIN));
  }

  /** Clamps and applies the sidebar width; keeps room for the panes beside it. */
  private setSidebarWidth(px: number): number {
    const max = this.sidebarMax();
    this.sidebarWidth = Math.min(Math.max(px, SIDEBAR_MIN), max);
    this.root.style.setProperty('--sidebar-width', `${Math.round(this.sidebarWidth)}px`);
    return this.sidebarWidth;
  }

  /** Clamps and applies the editor width in split view, stored as a share of the area. */
  private setEditorWidth(px: number): number {
    const available = this.panes.getBoundingClientRect().width;
    if (available <= 0) return px;
    const clamped = Math.min(Math.max(px, PANE_MIN), Math.max(PANE_MIN, available - PANE_MIN));
    this.editorFraction = clamped / available;
    this.root.style.setProperty('--editor-fraction', this.editorFraction.toFixed(4));
    return clamped;
  }

  private applySizes(): void {
    this.setSidebarWidth(this.sidebarWidth);
    const fraction = Math.min(Math.max(this.editorFraction, 0.1), 0.9);
    this.root.style.setProperty('--editor-fraction', fraction.toFixed(4));
  }

  private applyMeasure(): void {
    this.root.dataset.measure = this.measure;
    this.measureButton.replaceChildren(icon(this.measure === 'readable' ? 'width-readable' : 'width-full'));
    this.measureButton.title =
      this.measure === 'readable' ? 'Reading width: comfortable column (click for full width)' : 'Reading width: full pane (click for a comfortable column)';
    this.measureButton.setAttribute('aria-label', this.measureButton.title);
  }

  private applyTheme(): void {
    const dark = this.theme === 'dark' || (this.theme === 'auto' && window.matchMedia('(prefers-color-scheme: dark)').matches);
    document.documentElement.dataset.theme = dark ? 'dark' : 'light';
    this.themeButton.replaceChildren(icon(this.theme === 'auto' ? 'theme-auto' : dark ? 'theme-dark' : 'theme-light'));
    this.themeButton.title = `Theme: ${this.theme} (click to change)`;
    this.themeButton.setAttribute('aria-label', this.themeButton.title);
    if (this.view) this.schedulePreview(0);
  }

  /** Focus and typewriter mode (RD17): remembered, announced politely, and applied to the open editor. */
  private toggleFocusMode(): void {
    this.focusMode = !this.focusMode;
    store('ztg-readout-focus', this.focusMode ? 'on' : 'off');
    setFocusMode(this.view, this.focusMode);
    this.showEditingModes();
    this.announce(`Focus mode ${this.focusMode ? 'on' : 'off'}`);
  }

  private toggleTypewriterMode(): void {
    this.typewriterMode = !this.typewriterMode;
    store('ztg-readout-typewriter', this.typewriterMode ? 'on' : 'off');
    setTypewriterMode(this.view, this.typewriterMode);
    this.showEditingModes();
    this.announce(`Typewriter mode ${this.typewriterMode ? 'on' : 'off'}`);
  }

  private showEditingModes(): void {
    this.focusButton.setAttribute('aria-pressed', String(this.focusMode));
    this.typewriterButton.setAttribute('aria-pressed', String(this.typewriterMode));
  }

  private get dark(): boolean {
    return document.documentElement.dataset.theme === 'dark';
  }

  private setMode(mode: ViewMode): void {
    this.mode = mode;
    store('ztg-readout-mode', mode);
    this.root.dataset.mode = mode;
    for (const [m, b] of this.modeButtons) b.setAttribute('aria-pressed', String(m === mode));
    this.formatBar.hidden = mode === 'preview' || this.doc.readOnly !== null;
    if (mode !== 'edit') this.schedulePreview(0);
    if (mode !== 'preview') this.view?.requestMeasure();
  }

  /** Says text politely to a screen reader. The region is cleared first so the same text can be said twice. */
  private announce(text: string): void {
    window.clearTimeout(this.announceTimer);
    this.announcer.textContent = '';
    if (text === '') return;
    this.announceTimer = window.setTimeout(() => (this.announcer.textContent = text), 60);
  }

  private notify(text: string | null): void {
    this.banner.hidden = text === null;
    this.banner.textContent = text ?? '';
  }

  /** A browser session waits for a click, which is what lets the page ask for access again. */
  private offerSession(pending: PendingSession): void {
    const reopen = el('button', { type: 'button', class: 'text-button banner-action' }, 'Reopen');
    reopen.addEventListener('click', () => {
      this.notify(null);
      void pending.open().then(
        (arrival) => (arrival ? this.arrive(arrival, false) : this.notify(`${pending.name} could not be reopened.`)),
        (error: unknown) => this.notify(`Could not reopen ${pending.name}: ${message(error)}`),
      );
    });
    this.banner.replaceChildren(`Last time you had ${pending.name} open. `, reopen);
    this.banner.hidden = false;
  }

  /** Stores what is open for the next start: the folder, and the document when it is in it. */
  private rememberSession(): void {
    if (!this.remembering) return;
    const ref = this.doc.ref;
    this.platform.remember(this.listing, this.listing === null || ref?.folderPath !== undefined ? ref : null);
  }

  // ---- documents ------------------------------------------------------------------------

  private load(ref: FileRef | null, text: string, format: DocumentFormat, bytes: Uint8Array | null = null): void {
    const readOnly = readOnlyReason(format);
    this.doc = { ref, format, readOnly, savedText: text, converted: false };
    this.mountEditor(text, format, readOnly !== null);
    // The editor's own text is the reference for "unsaved": a mixed file is shown with LF
    // between every line, which is not the raw text it was decoded from.
    this.doc.savedText = documentText(this.view.state);
    this.showReadOnly();
    this.syncLine = 0;
    this.refreshOutline();
    this.folds.clear();
    this.previewPane.scrollTo({ top: 0 });
    this.status.cursor.textContent = 'Ln 1, Col 1';
    this.updateFormatStatus();
    this.sidebar.select(ref?.folderPath);
    if (ref) this.announce(`Opened ${ref.name}`);
    this.updateWords();
    this.updateDirty();
    this.setMode(this.mode);
    this.schedulePreview(0);
    this.rememberSession();
    this.watcher.rebase(bytes);
    this.watcher.sync();
  }

  /** Replaces the editor with one holding text. The caller sets this.doc first. */
  private mountEditor(text: string, format: DocumentFormat, readOnly: boolean): void {
    this.view?.destroy();
    this.view = createEditor({
      parent: this.editorHost,
      text,
      format,
      readOnly,
      onChange: () => {
        this.updateDirty();
        this.schedulePreview(PREVIEW_DELAY_MS);
        this.updateWords();
        this.scheduleOutline();
      },
      onCursor: (line, column) => {
        this.status.cursor.textContent = `Ln ${line}, Col ${column}`;
        this.outline.setCursorLine(line - 1);
      },
      onScroll: (line) => this.editorScrolled(line),
      focusMode: this.focusMode,
      typewriterMode: this.typewriterMode,
      onPasteImage: (file) => void this.pasteImage(file),
    });
  }

  // ---- images (RD13) --------------------------------------------------------------------

  private static readonly SAVE_FIRST =
    'Save the document first (Ctrl+S), then paste or drop the image. Images are saved in an images folder beside the document. Nothing was written.';

  /** A pasted image: checked here for the message, and again by whoever writes it. */
  private async pasteImage(file: File): Promise<void> {
    if (!this.platform.saveImage) return this.notify('Images cannot be saved here.');
    if (this.doc.readOnly !== null) return this.notify('This document is read-only, so no image was added.');
    if (!this.doc.ref) return this.notify(App.SAVE_FIRST);
    const kind = imageKindFromMime(file.type);
    if (!kind) return this.notify(IMAGE_REFUSED);
    if (file.size > MAX_IMAGE_BYTES) return this.notify(`The image is larger than ${MAX_IMAGE_BYTES / (1024 * 1024)} MB.`);
    const bytes = new Uint8Array(await file.arrayBuffer());
    const problem = imageProblem(bytes, kind);
    if (problem) return this.notify(problem);
    await this.insertImage({ kind: 'bytes', bytes, type: file.type, name: undefined });
  }

  /** An image file dropped on the window. Markdown and folders never come here. */
  private async dropImage(name: string, source: ImageSource | null, reason?: string): Promise<void> {
    const pristine = !this.doc.ref && documentText(this.view.state) === '' && !this.dirty;
    // With nothing open the drop is refused as any other file is.
    if (pristine) return this.notify(`Only Markdown files (.md, .markdown, .txt) and folders open here: ${name}`);
    if (!this.doc.ref) return this.notify(App.SAVE_FIRST);
    if (this.doc.readOnly !== null) return this.notify('This document is read-only, so no image was added.');
    if (!source) return this.notify(reason ?? `${name} was not added. ${IMAGE_REFUSED}`);
    await this.insertImage(source);
  }

  /** Saves the image beside the document, then writes the link at the cursor as one undo step. */
  private async insertImage(source: ImageSource): Promise<void> {
    const { ref } = this.doc;
    const view = this.view;
    if (!ref || !this.platform.saveImage) return;
    try {
      const saved = await this.platform.saveImage(ref, this.listing, source);
      if (view !== this.view) {
        // Another document was loaded while the file was written.
        return this.notify(`Saved ${saved.link}, but the document changed, so the link was not inserted.`);
      }
      const text = imageMarkdown(saved.link);
      const { from, to } = view.state.selection.main;
      view.dispatch({
        changes: { from, to, insert: text },
        selection: { anchor: from + text.length },
        scrollIntoView: true,
        userEvent: 'input.paste',
        annotations: isolateHistory.of('full'),
      });
      view.focus();
      this.notify(`Saved ${saved.link} beside the document. Undo removes the link only; the image file stays.`);
    } catch (error) {
      this.notify(`No image was added: ${message(error)}`);
    }
  }

  private updateFormatStatus(): void {
    const format = this.doc.format;
    this.status.encoding.textContent = format.validUtf8 ? (format.bom ? 'UTF-8 with BOM' : 'UTF-8') : 'Not UTF-8';
    this.status.encoding.title = 'Saved exactly as opened';
    const eol = format.mixedEndings ? 'Mixed line endings' : format.eol === '\r\n' ? 'CRLF' : 'LF';
    this.status.eol.textContent = this.doc.converted ? `${eol} (converted, not saved)` : eol;
  }

  // ---- line endings (the rules live in core/document.ts) -----------------------------------

  /**
   * The banner for a read-only document. A file that is read-only only because its line endings
   * are mixed offers the conversion; one that is not valid UTF-8 never does.
   */
  private showReadOnly(): void {
    const reason = this.doc.readOnly;
    if (reason === null || !canConvertLineEndings(this.doc.format)) {
      this.notify(reason);
      return;
    }
    const convert = el('button', { type: 'button', class: 'text-button banner-action' }, 'Convert line endings');
    convert.addEventListener('click', () => void this.convertLineEndings());
    this.banner.replaceChildren(`${reason} `, convert);
    this.banner.hidden = false;
  }

  /**
   * Asks which ending to use (the file's majority ending first) and converts the open document.
   * The edit is in memory only: the document becomes editable and unsaved, and the file on disk
   * is not touched until the reader saves.
   */
  private async convertLineEndings(): Promise<void> {
    const { format } = this.doc;
    if (!this.doc.readOnly || !canConvertLineEndings(format)) return;
    const counts = format.endings;
    const parts = counts
      ? ([
          [counts.crlf, 'CRLF'],
          [counts.lf, 'LF'],
          [counts.cr, 'CR'],
        ] as const)
          .filter(([n]) => n > 0)
          .map(([n, name]) => `${n.toLocaleString()} ${name}`)
          .join(', ')
      : '';
    const first: LineEnding = counts ? majorityEnding(counts) : '\n';
    const names: Record<LineEnding, string> = { '\n': 'LF', '\r\n': 'CRLF' };
    const other: LineEnding = first === '\n' ? '\r\n' : '\n';
    // Say "most lines" only when one of the two is really the larger; a tie has no majority.
    const clear = counts !== undefined && counts.crlf !== counts.lf;
    const label = (eol: LineEnding) => `Convert to ${names[eol]}${clear && eol === first ? ' (most lines)' : ''}`;
    const before = this.doc;
    const choice = await confirmDialog(
      'Convert line endings?',
      `${this.doc.ref?.name ?? 'This file'} mixes line endings${parts ? ` (${parts})` : ''}. Every line ending will become one ending, and the document becomes editable. Nothing is written until you save.`,
      [label(first), label(other), 'Cancel'],
    );
    // The reader may have opened another file, or the file may have reloaded, behind the dialog.
    if (choice === null || choice === 'Cancel' || this.doc !== before) return;
    this.applyConversion(choice === label(first) ? first : other);
  }

  private applyConversion(target: LineEnding): void {
    if (!this.doc.readOnly || !canConvertLineEndings(this.doc.format)) return;
    // The editor holds the lines joined by LF (a mixed file is split on every ending to show it).
    const converted = convertDocument(documentText(this.view.state), this.doc.format, target);
    const selection = this.view.state.selection;
    const scrollTop = this.view.scrollDOM.scrollTop;
    this.doc = { ...this.doc, format: converted.format, readOnly: null, savedText: converted.text, converted: true };
    this.mountEditor(converted.text, converted.format, false);
    // Same lines, same offsets: the cursor and the scroll position carry over.
    this.view.dispatch({ selection });
    this.view.scrollDOM.scrollTop = scrollTop;
    this.updateFormatStatus();
    this.setMode(this.mode);
    this.updateDirty();
    this.updateWords();
    this.refreshOutline();
    this.notify(
      `Line endings converted to ${target === '\r\n' ? 'CRLF' : 'LF'}. The file on disk is unchanged until you save.`,
    );
    this.view.focus();
  }

  // ---- external changes (the decisions live in core/reload.ts) ----------------------------

  private watchHost(): WatchHost {
    const read = async (get: () => Promise<OpenedFile | null>) => {
      try {
        return (await get())?.bytes ?? null;
      } catch {
        return null;
      }
    };
    return {
      active: () => this.listing !== null || this.canReread(),
      paused: () => document.hidden || dialogState.open > 0,
      currentPath: () => (this.listing ? (this.doc.ref?.folderPath ?? null) : null),
      hasFile: () => this.canReread(),
      isDirty: () => this.dirty,
      folderFiles: () => this.listing?.files ?? null,
      listingTruncated: () => this.listing?.truncated ?? false,
      relist: async () => {
        const listing = this.listing;
        if (!listing || !this.platform.relist) return null;
        try {
          return await this.platform.relist(listing);
        } catch {
          return null;
        }
      },
      readCurrent: () => read(async () => this.reread(this.doc.ref)),
      readFolderFile: (path) => read(async () => (this.listing ? this.platform.readFolderFile(this.listing, path) : null)),
      applyListing: (next) => {
        if (!this.listing) return;
        this.listing = { ...this.listing, files: next.files, truncated: next.truncated };
        this.sidebar.update(this.listing);
      },
      reload: (bytes) => {
        const decoded = decodeDocument(bytes);
        const top = this.previewPane.scrollTop;
        this.load(this.doc.ref, decoded.text, decoded.format, bytes);
        this.previewPane.scrollTop = top;
        if (this.doc.readOnly) this.showReadOnly();
        else this.notify(`${this.doc.ref?.name ?? 'The file'} changed on disk and was reloaded.`);
      },
      resolveConflict: async (bytes) => {
        void bytes;
        const choice = await confirmDialog(
          `${this.doc.ref?.name ?? 'This file'} changed on disk`,
          'It was changed outside Readout, and you have unsaved edits. Neither version is overwritten until you choose. Loading the disk version discards your edits.',
          ['Keep my version', 'Load disk version', 'Decide later'],
        );
        return choice === 'Keep my version' ? 'mine' : choice === 'Load disk version' ? 'disk' : null;
      },
      conflictDeferred: () => {
        const review = el('button', { type: 'button', class: 'text-button banner-action' }, 'Review');
        review.addEventListener('click', () => this.watcher.review());
        this.banner.replaceChildren(`${this.doc.ref?.name ?? 'The file'} changed on disk. Your unsaved edits are kept. `, review);
        this.banner.hidden = false;
      },
      renamed: async (path, stillCurrent) => {
        const listing = this.listing;
        const before = this.doc;
        if (!listing) return;
        try {
          const moved = await this.platform.readFolderFile(listing, path);
          // The reader may have opened something else while the file was read.
          if (!stillCurrent() || this.doc !== before || this.listing !== listing) return;
          this.doc = { ...this.doc, ref: moved.ref };
          this.sidebar.select(moved.ref.folderPath);
          this.updateDirty();
          this.rememberSession();
          this.notify(`The file was renamed to ${path} on disk. It stays open here.`);
        } catch {
          // A failed read is still stale if the reader has since opened another document.
          if (!stillCurrent() || this.doc !== before || this.listing !== listing) return;
          this.notify(`The file was renamed to ${path} on disk, but it could not be followed.`);
        }
      },
      gone: () => this.notify(`${this.doc.ref?.name ?? 'The file'} was deleted or moved on disk. It stays open here; Save writes it back.`),
    };
  }

  /** True when the open document can be read again from where it came from. */
  private canReread(): boolean {
    const ref = this.doc.ref;
    if (!ref) return false;
    return ref.folderPath !== undefined ? this.listing !== null : ref.path !== undefined || ref.handle !== undefined;
  }

  private reread(ref: FileRef | null): Promise<OpenedFile | null> {
    if (!ref) return Promise.resolve(null);
    if (ref.folderPath !== undefined && this.listing) return this.platform.readFolderFile(this.listing, ref.folderPath);
    return this.platform.reopen(ref);
  }

  private get dirty(): boolean {
    return this.doc.converted || documentText(this.view.state) !== this.doc.savedText;
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
    this.pendingLine = null;
    this.pendingFragment = fragment;
    const decoded = decodeDocument(file.bytes);
    this.load(file.ref, decoded.text, decoded.format, file.bytes);
    this.noteRecent(file.ref);
  }

  private async arrive(arrival: Arrival, startup: boolean): Promise<void> {
    if (arrival.pending) {
      this.offerSession(arrival.pending);
      return;
    }
    if (!startup && !(await this.confirmDiscard())) return;
    if (arrival.listing) await this.showFolder(arrival.listing, arrival.file);
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
    // A search belongs to one folder: a new or closed folder cancels it and starts from nothing.
    this.search.reset();
    if (listing === null) this.closeSearch();
    this.searchButton.disabled = listing === null;
    this.root.classList.toggle('has-folder', listing !== null);
    this.watcher.sync();
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

  /**
   * A folder opens for reading: its README in the preview, the tree beside it. A restored
   * session passes the document that was open in it instead, shown in the view it was left in.
   */
  private async showFolder(listing: Listing, file?: OpenedFile): Promise<void> {
    this.setListing(listing);
    if (file) this.show(file, null, false);
    else {
      const first = defaultDocument(listing.files);
      if (!first) {
        this.load(null, '', NEW_DOCUMENT_FORMAT);
        this.notify(`No Markdown files were found in ${listing.folder.name}.`);
        return;
      }
      if (this.mode === 'edit') this.setMode('preview');
      await this.openFolderPath(first, null, false, true);
    }
    if (listing.truncated) this.notify('This folder is very large; the file list shows the first 20,000 Markdown files.');
  }

  private async closeFolder(): Promise<void> {
    if (!(await this.confirmDiscard())) return;
    this.setListing(null);
    this.load(null, '', NEW_DOCUMENT_FORMAT);
  }

  /** Resolves true when folderPath is the open document afterwards (false: declined or unreadable). */
  private async openFolderPath(folderPath: string, fragment: string | null, remember: boolean, confirmed = false): Promise<boolean> {
    if (!this.listing) return false;
    if (folderPath === this.doc.ref?.folderPath) {
      this.scrollToFragment(fragment);
      return true;
    }
    if (!confirmed && !(await this.confirmDiscard())) return false;
    try {
      this.show(await this.platform.readFolderFile(this.listing, folderPath), fragment, remember);
      return true;
    } catch (error) {
      this.notify(`Could not open ${folderPath}: ${message(error)}`);
      return false;
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

  /** A save never replaces a disk version it has not seen: when the file changed, ask first. */
  private async confirmOverwrite(): Promise<boolean> {
    if (!this.canReread()) return true;
    let disk: Uint8Array | null = null;
    try {
      disk = (await this.reread(this.doc.ref))?.bytes ?? null;
    } catch {
      return true;
    }
    if (!disk || this.watcher.matchesBaseline(disk)) return true;
    const choice = await confirmDialog(
      `${this.doc.ref?.name ?? 'This file'} changed on disk`,
      'Saving replaces the version on disk, which was changed outside Readout after you opened it.',
      ['Cancel', 'Overwrite'],
    );
    return choice === 'Overwrite';
  }

  /** Resolves true when the document was written. */
  private async save(as: boolean): Promise<boolean> {
    if (this.doc.readOnly) {
      this.showReadOnly();
      return false;
    }
    const text = documentText(this.view.state);
    if (!as && !(await this.confirmOverwrite())) return false;
    try {
      const bytes = encodeDocument(text, this.doc.format);
      const name = this.doc.ref?.name ?? 'Untitled.md';
      const ref = as || !this.doc.ref ? await this.platform.saveAs(bytes, name) : await this.platform.save(this.doc.ref, bytes, name);
      if (!ref) return false;
      const previous = this.doc.ref;
      // Saving somewhere else makes the file independent of the open folder.
      const kept = ref === this.doc.ref ? ref : { ...ref, folderPath: undefined };
      this.doc = { ...this.doc, ref: kept, savedText: text, converted: false };
      this.updateFormatStatus();
      this.watcher.rebase(bytes);
      this.watcher.sync();
      this.sidebar.select(kept.folderPath);
      this.updateDirty();
      this.notify(null);
      this.announce(`Saved ${kept.name}`);
      this.rememberSession();
      // A file saved under a new name or place is a document the reader will want again.
      if (ref !== previous) this.noteRecent(kept);
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

  /**
   * Copy as rich text (core/richtext.ts): the selection in the preview when there is one, else the
   * whole document. The clipboard gets text/html and text/plain together. The preview is only
   * read; nothing is written to the document or to disk.
   */
  private async copyRichText(): Promise<void> {
    let fragment: DocumentFragment | null = null;
    const selection = window.getSelection();
    // The selection is captured before anything is awaited: a render replaces the preview.
    if (selection && selection.rangeCount > 0 && !selection.isCollapsed) {
      const range = selection.getRangeAt(0);
      if (this.preview.contains(range.commonAncestorContainer)) fragment = fragmentForRange(range, this.preview);
    }
    const whole = fragment === null;
    try {
      if (!fragment) {
        // The preview is not kept current in edit mode, so it is rendered first.
        window.clearTimeout(this.renderTimer);
        await this.renderPreview();
        const range = document.createRange();
        range.selectNodeContents(this.preview);
        fragment = range.cloneContents();
      }
      if (isEmptyFragment(fragment)) {
        this.notify('There is nothing to copy.');
        return;
      }
      const { html, text } = await buildRichText(fragment, { resolveImage: blobUrlToDataUrl });
      await this.platform.copyRichText(html, text);
      this.notify(
        whole
          ? 'Copied the whole document as rich text. Select part of the preview first to copy only that part.'
          : 'Copied the selection as rich text.',
      );
    } catch (error) {
      this.notify(`Could not copy: ${message(error)}`);
    }
  }

  /**
   * Print (Ctrl+P, RD12): the preview is not kept current in edit mode, so it is rendered first;
   * then the reader may give a client name for the page header. Cancelling prints nothing.
   */
  private async print(): Promise<void> {
    window.clearTimeout(this.renderTimer);
    await this.renderPreview();
    const key = printKey(this.doc.ref);
    const client = await printOptionsDialog({
      client: rememberedClient(key),
      remembered: key !== null,
      onClear: () => forgetClient(key),
    });
    if (client === null) return;
    rememberClient(key, client);
    this.preparePrint(client);
    window.print();
  }

  /**
   * Fills the header and footer text and opens folded callouts and <details> for printing; the
   * reader's folds and the template return after printing. Also runs on the browser's own print
   * command (the beforeprint event), with the name remembered for the document.
   */
  private preparePrint(client: string): void {
    if (this.endPrint) return;
    const root = document.documentElement;
    setPrintTemplate(root, {
      title: printTitle(this.preview, this.doc.ref?.name ?? 'Untitled.md'),
      client,
      date: printDate(new Date()),
    });
    const closed = [...this.preview.querySelectorAll('details')].filter((d) => !d.open);
    closed.forEach((d) => (d.open = true));
    this.endPrint = () => {
      closed.forEach((d) => (d.open = false));
      clearPrintTemplate(root);
      this.endPrint = null;
    };
  }

  private globalShortcut(event: KeyboardEvent): void {
    if (event.altKey && event.key === 'ArrowLeft' && !this.backButton.disabled) {
      event.preventDefault();
      void this.back();
      return;
    }
    // F8 and F9 (as in Typora): no browser or WebView binds them, and no editor command does.
    if ((event.key === 'F8' || event.key === 'F9') && !event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey) {
      event.preventDefault();
      if (event.key === 'F8') this.toggleFocusMode();
      else this.toggleTypewriterMode();
      return;
    }
    if (!(event.ctrlKey || event.metaKey) || event.altKey) return;
    const key = event.key.toLowerCase();
    if (key === 'f' && event.shiftKey) {
      event.preventDefault();
      this.openSearch();
      return;
    }
    if (key === 'e' && event.shiftKey && this.platform.recent) {
      event.preventDefault();
      this.openRecentPanel(true);
      return;
    }
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
    this.showRemoteImages();
    // The new content can move the preview's scroll position (it may be shorter); that is not the reader scrolling.
    this.echo.expect('preview', this.previewPane.scrollTop);
    const details = this.preview.querySelectorAll('details');
    for (const [index, open] of this.folds) if (details[index]) details[index].open = open;
    if (this.pendingLine !== null) {
      this.scrollPreviewToLine(this.pendingLine);
      this.pendingLine = null;
      this.pendingFragment = null;
    } else if (this.pendingFragment !== null) {
      this.scrollToFragment(this.pendingFragment);
      this.pendingFragment = null;
    } else this.syncPreview(this.syncLine);
  }

  /** One note in the status bar when the preview holds remote images; none is ever fetched (RD14). */
  private showRemoteImages(): void {
    const note = describeRemoteImages(summarizeRemoteImages(this.preview));
    this.status.remote.hidden = note === null;
    this.status.remote.textContent = note?.text ?? '';
    this.status.remote.title = note?.title ?? '';
  }

  // ---- outline ----------------------------------------------------------------------------

  private toggleOutline(): void {
    if (this.outline.hidden) this.openOutline();
    else this.closeOutline();
  }

  private openOutline(): void {
    this.closeSearch();
    this.closeRecent();
    this.outline.show(true);
    this.outlineButton.setAttribute('aria-pressed', 'true');
    this.refreshOutline();
    this.outline.setCursorLine(this.view.state.doc.lineAt(this.view.state.selection.main.head).number - 1);
  }

  private closeOutline(): void {
    const hadFocus = this.outline.element.contains(document.activeElement);
    this.outline.show(false);
    this.outlineButton.setAttribute('aria-pressed', 'false');
    if (hadFocus) this.view.focus();
  }

  // ---- recent files -------------------------------------------------------------------------

  /** Records a document the reader opened or saved. Best effort; the platform decides what it can keep. */
  private noteRecent(ref: FileRef | null): void {
    const recent = this.platform.recent;
    if (!recent || !ref) return;
    void recent.add(ref).then(() => (this.recent.hidden ? undefined : this.refreshRecent()));
  }

  private toggleRecent(): void {
    if (this.recent.hidden) this.openRecentPanel(false);
    else this.closeRecent();
  }

  /** Shows the panel; from the keyboard it takes focus so the list can be used at once. */
  private openRecentPanel(focus: boolean): void {
    if (!this.platform.recent) return;
    this.closeOutline();
    this.closeSearch();
    this.recent.show(true);
    this.recentButton.setAttribute('aria-pressed', 'true');
    void this.refreshRecent().then(() => {
      if (focus) this.recent.focus();
    });
  }

  private closeRecent(): void {
    const hadFocus = this.recent.element.contains(document.activeElement);
    this.recent.show(false);
    this.recentButton.setAttribute('aria-pressed', 'false');
    if (hadFocus) this.view.focus();
  }

  private async refreshRecent(): Promise<void> {
    const recent = this.platform.recent;
    if (!recent) return;
    const entries = await recent.list();
    for (const id of this.recentMissing) if (!entries.some((e) => e.id === id)) this.recentMissing.delete(id);
    this.recent.update(entries.map((e): RecentEntry => (this.recentMissing.has(e.id) ? { ...e, missing: true } : e)));
  }

  /** Opens an entry the way Open does: unsaved edits are asked about first, then the folder gives way. */
  private async openRecent(entry: RecentEntry): Promise<void> {
    const recent = this.platform.recent;
    if (!recent) return;
    if (!(await this.confirmDiscard())) return;
    try {
      let file = await recent.open(entry.id, this.listing);
      if (!file) {
        this.recentMissing.add(entry.id);
        this.notify(`${entry.name} no longer exists. Remove it from the recent files if you do not need it.`);
        await this.refreshRecent();
        return;
      }
      this.recentMissing.delete(entry.id);
      const listing = this.listing;
      if (listing && file.ref.folderPath !== undefined && listing.files.includes(file.ref.folderPath)) {
        // A file of the open folder stays in it, with the file list beside it.
        this.show(file, null, true);
      } else {
        file = { ...file, ref: { ...file.ref, folderPath: undefined } };
        this.setListing(null);
        this.show(file, null, false);
      }
      this.notify(null);
      this.closeRecent();
      this.view.focus();
    } catch (error) {
      this.notify(`Could not open ${entry.name}: ${message(error)}`);
    }
  }

  private async removeRecent(entry: RecentEntry): Promise<void> {
    await this.platform.recent?.remove(entry.id);
    this.recentMissing.delete(entry.id);
    await this.refreshRecent();
  }

  private async clearRecent(): Promise<void> {
    await this.platform.recent?.clear();
    this.recentMissing.clear();
    await this.refreshRecent();
  }

  // ---- folder search ----------------------------------------------------------------------

  private toggleSearch(): void {
    if (this.search.hidden) this.openSearch();
    else this.closeSearch();
  }

  private openSearch(): void {
    if (!this.listing) {
      this.notify('Open a folder (Ctrl+Shift+O) to search it.');
      return;
    }
    this.closeOutline();
    this.closeRecent();
    this.search.show(true);
    this.searchButton.setAttribute('aria-pressed', 'true');
  }

  private closeSearch(): void {
    const hadFocus = this.search.element.contains(document.activeElement);
    this.search.show(false);
    this.searchButton.setAttribute('aria-pressed', 'false');
    if (hadFocus) this.view.focus();
  }

  /**
   * What a search may read: the files of the open listing, through the platform's folder reader.
   * The open file is searched as it is in the editor, unsaved edits included, so a hit's line is
   * the line the jump lands on.
   */
  private searchScope(): { files: readonly string[]; read: (path: string, maxBytes?: number) => Promise<Uint8Array | string | null> } | null {
    const listing = this.listing;
    if (!listing) return null;
    return {
      files: listing.files,
      read: async (path, maxBytes = DEFAULT_BOUNDS.maxFileBytes) => {
        // A refresh swaps the listing object but keeps the folder, so path alone decides.
        if (path === this.doc.ref?.folderPath) return documentText(this.view.state);
        return (await this.platform.readFolderFile(listing, path, { maxBytes })).bytes;
      },
    };
  }

  /** Opens a result's file the way the sidebar does (asking about unsaved edits), then its line. */
  private async openSearchHit(path: string, match: SearchMatch): Promise<void> {
    const already = path === this.doc.ref?.folderPath;
    if (!(await this.openFolderPath(path, null, true))) return;
    if (this.doc.ref?.folderPath !== path) return;
    // On a narrow window the panel covers the document, so it gives way to the result.
    if (window.matchMedia('(max-width: 720px)').matches) this.closeSearch();
    // A freshly opened file in preview view renders a moment later; the line waits for it.
    if (this.mode === 'preview' && !already) this.pendingLine = match.line;
    else this.jumpToLine(match.line, { column: match.column, length: match.length });
  }

  /** On a narrow window the panel covers the document, so it closes to show where the jump landed. */
  private outlineJump(line: number): void {
    const narrow = window.matchMedia('(max-width: 720px)').matches;
    if (narrow) this.closeOutline();
    this.jumpToLine(line, { focus: narrow });
  }

  private scheduleOutline(): void {
    if (this.outline.hidden) return;
    window.clearTimeout(this.outlineTimer);
    this.outlineTimer = window.setTimeout(() => this.refreshOutline(), OUTLINE_DELAY_MS);
  }

  private refreshOutline(): void {
    window.clearTimeout(this.outlineTimer);
    if (this.outline.hidden) return;
    this.outline.update(extractOutline(documentText(this.view.state)));
  }

  /**
   * Brings a zero-based source line into view: the editor selects it in edit and split view (the
   * preview follows the editor), and the preview scrolls to its block in preview-only view.
   */
  private jumpToLine(line: number, options: JumpOptions = {}): void {
    if (this.mode === 'preview') this.scrollPreviewToLine(line);
    else scrollToLine(this.view, line, options);
  }

  private scrollPreviewToLine(line: number): void {
    let target: HTMLElement | null = null;
    for (const node of this.preview.querySelectorAll<HTMLElement>('[data-line]')) {
      if (Number(node.dataset.line) > line) break;
      target = node;
    }
    target?.scrollIntoView({ block: 'start' });
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
    this.echo.expect('preview', this.previewPane.scrollTop);
  }

  /** The editor scrolled: the preview follows, unless the scroll was this app moving the editor for the preview. */
  private editorScrolled(line: number): void {
    if (this.echo.isEcho('editor', this.view.scrollDOM.scrollTop)) {
      this.syncLine = line;
      return;
    }
    this.syncPreview(line);
  }

  /** The preview scrolled (RD18): at most once a frame, and never for the app's own scrolls or outside split view. */
  private previewScrolled(): void {
    if (this.mode !== 'split' || this.previewScrollQueued) return;
    if (this.echo.isEcho('preview', this.previewPane.scrollTop)) return;
    this.previewScrollQueued = true;
    requestAnimationFrame(() => {
      this.previewScrollQueued = false;
      this.syncEditor();
    });
  }

  /** Moves the editor to the source line at the top of the preview, or to its end when the preview is at its end. */
  private syncEditor(): void {
    if (this.mode !== 'split') return;
    const pane = this.previewPane;
    const doc = this.view.state.doc;
    const atEnd = pane.scrollHeight - pane.clientHeight > 1 && pane.scrollTop >= pane.scrollHeight - pane.clientHeight - 1;
    const line = lineAtOffset(previewBlocks(this.preview, pane), pane.scrollTop, { line: doc.lines, top: pane.scrollHeight });
    this.echo.expect('editor', atEnd ? scrollToEnd(this.view) : scrollToTopLine(this.view, line));
    // A re-render restores the preview to the editor's top line, so keep that line current.
    this.syncLine = topLine(this.view);
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
      else if (ref && !LISTED_EXTENSIONS.test(resolved) && this.platform.inspectLinkedFile) {
        // RD16: a document, a spreadsheet or a script. Only a folder is left to the Markdown rule.
        if ((await this.linkedFile(ref, href, split.path)) === 'folder') this.notify(`Only Markdown files open here: ${href}`);
      } else this.notify(`Only Markdown files open here: ${href}`);
      return;
    }
    // RD16: a link from a lone file to something that is not Markdown or text never discards the
    // open document, so it is judged first. A link to a folder carries on to the README rule.
    if (ref && this.platform.inspectLinkedFile && !opensInReadout(split.path)) {
      if ((await this.linkedFile(ref, href, split.path)) === 'handled') return;
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

  /**
   * RD16 (desktop only): a relative link to a file that is not Markdown. Rust resolves and judges
   * it; a refusal is explained, an allowed document type opens in its default application only
   * after the reader confirms, and Cancel, Escape or a closed dialog do nothing. Resolves
   * `folder` when the link points to a folder, which the caller treats as a Markdown link.
   */
  private async linkedFile(ref: FileRef, href: string, path: string): Promise<'handled' | 'folder'> {
    const platform = this.platform;
    try {
      const check = await platform.inspectLinkedFile!(ref, path);
      if (check.status === 'folder') return 'folder';
      if (check.status === 'refused') {
        this.notify(check.message);
        return 'handled';
      }
      if (!(await linkedFileDialog(check))) return 'handled';
      await platform.openLinkedFile!(ref, path, check);
      this.notify(null);
    } catch (error) {
      this.notify(`Could not open ${href}: ${message(error)}`);
    }
    return 'handled';
  }

  /** Double-clicking rendered text moves the editor to its source line. */
  /** Folds are kept by position, so a fold the reader opened stays open while they type. */
  private rememberFold(event: Event): void {
    const target = event.target;
    if (!(target instanceof HTMLDetailsElement)) return;
    const index = [...this.preview.querySelectorAll('details')].indexOf(target);
    if (index >= 0) this.folds.set(index, target.open);
  }

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

/**
 * The confirmation before a linked file opens in another program (RD16): the full path and the
 * file type, Open as an ordinary button and Cancel as the default (focused, so Enter and Escape
 * both decline). Resolves true only when the reader clicks Open.
 */
function linkedFileDialog(check: LinkedFileCheck): Promise<boolean> {
  return new Promise((resolve) => {
    const dialog = el('dialog', { class: 'modal', 'aria-labelledby': 'linked-file-title', 'aria-describedby': 'linked-file-body' }) as HTMLDialogElement;
    const open = el('button', { type: 'button', class: 'text-button' }, 'Open');
    const cancel = el('button', { type: 'button', class: 'primary' }, 'Cancel');
    let answer = false;
    open.addEventListener('click', () => {
      answer = true;
      dialog.close();
    });
    cancel.addEventListener('click', () => dialog.close());
    dialog.append(
      el('h2', { id: 'linked-file-title' }, 'Open linked file?'),
      el('p', { id: 'linked-file-body' }, 'This link points to a file that is not Markdown. Readout will hand it to the program your system uses for this type of file. Readout does not run programs.'),
      el('p', { class: 'linked-file' }, el('code', {}, check.path)),
      el('p', {}, `Type: ${check.kind}`),
      el('div', { class: 'modal-actions' }, open, cancel),
    );
    dialog.addEventListener('cancel', () => {
      answer = false;
    });
    dialog.addEventListener('close', () => {
      dialogState.open--;
      dialog.remove();
      resolve(answer);
    });
    dialogState.open++;
    document.body.append(dialog);
    trapTab(dialog);
    dialog.showModal();
    cancel.focus();
  });
}

function confirmDialog(title: string, body: string, choices: string[]): Promise<string | null> {
  return new Promise((resolve) => {
    const dialog = el('dialog', { class: 'modal', 'aria-labelledby': 'confirm-title', 'aria-describedby': 'confirm-body' }) as HTMLDialogElement;
    const buttons = choices.map((choice, i) => {
      const b = el('button', { type: 'button', class: i === 0 ? 'primary' : 'text-button' }, choice);
      b.addEventListener('click', () => {
        dialog.close();
        resolve(choice);
      });
      return b;
    });
    dialog.append(el('h2', { id: 'confirm-title' }, title), el('p', { id: 'confirm-body' }, body), el('div', { class: 'modal-actions' }, ...buttons));
    dialog.addEventListener('cancel', () => resolve(null));
    dialog.addEventListener('close', () => {
      dialogState.open--;
      dialog.remove();
    });
    dialogState.open++;
    document.body.append(dialog);
    trapTab(dialog);
    dialog.showModal();
    buttons[0]?.focus();
  });
}
