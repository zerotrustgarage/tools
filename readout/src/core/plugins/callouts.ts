import type { MarkdownIt, StateBlock, StateCore, Token } from 'markdown-it';

// Callouts in the three forms documentation and notes use, rendered as one element so they look
// the same whichever tool wrote them. AI tools emit these constantly, and an editor that shows
// them as plain quotes with a literal "[!WARNING]" loses the document's intent.
//
// - GitHub alerts and Obsidian callouts: a blockquote whose first line starts with [!type],
//   optionally followed by + or - (foldable, open or closed) and a title.
// - MkDocs admonitions: "!!! type "Title"" with an indented body; "???" folds closed, "???+" open.
// - Docusaurus admonitions: ":::type[Title]" (or ":::type Title") up to a closing ":::"; a parent
//   that holds another uses more colons, as Docusaurus requires.
//
// Each renders as <div class="markdown-alert markdown-alert-{style}" data-callout="{type}">, or
// as <details> with a <summary> title when foldable. The style is one of GitHub's five alert
// colours, or "quote"; types are matched case-insensitively and unknown types look like a note,
// as Obsidian does. Rendering only: the source is never changed.

export const CALLOUT_STYLES = ['note', 'tip', 'important', 'warning', 'caution', 'quote'] as const;
export type CalloutStyle = (typeof CALLOUT_STYLES)[number];

// Obsidian's types and aliases, MkDocs' and Docusaurus' names, mapped onto the alert palette.
// GitHub's five keep their own colours, so "important" and "caution" are not Obsidian aliases.
const STYLE_OF: Record<string, CalloutStyle> = {
  note: 'note',
  info: 'note',
  todo: 'note',
  abstract: 'note',
  summary: 'note',
  tldr: 'note',
  seealso: 'note',
  tip: 'tip',
  hint: 'tip',
  success: 'tip',
  check: 'tip',
  done: 'tip',
  important: 'important',
  example: 'important',
  warning: 'warning',
  attention: 'warning',
  question: 'warning',
  help: 'warning',
  faq: 'warning',
  caution: 'caution',
  danger: 'caution',
  error: 'caution',
  failure: 'caution',
  fail: 'caution',
  missing: 'caution',
  bug: 'caution',
  quote: 'quote',
  cite: 'quote',
  secondary: 'quote',
};

export function calloutStyle(type: string): CalloutStyle {
  return STYLE_OF[type.toLowerCase()] ?? 'note';
}

const defaultTitle = (type: string) => type[0]!.toUpperCase() + type.slice(1).toLowerCase();

type Fold = 'open' | 'closed' | null;

function openToken(token: Token, type: string, fold: Fold): void {
  token.tag = fold ? 'details' : 'div';
  token.attrJoin('class', `markdown-alert markdown-alert-${calloutStyle(type)}`);
  token.attrSet('data-callout', type.toLowerCase());
  if (fold === 'open') token.attrSet('open', '');
}

/** Title tokens; the inline token's children are filled in by the caller or the inline rule. */
function titleTokens(state: StateBlock | StateCore, fold: Fold, title: string): Token[] {
  const tag = fold ? 'summary' : 'p';
  const open = new state.Token('callout_title_open', tag, 1);
  open.attrSet('class', 'markdown-alert-title');
  open.block = true;
  const inline = new state.Token('inline', '', 0);
  inline.content = title;
  inline.children = [];
  const close = new state.Token('callout_title_close', tag, -1);
  close.block = true;
  return [open, inline, close];
}

// ---- GitHub alerts and Obsidian callouts --------------------------------------------------

const MARKER = /^\[!([A-Za-z][\w-]*)\]([+-]?)[ \t]*([^\n]*?)[ \t]*(?:\n|$)/;

function closeIndex(tokens: Token[], open: number): number {
  let depth = 0;
  for (let i = open; i < tokens.length; i++) {
    const t = tokens[i]!;
    if (t.type === 'blockquote_open') depth++;
    else if (t.type === 'blockquote_close' && --depth === 0) return i;
  }
  return -1;
}

function quoteCallouts(state: StateCore): void {
  const tokens = state.tokens;
  for (let i = 0; i < tokens.length; i++) {
    const open = tokens[i]!;
    if (open.type !== 'blockquote_open') continue;
    const para = tokens[i + 1];
    const inline = tokens[i + 2];
    if (para?.type !== 'paragraph_open' || inline?.type !== 'inline') continue;
    const match = MARKER.exec(inline.content);
    if (!match) continue;
    const close = closeIndex(tokens, i);
    if (close < 0) continue;
    const [marker, type, sign, written] = match as unknown as [string, string, string, string];
    const fold: Fold = sign === '+' ? 'open' : sign === '-' ? 'closed' : null;

    openToken(open, type, fold);
    tokens[close]!.tag = open.tag;

    // The title is parsed on its own, since the inline rule has already run.
    const title = titleTokens(state, fold, written || defaultTitle(type));
    state.md.inline.parse(title[1]!.content, state.md, state.env, title[1]!.children!);
    for (const child of title[1]!.children!) if (child.type === 'text_special') child.type = 'text';

    // Drop the marker line from the paragraph; drop the paragraph if nothing else is in it.
    const children = inline.children ?? [];
    const brk = children.findIndex((c) => c.type === 'softbreak' || c.type === 'hardbreak');
    const rest = brk < 0 ? [] : children.slice(brk + 1);
    if (rest.length === 0) {
      tokens.splice(i + 1, 3, ...title); // paragraph_open, inline, paragraph_close
    } else {
      inline.children = rest;
      inline.content = inline.content.slice(marker.length);
      tokens.splice(i + 1, 0, ...title);
    }
  }
}

