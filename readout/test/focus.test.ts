import { EditorState } from '@codemirror/state';
import { describe, expect, it } from 'vitest';
import { focusRange, paragraphAround } from '../src/core/focus';
import { focusDecorations } from '../src/ui/editor';

// RD17: which lines focus mode keeps lit, and the dim decoration it puts on the others.

const lines = (text: string): ((n: number) => string) => {
  const all = text.split('\n');
  return (n) => all[n - 1] ?? '';
};
const count = (text: string): number => text.split('\n').length;

describe('paragraphAround', () => {
  const text = 'one\ntwo\n\nthree\n\n\n\nfour\nfive\nsix';
  it('is the run of non-blank lines around a line', () => {
    expect(paragraphAround(lines(text), count(text), 1)).toEqual({ from: 1, to: 2 });
    expect(paragraphAround(lines(text), count(text), 2)).toEqual({ from: 1, to: 2 });
    expect(paragraphAround(lines(text), count(text), 4)).toEqual({ from: 4, to: 4 });
    expect(paragraphAround(lines(text), count(text), 9)).toEqual({ from: 8, to: 10 });
  });
  it('treats a run of blank lines as a block of its own', () => {
    expect(paragraphAround(lines(text), count(text), 3)).toEqual({ from: 3, to: 3 });
    expect(paragraphAround(lines(text), count(text), 6)).toEqual({ from: 5, to: 7 });
  });
  it('counts a whitespace-only line as blank', () => {
    const t = 'a\n   \t\nb';
    expect(paragraphAround(lines(t), 3, 1)).toEqual({ from: 1, to: 1 });
    expect(paragraphAround(lines(t), 3, 3)).toEqual({ from: 3, to: 3 });
  });
  it('clamps a line outside the document', () => {
    expect(paragraphAround(lines('a\nb'), 2, 99)).toEqual({ from: 1, to: 2 });
    expect(paragraphAround(lines('a\nb'), 2, 0)).toEqual({ from: 1, to: 2 });
  });
});

describe('focusRange', () => {
  const text = 'a\nb\n\nc\nd\n\ne';
  it('spans from the first paragraph of a selection to the last', () => {
    expect(focusRange(lines(text), count(text), 2, 4)).toEqual({ from: 1, to: 5 });
    expect(focusRange(lines(text), count(text), 4, 2)).toEqual({ from: 1, to: 5 });
  });
});

describe('focusDecorations', () => {
  const dimmed = (text: string, cursor: number, selectionTo?: number): number[] => {
    const state = EditorState.create({ doc: text, selection: { anchor: cursor, head: selectionTo ?? cursor } });
    const set = focusDecorations(state, 0, state.doc.length);
    const out: number[] = [];
    set.between(0, state.doc.length, (from) => {
      out.push(state.doc.lineAt(from).number);
    });
    return out;
  };
  const text = 'one\ntwo\n\nthree\nfour\n\nfive';

  it('dims every line outside the cursor paragraph and none inside it', () => {
    expect(dimmed(text, 1)).toEqual([3, 4, 5, 6, 7]); // cursor in "one": lines 1 and 2 stay lit
    expect(dimmed(text, text.indexOf('four'))).toEqual([1, 2, 3, 6, 7]);
  });
  it('dims all the text while the cursor is on a blank line, and lights the paragraph again once text is typed', () => {
    expect(dimmed(text, text.indexOf('\n\n') + 1)).toEqual([1, 2, 4, 5, 6, 7]);
  });
  it('keeps every paragraph a selection touches lit', () => {
    expect(dimmed(text, 1, text.indexOf('three'))).toEqual([6, 7]);
  });
  it('limits the work to the given range', () => {
    const state = EditorState.create({ doc: text, selection: { anchor: 0 } });
    const set = focusDecorations(state, state.doc.line(4).from, state.doc.line(5).to);
    const out: number[] = [];
    set.between(0, state.doc.length, (from) => void out.push(state.doc.lineAt(from).number));
    expect(out).toEqual([4, 5]);
  });
});
