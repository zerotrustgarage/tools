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

export function scrollToLine(view: EditorView, zeroBasedLine: number): void {
  const line = view.state.doc.line(Math.min(view.state.doc.lines, zeroBasedLine + 1));
  view.dispatch({ selection: { anchor: line.from }, effects: EditorView.scrollIntoView(line.from, { y: 'start' }) });
  view.focus();
}
