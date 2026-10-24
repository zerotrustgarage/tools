import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { markdown, markdownLanguage } from '@codemirror/lang-markdown';
import { HighlightStyle, bracketMatching, syntaxHighlighting } from '@codemirror/language';
import { languages } from '@codemirror/language-data';
import { highlightSelectionMatches, search, searchKeymap } from '@codemirror/search';
import { Compartment, EditorState, type Extension } from '@codemirror/state';
import {
  EditorView,
  drawSelection,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
} from '@codemirror/view';
import { tags as t } from '@lezer/highlight';
import type { DocumentFormat } from '../core/document';
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
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': {
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace",
    lineHeight: '1.6',
  },
  '.cm-content': { padding: '16px 0', caretColor: 'var(--acc)' },
  '.cm-line': { padding: '0 16px' },
  '.cm-gutters': {
    backgroundColor: 'var(--panel)',
    color: 'var(--dim2)',
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
    border: '1px solid var(--line)',
    borderRadius: '4px',
  },
  '.cm-panel.cm-search label': { color: 'var(--dim)' },
  '.cm-tooltip': { backgroundColor: 'var(--panel2)', color: 'var(--text)', border: '1px solid var(--line)' },
});

export interface EditorOptions {
  parent: HTMLElement;
  text: string;
  format: DocumentFormat;
  readOnly: boolean;
  onChange: () => void;
  onCursor: (line: number, column: number) => void;
  onScroll: (topLine: number) => void;
}

const readOnlyCompartment = new Compartment();

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
      keymap.of([...formattingKeymap, ...defaultKeymap, ...historyKeymap, ...searchKeymap, indentWithTab]),
      readOnlyCompartment.of([EditorState.readOnly.of(options.readOnly), EditorView.editable.of(!options.readOnly)]),
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
        scroll: (_event, view) => {
          const block = view.lineBlockAtHeight(view.scrollDOM.scrollTop);
          options.onScroll(view.state.doc.lineAt(block.from).number - 1);
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
