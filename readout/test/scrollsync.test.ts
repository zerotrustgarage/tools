import { describe, expect, it } from 'vitest';
import { EchoGuard, lineAtOffset, previewBlocks, type Block } from '../src/core/scrollsync';
import { renderMarkdown } from '../src/core/render';

// RD18: the source line at the top of the preview, and the guard that stops the two panes
// from driving each other.

const end = (line: number, top: number): Block => ({ line, top });

describe('lineAtOffset', () => {
  // A heading (line 0), a paragraph (line 2) and a code block (line 4, three lines long), then the end at line 9.
  const blocks: Block[] = [
    { line: 0, top: 20 },
    { line: 2, top: 80 },
    { line: 4, top: 140 },
  ];
  const stop = end(9, 440);

  it('returns the block line at a block top and interpolates between blocks', () => {
    expect(lineAtOffset(blocks, 20, stop)).toBe(0);
    expect(lineAtOffset(blocks, 80, stop)).toBe(2);
    expect(lineAtOffset(blocks, 50, stop)).toBe(1);
    expect(lineAtOffset(blocks, 110, stop)).toBe(3);
  });

  it('interpolates through the last block to the end of the content', () => {
    expect(lineAtOffset(blocks, 140, stop)).toBe(4);
    expect(lineAtOffset(blocks, 290, stop)).toBe(6.5);
    expect(lineAtOffset(blocks, 440, stop)).toBe(9);
  });

  it('clamps above the first block and beyond the end', () => {
    expect(lineAtOffset(blocks, 0, stop)).toBe(0);
    expect(lineAtOffset(blocks, -30, stop)).toBe(0);
    expect(lineAtOffset(blocks, 900, stop)).toBe(9);
  });

  it('maps the space above a first block that is not on line 0 (front matter) onto the lines before it', () => {
    expect(lineAtOffset([{ line: 6, top: 60 }], 30, end(10, 200))).toBe(3);
    expect(lineAtOffset([{ line: 6, top: 60 }], 60, end(10, 200))).toBe(6);
  });

  it('ignores blocks that share a line with an enclosing block', () => {
    // ul, li and the paragraph in it all start on line 3; the next item starts on line 4.
    const list: Block[] = [
      { line: 3, top: 100 },
      { line: 3, top: 100 },
      { line: 3, top: 104 },
      { line: 4, top: 160 },
    ];
    expect(lineAtOffset(list, 100, end(6, 300))).toBe(3);
    // Measured from the paragraph inside the item (top 104) to the next item (top 160).
    expect(lineAtOffset(list, 132, end(6, 300))).toBeCloseTo(3.5, 5);
    expect(lineAtOffset(list, 160, end(6, 300))).toBe(4);
  });

  it('is zero for an empty preview', () => {
    expect(lineAtOffset([], 100, stop)).toBe(0);
  });

  it('never goes backwards as the offset grows', () => {
    let last = -1;
    for (let offset = 0; offset <= 460; offset += 7) {
      const line = lineAtOffset(blocks, offset, stop);
      expect(line).toBeGreaterThanOrEqual(last);
      last = line;
    }
  });
});

describe('previewBlocks', () => {
  const rect = (top: number, height = 20): DOMRect => ({ top, height, width: 100, bottom: top + height, left: 0, right: 100, x: 0, y: top, toJSON: () => ({}) }) as DOMRect;

  it('reads data-line elements in the pane scroll coordinates and skips boxless and malformed ones', () => {
    const pane = document.createElement('div');
    pane.innerHTML = '<p data-line="0">a</p><p data-line="3">b</p><p data-line="x">c</p><p data-line="-2">d</p><details><p data-line="5">hidden</p></details><p>e</p>';
    pane.getBoundingClientRect = () => rect(100, 400);
    Object.defineProperty(pane, 'scrollTop', { value: 50 });
    const tops = new Map<string, number>([['a', 90], ['b', 200]]);
    for (const p of pane.querySelectorAll('p')) {
      const text = p.textContent ?? '';
      p.getBoundingClientRect = () => (text === 'hidden' ? ({ top: 0, height: 0, width: 0 } as DOMRect) : rect(tops.get(text) ?? 0));
    }
    // origin = 100 - 50 = 50, so a block's top is its viewport top minus 50.
    expect(previewBlocks(pane, pane)).toEqual([
      { line: 0, top: 40 },
      { line: 3, top: 150 },
    ]);
  });
});

describe('source line markers in the render pipeline', () => {
  const root = (md: string): HTMLElement => {
    const div = document.createElement('div');
    div.innerHTML = renderMarkdown(md);
    return div;
  };

  it('marks each block with the line it starts on', () => {
    const r = root('# Title\n\nPara one\ncontinues\n\n- a\n- b\n\n```js\ncode\n```\n\n| h |\n|---|\n| c |\n');
    const lines = [...r.querySelectorAll('[data-line]')].map((n) => `${n.tagName.toLowerCase()}:${n.getAttribute('data-line')}`);
    expect(lines).toEqual(expect.arrayContaining(['h1:0', 'p:2', 'ul:5', 'li:5', 'li:6', 'pre:8', 'table:12']));
  });

  it('keeps only numeric data-line values from a document', () => {
    const r = root('<p data-line="7">one</p>\n\n<p data-line="7;x">two</p>\n\n<p data-line="">three</p>\n\n<p data-line="1e3">four</p>\n');
    const marks = [...r.querySelectorAll('p')].map((p) => p.getAttribute('data-line'));
    expect(marks).toEqual(['7', null, null, null]);
  });
});

describe('EchoGuard', () => {
  const guard = (): { guard: EchoGuard; at: (ms: number) => void } => {
    let now = 0;
    return { guard: new EchoGuard(() => now), at: (ms) => (now = ms) };
  };

  it('treats nothing as an echo before the app has scrolled a pane', () => {
    const { guard: g } = guard();
    expect(g.isEcho('editor', 100)).toBe(false);
    expect(g.isEcho('preview', 100)).toBe(false);
  });

  it('recognises the scroll that follows the app moving a pane, and only that pane', () => {
    const { guard: g, at } = guard();
    g.expect('editor', 400);
    at(16);
    expect(g.isEcho('editor', 400)).toBe(true);
    expect(g.isEcho('editor', 398)).toBe(true); // the editor settling a little
    expect(g.isEcho('preview', 400)).toBe(false); // the other pane is the reader's
  });

  it('does not feed back: a sync from the preview is not seen as an editor scroll, and the next real one is', () => {
    const { guard: g, at } = guard();
    // The reader scrolls the preview; the app moves the editor and notes it.
    g.expect('editor', 640);
    at(10);
    expect(g.isEcho('editor', 640)).toBe(true); // would otherwise move the preview again
    at(400);
    expect(g.isEcho('editor', 900)).toBe(false); // the reader scrolls the editor later
  });

  it('accepts a late echo only where the app put the pane', () => {
    const { guard: g, at } = guard();
    g.expect('preview', 300);
    at(600);
    expect(g.isEcho('preview', 300)).toBe(true);
    expect(g.isEcho('preview', 500)).toBe(false);
    at(1500);
    expect(g.isEcho('preview', 300)).toBe(false);
  });

  it('follows the latest expectation', () => {
    const { guard: g, at } = guard();
    g.expect('editor', 10);
    at(5);
    g.expect('preview', 20);
    expect(g.isEcho('editor', 10)).toBe(false);
    expect(g.isEcho('preview', 20)).toBe(true);
  });
});