// ---- MkDocs admonitions -------------------------------------------------------------------

// Extra words after the type are further CSS classes in MkDocs; they are accepted and ignored.
const MKDOCS = /^(!!!|\?\?\?\+?)[ \t]+([A-Za-z][\w-]*)(?:[ \t]+[A-Za-z][\w-]*)*(?:[ \t]+"([^"]*)")?[ \t]*$/;

const lineText = (state: StateBlock, n: number) => state.src.slice(state.bMarks[n]! + state.tShift[n]!, state.eMarks[n]!);

function pushBody(state: StateBlock, first: number, end: number, indent: number): void {
  const oldIndent = state.blkIndent;
  const oldMax = state.lineMax;
  state.blkIndent = indent;
  state.lineMax = end;
  state.md.block.tokenize(state, first, end);
  state.blkIndent = oldIndent;
  state.lineMax = oldMax;
}

function mkdocs(state: StateBlock, startLine: number, endLine: number, silent: boolean): boolean {
  if (state.sCount[startLine]! - state.blkIndent >= 4) return false;
  const match = MKDOCS.exec(lineText(state, startLine));
  if (!match) return false;
  if (silent) return true;
  const [, fence, type, written] = match as unknown as [string, string, string, string | undefined];
  const fold: Fold = fence === '!!!' ? null : fence === '???+' ? 'open' : 'closed';

  // The body is every following line indented four more than the marker, with blank lines
  // between; trailing blank lines belong to the document, not the admonition.
  const indent = state.sCount[startLine]! + 4;
  let last = startLine;
  for (let n = startLine + 1; n < endLine; n++) {
    if (state.isEmpty(n)) continue;
    if (state.sCount[n]! < indent) break;
    last = n;
  }

  const open = state.push('callout_open', '', 1);
  open.block = true;
  open.map = [startLine, last + 1];
  openToken(open, type, fold);
  // An empty quoted title ("") means no title bar, as in MkDocs.
  if (written !== '') state.tokens.push(...titleTokens(state, fold, written ?? defaultTitle(type)));
  pushBody(state, startLine + 1, last + 1, indent);
  state.push('callout_close', open.tag, -1).block = true;
  state.line = last + 1;
  return true;
}

// ---- Docusaurus admonitions ---------------------------------------------------------------

// Docusaurus' built-in keywords; any other name (a Pandoc fenced div, say) stays text.
const DOCUSAURUS = /^(:{3,})[ \t]*(note|tip|info|warning|danger|caution|important|success|secondary)(?:\[([^\]\n]*)\]|[ \t]+([^\n]*?))?[ \t]*$/i;

function docusaurus(state: StateBlock, startLine: number, endLine: number, silent: boolean): boolean {
  if (state.sCount[startLine]! - state.blkIndent >= 4) return false;
  const match = DOCUSAURUS.exec(lineText(state, startLine));
  if (!match) return false;
  if (silent) return true;
  const [, colons, type, bracketed, spaced] = match as unknown as [string, string, string, string?, string?];

  // The closing fence has at least as many colons; an unclosed admonition runs to the end of
  // its parent, as in remark-directive.
  const closeFence = new RegExp(`^:{${colons.length},}[ \\t]*$`);
  let close = -1;
  let n = startLine + 1;
  for (; n < endLine; n++) {
    if (!state.isEmpty(n) && state.sCount[n]! < state.blkIndent) break;
    if (state.sCount[n]! - state.blkIndent < 4 && closeFence.test(lineText(state, n))) {
      close = n;
      break;
    }
  }
  const end = close < 0 ? n : close;

  const open = state.push('callout_open', '', 1);
  open.block = true;
  open.map = [startLine, close < 0 ? end : close + 1];
  openToken(open, type, null);
  const title = bracketed ?? spaced;
  state.tokens.push(...titleTokens(state, null, title || defaultTitle(type)));
  pushBody(state, startLine + 1, end, state.blkIndent);
  state.push('callout_close', 'div', -1).block = true;
  state.line = close < 0 ? end : close + 1;
  return true;
}

export function calloutsPlugin(md: MarkdownIt): void {
  md.block.ruler.before('fence', 'callout_mkdocs', mkdocs, { alt: ['reference', 'blockquote', 'list'] });
  md.block.ruler.before('fence', 'callout_docusaurus', docusaurus, { alt: ['paragraph', 'reference', 'blockquote', 'list'] });
  md.core.ruler.after('text_join', 'callout_quotes', quoteCallouts);
}
