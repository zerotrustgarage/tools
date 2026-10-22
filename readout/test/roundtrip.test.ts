import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { EditorSelection } from '@codemirror/state';
import { EditorView } from '@codemirror/view';
import { describe, expect, it } from 'vitest';
import { decodeDocument, encodeDocument, readOnlyReason } from '../src/core/document';
import { createEditorState, documentText } from '../src/ui/editor';

// The promise this editor makes: open a file, save it without editing, and get the same bytes.

const corpus = join(__dirname, 'corpus');
const files = readdirSync(corpus).filter((f) => f.endsWith('.md'));
const noop = () => undefined;

function throughEditor(bytes: Uint8Array): Uint8Array {
  const { text, format } = decodeDocument(bytes);
  const state = createEditorState({ text, format, readOnly: false, onChange: noop, onCursor: noop, onScroll: noop });
  return encodeDocument(documentText(state), format);
}

describe('byte-exact round trip through the editor', () => {
  it('has a corpus', () => expect(files.length).toBeGreaterThan(3));

  for (const file of files) {
    it(file, () => {
      const bytes = new Uint8Array(readFileSync(join(corpus, file)));
      expect(Buffer.from(throughEditor(bytes)).equals(Buffer.from(bytes))).toBe(true);
    });

    it(`${file} converted to CRLF`, () => {
      const lf = decodeDocument(new Uint8Array(readFileSync(join(corpus, file)))).text.replace(/\r\n/g, '\n');
      const bytes = new TextEncoder().encode(lf.replace(/\n/g, '\r\n'));
      expect(Buffer.from(throughEditor(bytes)).equals(Buffer.from(bytes))).toBe(true);
    });
  }

  it('keeps a BOM and a missing trailing newline', () => {
    const bytes = new Uint8Array(readFileSync(join(corpus, 'bom-no-trailing-newline.md')));
    const { format, text } = decodeDocument(bytes);
    expect(format.bom).toBe(true);
    expect(text.startsWith('﻿')).toBe(false);
    expect(text.endsWith('newline')).toBe(true);
  });
});

describe('formats that cannot be saved safely', () => {
  it('opens invalid UTF-8 read-only and refuses to save it', () => {
    const decoded = decodeDocument(new Uint8Array([0x23, 0x20, 0xff, 0xfe, 0x0a]));
    expect(decoded.format.validUtf8).toBe(false);
    expect(readOnlyReason(decoded.format)).toMatch(/not valid UTF-8/);
    expect(() => encodeDocument(decoded.text, decoded.format)).toThrow();
  });

  it('opens mixed line endings read-only', () => {
    const decoded = decodeDocument(new TextEncoder().encode('a\r\nb\nc\r\n'));
    expect(decoded.format.mixedEndings).toBe(true);
    expect(readOnlyReason(decoded.format)).toMatch(/mixes line endings/);
  });

  it('treats lone CR as mixed', () => {
    expect(decodeDocument(new TextEncoder().encode('a\rb\r')).format.mixedEndings).toBe(true);
  });
});

describe('edits keep the file line ending', () => {
  for (const eol of ['\n', '\r\n'] as const) {
    it(`pasting LF, CRLF and CR text into a ${eol === '\n' ? 'LF' : 'CRLF'} file`, () => {
      const original = `# Title${eol}${eol}Body${eol}`;
      const { text, format } = decodeDocument(new TextEncoder().encode(original));
      const view = new EditorView({
        state: createEditorState({ text, format, readOnly: false, onChange: noop, onCursor: noop, onScroll: noop }),
        parent: document.body,
      });
      // CodeMirror applies the clipboard filters to pasted text; exercise the same facet.
      const pasted = view.state
        .facet(EditorView.clipboardInputFilter)
        .reduce((t, filter) => filter(t, view.state), 'one\ntwo\r\nthree\rfour');
      view.dispatch(view.state.replaceSelection(pasted), { selection: EditorSelection.cursor(0) });
      const saved = documentText(view.state);
      expect(saved).toBe(`one${eol}two${eol}three${eol}four# Title${eol}${eol}Body${eol}`);
      expect(decodeDocument(encodeDocument(saved, format)).format.mixedEndings).toBe(false);
      view.destroy();
    });
  }
});
