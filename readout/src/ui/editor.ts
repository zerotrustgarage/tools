import { defaultKeymap, history, historyKeymap, indentWithTab, temporarilySetTabFocusMode } from '@codemirror/commands';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { HighlightStyle, bracketMatching, syntaxHighlighting } from '@codemirror/language';
import { languages } from '@codemirror/language-data';
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search';
import { Compartment, EditorState, type Extension, type Range } from '@codemirror/state';
import {
  Decoration,
  type DecorationSet,
  EditorView,
  ViewPlugin,
  type ViewUpdate,
  drawSelection,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from '@codemirror/view';
import { tags as t } from '@lezer/highlight';
import type { DocumentFormat } from '../core/document';
import { focusRange } from '../core/focus';
import { formattingKeymap } from './format';

// The source editor. Its text IS the file: there is no document model in between, so bytes
// the user did not touch are never rewritten (see core/document.ts for the rest of that
// guarantee).

const markdownHighlight = HighlightStyle.define([
  { tag: t.heading1, class: 'cm-md-h1' },
  { tag: t.heading2, class: 'cm-md-h2' },
  { tag: [t.heading3, t.heading4, t.heading5, t.heading6], class: 'cm-md-h' },
  { tag: t.strong, class: 'cm-md-strong' },
  { tag: t.emphasis, class: 'cm-md-em' },
  { tag: t.strikethrough, class: 'cm-md-strike' },
  { tag: [t.link, t.url], class: 'cm-md-link' },
  { tag: t.monospace, class: 'cm-md-code' },
  { tag: [t.processingInstruction, t.meta, t.contentSeparator], class: 'cm-md-mark' },
  { tag: t.quote, class: 'cm-md-quote' },
  { tag: t.list, class: 'cm-md-list' },
  { tag: [t.keyword, t.operator, t.modifier], class: 'cm-tok-keyword' },
  { tag: [t.string, t.special(t.string), t.regexp], class: 'cm-tok-string' },
  { tag: [t.number, t.bool, t.null, t.atom], class: 'cm-tok-number' },
  { tag: [t.comment, t.lineComment, t.blockComment], class: 'cm-tok-comment' },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], class: 'cm-tok-function' },
  { tag: [t.typeName, t.className, t.tagName], class: 'cm-tok-type' },
  { tag: [t.propertyName, t.attributeName], class: 'cm-tok-property' },
]);

/**
 * Editor chrome in the app's colours. It has to be an EditorView.theme: CodeMirror's base theme
 * styles the gutter, active line and selection with scoped selectors that outrank ordinary CSS,
 * which left a light gray line-number strip in the dark theme. The values are the app's CSS
 * variables, so one theme serves light and dark.
 */
