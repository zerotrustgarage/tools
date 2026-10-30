// Two-way scroll sync (RD18). The preview's blocks carry data-line, the zero-based source line they
// start on (render.ts). The editor already drives the preview; this is the other direction: the
// source line at the top of the preview, and a guard that keeps one pane's scroll from being
// mistaken for the reader's scroll in the other.

/** A rendered block: the source line it starts on and where its top edge is in the preview's scrolled content. */
export interface Block {
  line: number;
  top: number;
}

/**
 * The (fractional) zero-based source line at the top of a preview scrolled to `offset`.
 *
 * `blocks` are the visible [data-line] elements in document order. Between two blocks the line is
 * interpolated, so a long table, code block or list moves the editor smoothly instead of in
 * jumps; the gap between two blocks (their margin) stands for the blank lines between them.
 * `end` is the bottom of the content and the line count, the far edge of the last block.
 */
export function lineAtOffset(blocks: readonly Block[], offset: number, end: Block): number {
  const first = blocks[0];
  if (!first) return 0;
  if (offset < first.top) return first.line > 0 && first.top > 0 ? (first.line * Math.max(offset, 0)) / first.top : first.line;
  // The last block that starts at or above the offset (tops only ever grow in document order).
  let current = 0;
  for (let i = 1; i < blocks.length && blocks[i]!.top <= offset; i++) current = i;
  const here = blocks[current]!;
  // The next block that starts later in the file and lower on the page (a list item shares its
  // list's line, a paragraph in an item shares the item's).
  let next: Block = end;
  for (let i = current + 1; i < blocks.length; i++) {
    const candidate = blocks[i]!;
    if (candidate.line > here.line && candidate.top > here.top) {
      next = candidate;
      break;
    }
  }
  const span = next.top - here.top;
  if (span <= 0 || next.line <= here.line) return here.line;
  const fraction = Math.min(Math.max((offset - here.top) / span, 0), 1);
  return here.line + fraction * (next.line - here.line);
}

/**
 * The blocks of a rendered preview, from its data-line elements. `pane` is the scrolling element,
 * so a block's top is measured in the pane's own scroll coordinates. Elements inside a closed
 * <details> have no box and are skipped; an attribute that is not a plain number is ignored (the
 * sanitizer only lets numbers through, but a document can still carry odd ones).
 */
export function previewBlocks(content: ParentNode, pane: HTMLElement): Block[] {
  const origin = pane.getBoundingClientRect().top - pane.scrollTop;
  const blocks: Block[] = [];
  for (const node of content.querySelectorAll<HTMLElement>('[data-line]')) {
    const text = node.getAttribute('data-line') ?? '';
    if (!/^\d{1,9}$/.test(text)) continue;
    const rect = node.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) continue;
    blocks.push({ line: Number(text), top: rect.top - origin });
  }
  return blocks;
}

export type Pane = 'editor' | 'preview';

/**
 * Tells a scroll the app caused from one the reader made. Whenever the app sets a pane's scroll
 * position it calls `expect(pane, top)`; the scroll event that follows (a frame later) is then
 * recognised by `isEcho` and does not drive the other pane, so the two never chase each other.
 * An echo is any scroll of that pane within a short window, or one that lands where the app put
 * it a little later (a slow frame). A scroll that never fires (the position did not change) just
 * lets the window run out.
 */
export class EchoGuard {
  private mark: { pane: Pane; top: number; at: number } | null = null;

  constructor(
    private readonly now: () => number = () => performance.now(),
    private readonly windowMs = 150,
    private readonly lateMs = 1000,
    private readonly tolerance = 1.5,
  ) {}

  expect(pane: Pane, top: number): void {
    this.mark = { pane, top, at: this.now() };
  }

  isEcho(pane: Pane, top: number): boolean {
    const mark = this.mark;
    if (!mark || mark.pane !== pane) return false;
    const age = this.now() - mark.at;
    if (age <= this.windowMs) return true;
    if (age <= this.lateMs && Math.abs(top - mark.top) <= this.tolerance) return true;
    if (age > this.lateMs) this.mark = null;
    return false;
  }
}
