import type { MarkdownIt, StateBlock, StateInline } from 'markdown-it';

// Math in the forms GitHub accepts: $inline$, $$display$$ (one line or a block), and ```math
// fences (handled by the fence renderer). This plugin emits placeholders holding the TeX
// source as escaped text; KaTeX renders them after sanitization (see postprocess.ts), so the
// sanitizer never has to trust KaTeX's markup and an unrendered placeholder still shows the
// source.

const isSpace = (code: number | undefined) => code === 0x20 || code === 0x09 || code === 0x0a;
const isDigit = (code: number | undefined) => code !== undefined && code >= 0x30 && code <= 0x39;

function mathInline(state: StateInline, silent: boolean): boolean {
  const src = state.src;
  const start = state.pos;
  if (src.charCodeAt(start) !== 0x24 /* $ */) return false;
  if (src.charCodeAt(start + 1) === 0x24) return false; // $$ is display math
  // "$5 and $10" is money, not math: the opener must not be followed by whitespace, and the
  // closer must not follow whitespace or precede a digit.
  if (isSpace(src.charCodeAt(start + 1))) return false;
  let end = start + 1;
  while (end < state.posMax) {
    const c = src.charCodeAt(end);
    if (c === 0x5c /* \ */) {
      end += 2;
      continue;
    }
    if (c === 0x24) break;
    end++;
  }
  if (end >= state.posMax || end === start + 1) return false;
  if (isSpace(src.charCodeAt(end - 1)) || isDigit(src.charCodeAt(end + 1))) return false;
  if (!silent) {
    const token = state.push('math_inline', 'span', 0);
    token.markup = '$';
    token.content = src.slice(start + 1, end);
  }
  state.pos = end + 1;
  return true;
}

function mathBlock(state: StateBlock, startLine: number, endLine: number, silent: boolean): boolean {
  let pos = state.bMarks[startLine]! + state.tShift[startLine]!;
  let max = state.eMarks[startLine]!;
  if (state.sCount[startLine]! - state.blkIndent >= 4) return false;
  if (state.src.slice(pos, pos + 2) !== '$$') return false;
  if (silent) return true;

  pos += 2;
  let firstLine = state.src.slice(pos, max);
  let lastLine = '';
  let next = startLine;
  let found = false;
  if (firstLine.trim().endsWith('$$')) {
    firstLine = firstLine.trim().slice(0, -2);
    found = true;
  }
  while (!found) {
    next++;
    if (next >= endLine) break;
    pos = state.bMarks[next]! + state.tShift[next]!;
    max = state.eMarks[next]!;
    if (pos < max && state.tShift[next]! < state.blkIndent) break;
    const line = state.src.slice(pos, max).trim();
    if (line.endsWith('$$')) {
      lastLine = line.slice(0, -2);
      found = true;
    }
  }
  // An unclosed $$ is left to the paragraph rule, as GitHub does.
  if (!found) return false;

  state.line = next + 1;
  const token = state.push('math_block', 'div', 0);
  token.block = true;
  token.content =
    (firstLine.trim() ? firstLine + '\n' : '') +
    state.getLines(startLine + 1, next, state.tShift[startLine]!, true) +
    (lastLine.trim() ? lastLine : '');
  token.map = [startLine, state.line];
  token.markup = '$$';
  return true;
}

export function mathPlaceholder(tex: string, display: boolean, escape: (s: string) => string, line?: number): string {
  const lineAttr = line === undefined ? '' : ` data-line="${line}"`;
  return display
    ? `<div class="math math-display"${lineAttr} data-tex="${escape(tex)}">${escape(tex)}</div>\n`
    : `<span class="math math-inline" data-tex="${escape(tex)}">${escape(tex)}</span>`;
}

export function mathPlugin(md: MarkdownIt): void {
  md.inline.ruler.after('escape', 'math_inline', mathInline);
  md.block.ruler.before('fence', 'math_block', mathBlock, {
    alt: ['paragraph', 'reference', 'blockquote', 'list'],
  });
  const escape = md.utils.escapeHtml;
  md.renderer.rules.math_inline = (tokens, idx) => mathPlaceholder(tokens[idx]!.content, false, escape);
  md.renderer.rules.math_block = (tokens, idx) =>
    mathPlaceholder(tokens[idx]!.content, true, escape, tokens[idx]!.map?.[0]);
}