const ztgEditorTheme = EditorView.theme({
  '&': { height: '100%', flex: '1', backgroundColor: 'var(--panel)', color: 'var(--text)', fontSize: '14px' },
  // The editor shows where focus is with an inset ring (the caret alone is easy to lose).
  '&.cm-focused': { outline: '2px solid var(--focus)', outlineOffset: '-2px' },
  '.cm-scroller': {
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace",
    lineHeight: '1.6',
  },
  '.cm-content': { padding: '16px 0', caretColor: 'var(--acc)' },
  '.cm-line': { padding: '0 16px' },
  '.cm-gutters': {
    backgroundColor: 'var(--panel)',
    color: 'var(--dim)',
    border: 'none',
    borderRight: '1px solid var(--line)',
  },
  '.cm-lineNumbers .cm-gutterElement': { padding: '0 10px 0 14px', minWidth: '32px' },
  '.cm-activeLine': { backgroundColor: 'var(--active-line)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--active-line)', color: 'var(--text)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--acc)', borderLeftWidth: '2px' },
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection':
    { backgroundColor: 'var(--selection)' },
  '.cm-selectionMatch': { backgroundColor: 'var(--selection-match)' },
  '.cm-matchingBracket, &.cm-focused .cm-matchingBracket': { backgroundColor: 'var(--selection-match)', outline: 'none' },
  '.cm-searchMatch': { backgroundColor: 'color-mix(in srgb, var(--warn) 30%, transparent)', outline: 'none' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'color-mix(in srgb, var(--warn) 55%, transparent)' },
  '.cm-panels': { backgroundColor: 'var(--panel2)', color: 'var(--text)' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid var(--line)' },
  '.cm-panel input, .cm-panel button, .cm-panel select': {
    color: 'var(--text)',
    backgroundColor: 'var(--panel)',
    border: '1px solid var(--dim2)',
    borderRadius: '4px',
  },
  '.cm-panel.cm-search label': { color: 'var(--dim)' },
  '.cm-tooltip': { backgroundColor: 'var(--panel2)', color: 'var(--text)', border: '1px solid var(--line)' },
  // Focus mode (RD17): lines outside the current paragraph take one palette colour (--focus-dim,
  // 3:1 or better on the editor background, see test/contrast.test.ts); the current paragraph is untouched.
  '.cm-focus-dim, .cm-focus-dim span': { color: 'var(--focus-dim)' },
  // A find match keeps a legible colour on its highlight (the dim colour would drop under 3:1 on it).
  '.cm-focus-dim .cm-searchMatch': { color: 'var(--bright)' },
  // Typewriter mode: room above and below the text so the first and last lines can reach the middle too.
  '&.cm-typewriter .cm-content': { paddingTop: 'var(--typewriter-pad, 40vh)', paddingBottom: 'var(--typewriter-pad, 40vh)' },
});

export interface EditorOptions {
  parent: HTMLElement;
  text: string;
  format: DocumentFormat;
  readOnly: boolean;
  onChange: () => void;
  onCursor: (line: number, column: number) => void;
  onScroll: (topLine: number) => void;
  /** An image was pasted (RD13). Called instead of the paste; absent keeps the editor's own paste. */
  onPasteImage?: (file: File) => void;
  /** Focus mode and typewriter mode (RD17); both off when absent. */
  focusMode?: boolean;
  typewriterMode?: boolean;
}

const readOnlyCompartment = new Compartment();
const focusCompartment = new Compartment();
const typewriterCompartment = new Compartment();

// ---- focus mode (RD17) -----------------------------------------------------------------

const dimLine = Decoration.line({ class: 'cm-focus-dim' });

/** A dim decoration on every line of [from, to] outside the current paragraph (the paragraphs each selection range touches). */
export function focusDecorations(state: EditorState, from: number, to: number): DecorationSet {
  const doc = state.doc;
  // Every selection range keeps its paragraphs lit, so a selected line is never dimmed.
  const lit = state.selection.ranges.map((r) =>
    focusRange((n) => doc.line(n).text, doc.lines, doc.lineAt(r.from).number, doc.lineAt(r.to).number),
  );
  const ranges: Range<Decoration>[] = [];
  const first = doc.lineAt(from).number;
  const last = doc.lineAt(Math.min(to, doc.length)).number;
  for (let n = first; n <= last; n++) if (!lit.some((r) => n >= r.from && n <= r.to)) ranges.push(dimLine.range(doc.line(n).from));
  return Decoration.set(ranges);
}

const focusPlugin = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;
    constructor(view: EditorView) {
      this.decorations = focusDecorations(view.state, view.viewport.from, view.viewport.to);
    }
    update(update: ViewUpdate): void {
      if (update.docChanged || update.selectionSet || update.viewportChanged)
        this.decorations = focusDecorations(update.state, update.view.viewport.from, update.view.viewport.to);
    }
  },
  { decorations: (plugin) => plugin.decorations },
);

// ---- typewriter mode (RD17) ------------------------------------------------------------

/** Typing, deleting, undo and keyboard cursor moves keep the line centred; a click or a programmatic jump does not. */
function recentres(update: ViewUpdate): boolean {
  return update.transactions.some(
    (tr) =>
      tr.isUserEvent('input') ||
      tr.isUserEvent('delete') ||
      tr.isUserEvent('undo') ||
      tr.isUserEvent('redo') ||
      tr.isUserEvent('move') ||
      (tr.isUserEvent('select') && !tr.isUserEvent('select.pointer')),
  );
}

const typewriterPlugin = ViewPlugin.fromClass(
  class {
    constructor(private readonly view: EditorView) {
      this.setPadding();
      this.centre();
    }
    update(update: ViewUpdate): void {
      if (update.geometryChanged) this.setPadding();
      if (recentres(update)) this.centre();
    }
    /** Half the editor's height, so a line at either end of the document can still be centred. */
    private setPadding(): void {
      const half = Math.max(0, Math.floor(this.view.scrollDOM.clientHeight / 2) - 24);
      if (half > 0) this.view.dom.style.setProperty('--typewriter-pad', `${half}px`);
    }
    private centre(): void {
      this.view.requestMeasure({
        key: this,
        read: (view) => {
          const caret = view.coordsAtPos(view.state.selection.main.head);
          if (!caret) return null;
          const box = view.scrollDOM.getBoundingClientRect();
          return (caret.top + caret.bottom) / 2 - (box.top + box.height / 2);
        },
        write: (delta, view) => {
          if (delta !== null && Math.abs(delta) > 1) view.scrollDOM.scrollTop += delta;
        },
      });
    }
    destroy(): void {
      this.view.dom.style.removeProperty('--typewriter-pad');
    }
  },
);

const typewriterExtensions = (on: boolean): Extension => (on ? [typewriterPlugin, EditorView.editorAttributes.of({ class: 'cm-typewriter' })] : []);
const focusExtensions = (on: boolean): Extension => (on ? focusPlugin : []);

export function setFocusMode(view: EditorView, on: boolean): void {
  view.dispatch({ effects: focusCompartment.reconfigure(focusExtensions(on)) });
}

export function setTypewriterMode(view: EditorView, on: boolean): void {
  view.dispatch({ effects: typewriterCompartment.reconfigure(typewriterExtensions(on)) });
}

// ---- scroll sync (RD18) ----------------------------------------------------------------

