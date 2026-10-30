// Focus mode (RD17): which lines are "the current paragraph". A paragraph here is the run of
// non-blank lines around a line, the way a writer sees it in the source; a blank line is a block
// of its own, so pressing Enter twice dims the text you just left rather than leaving it lit.

export interface LineRange {
  /** One-based, inclusive. */
  from: number;
  to: number;
}

const blank = (text: string): boolean => text.trim() === '';

/** The paragraph around one-based `line`. `text(n)` returns line n's text; lines run 1..count. */
export function paragraphAround(text: (n: number) => string, count: number, line: number): LineRange {
  const at = Math.min(Math.max(line, 1), count);
  const isBlank = blank(text(at));
  let from = at;
  let to = at;
  while (from > 1 && blank(text(from - 1)) === isBlank) from--;
  while (to < count && blank(text(to + 1)) === isBlank) to++;
  return { from, to };
}

/** The lines to keep lit for a selection: from its first paragraph's start to its last paragraph's end. */
export function focusRange(text: (n: number) => string, count: number, fromLine: number, toLine: number): LineRange {
  const start = paragraphAround(text, count, Math.min(fromLine, toLine));
  const end = paragraphAround(text, count, Math.max(fromLine, toLine));
  return { from: start.from, to: end.to };
}