/** The zero-based line at the top of the editor's viewport (document padding above the text excluded). */
export function topLine(view: EditorView): number {
  const block = view.lineBlockAtHeight(Math.max(0, view.scrollDOM.scrollTop - view.documentPadding.top));
  return view.state.doc.lineAt(block.from).number - 1;
}

/**
 * Scrolls so a (fractional) zero-based source line sits at the top of the viewport, without moving the
 * cursor. Returns the scroll position it ended on. The reverse of topLine.
 */
export function scrollToTopLine(view: EditorView, line: number): number {
  const doc = view.state.doc;
  const clamped = Math.min(Math.max(line, 0), doc.lines - 1);
  if (clamped === 0) {
    view.scrollDOM.scrollTop = 0;
    return view.scrollDOM.scrollTop;
  }
  const index = Math.floor(clamped);
  const block = view.lineBlockAt(doc.line(index + 1).from);
  view.scrollDOM.scrollTop = block.top + (clamped - index) * block.height + view.documentPadding.top;
  return view.scrollDOM.scrollTop;
}

/** Scrolls to the very bottom, returning the position reached. */
export function scrollToEnd(view: EditorView): number {
  view.scrollDOM.scrollTop = view.scrollDOM.scrollHeight;
  return view.scrollDOM.scrollTop;
}

/**
 * Line endings: CodeMirror's lineSeparator decides both how the file is split into lines and
 * how sliceDoc() joins them, so it is set to the file's own ending. Pasted or dropped text is
 * converted to that ending first; otherwise a pasted LF inside a CRLF file would stay inside a
 * line as a raw character and the file would end up with mixed endings.
 */
export function lineEndingExtensions(format: DocumentFormat): Extension[] {
  // A mixed file is read-only; let CodeMirror split it on any ending just to display it.
  if (format.mixedEndings) return [];
  const eol = format.eol;
  return [
    EditorState.lineSeparator.of(eol),
    EditorView.clipboardInputFilter.of((text) => text.replace(/\r\n|\r|\n/g, eol)),
  ];
}

export function createEditorState(options: Omit<EditorOptions, 'parent'>): EditorState {
  return EditorState.create({
    doc: options.text,
    extensions: [
      lineEndingExtensions(options.format),
      ztgEditorTheme,
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightActiveLine(),
      history(),
      drawSelection(),
      bracketMatching(),
      search({ top: true }),
      highlightSelectionMatches(),
      EditorView.lineWrapping,
      markdown({ base: markdownLanguage, codeLanguages: languages }),
      syntaxHighlighting(markdownHighlight),
      // Tab indents here, so Escape followed by Tab (for two seconds) leaves the editor by keyboard
      // (WCAG 2.1.2); Ctrl+M toggles the same mode. Escape still does what it did (closes the find panel).
      keymap.of([{ key: 'Escape', run: (view) => (temporarilySetTabFocusMode(view), false) }]),
      keymap.of([...formattingKeymap, ...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
      readOnlyCompartment.of([EditorState.readOnly.of(options.readOnly), EditorView.editable.of(!options.readOnly)]),
      focusCompartment.of(focusExtensions(options.focusMode === true)),
      typewriterCompartment.of(typewriterExtensions(options.typewriterMode === true)),
      EditorView.contentAttributes.of({ spellcheck: 'true', 'aria-label': 'Markdown source' }),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) options.onChange();
        if (update.selectionSet || update.docChanged) {
          const head = update.state.selection.main.head;
          const line = update.state.doc.lineAt(head);
          options.onCursor(line.number, head - line.from + 1);
        }
      }),
      EditorView.domEventHandlers({
        // A clipboard that holds an image and no text (a screenshot, "Copy image") is an image
        // paste; anything with text pastes as text, as it always did.
        paste: (event) => {
          const data = event.clipboardData;
          if (!options.onPasteImage || !data || data.getData('text/plain') !== '') return false;
          const image = Array.from(data.files ?? []).find((file) => file.type.startsWith('image/'));
          if (!image) return false;
          event.preventDefault();
          options.onPasteImage(image);
          return true;
        },
        scroll: (_event, view) => {
          options.onScroll(topLine(view));
        },
      }),
    ],
  });
}

export function createEditor(options: EditorOptions): EditorView {
  return new EditorView({ parent: options.parent, state: createEditorState(options) });
}

/** The document text with the file's own line endings (never doc.toString(), which uses LF). */
export const documentText = (state: EditorState): string => state.sliceDoc();

export interface JumpOptions {
  /** Zero-based offset into the line where a match starts, and its length, to select it. */
  column?: number;
  length?: number;
  /** Move keyboard focus into the editor (default true). */
  focus?: boolean;
}

export function scrollToLine(view: EditorView, zeroBasedLine: number, options: JumpOptions = {}): void {
  const line = view.state.doc.line(Math.min(view.state.doc.lines, Math.max(0, zeroBasedLine) + 1));
  const from = Math.min(line.from + (options.column ?? 0), line.to);
  const to = Math.min(from + (options.length ?? 0), line.to);
  view.dispatch({ selection: { anchor: from, head: to }, effects: EditorView.scrollIntoView(from, { y: 'start' }) });
  if (options.focus !== false) view.focus();
}
