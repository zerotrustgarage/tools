import type { MarkdownIt, StateBlock } from 'markdown-it';

// YAML front matter (Jekyll, Hugo, Docusaurus, MkDocs, Obsidian). Without this a docs folder's
// pages open with a stray rule and a heading made of their metadata. GitHub shows front matter
// as a table; so does this, keeping simple "key: value" pairs as columns and anything more
// complex (lists, nested maps) as text. The YAML is displayed, never evaluated.

const FENCE = /^---[ \t]*$/;
const CLOSE = /^(?:---|\.\.\.)[ \t]*$/;
const PAIR = /^([A-Za-z0-9_][\w .-]*?)[ \t]*:[ \t]*(.*)$/;

function frontMatter(state: StateBlock, startLine: number, endLine: number, silent: boolean): boolean {
  if (startLine !== 0 || state.blkIndent !== 0) return false;
  const line = (n: number) => state.src.slice(state.bMarks[n]! + state.tShift[n]!, state.eMarks[n]!);
  if (state.tShift[0] !== 0 || !FENCE.test(line(0))) return false;
  let close = -1;
  for (let n = 1; n < endLine; n++) {
    if (state.tShift[n] === 0 && CLOSE.test(line(n))) {
      close = n;
      break;
    }
  }
  if (close < 1) return false;
  if (silent) return true;
  const token = state.push('front_matter', '', 0);
  token.block = true;
  token.content = state.getLines(1, close, 0, false).replace(/\r?\n$/, '');
  token.map = [0, close + 1];
  state.line = close + 1;
  return true;
}

export interface FrontMatterEntry {
  key: string;
  value: string;
}

/** Top-level keys with their raw values; indented continuation lines join the previous key. */
export function parseFrontMatter(yaml: string): FrontMatterEntry[] {
  const entries: FrontMatterEntry[] = [];
  for (const raw of yaml.split(/\r?\n/)) {
    if (!raw.trim() || raw.trimStart().startsWith('#')) continue;
    const pair = /^\s/.test(raw) || raw.startsWith('- ') ? null : PAIR.exec(raw);
    if (pair) entries.push({ key: pair[1]!, value: pair[2]!.trim() });
    else if (entries.length > 0) entries.at(-1)!.value += (entries.at(-1)!.value ? '\n' : '') + raw.trim();
    else entries.push({ key: '', value: raw.trim() });
  }
  for (const e of entries) e.value = e.value.replace(/^(['"])(.*)\1$/s, '$2');
  return entries;
}

export function frontMatterPlugin(md: MarkdownIt): void {
  md.block.ruler.before('hr', 'front_matter', frontMatter);
  const escape = md.utils.escapeHtml;
  md.renderer.rules.front_matter = (tokens, idx) => {
    const entries = parseFrontMatter(tokens[idx]!.content);
    if (entries.length === 0) return '';
    const head = entries.map((e) => `<th>${escape(e.key)}</th>`).join('');
    const body = entries.map((e) => `<td>${escape(e.value).replace(/\n/g, '<br>')}</td>`).join('');
    return `<table class="front-matter" data-line="0"><thead><tr>${head}</tr></thead><tbody><tr>${body}</tr></tbody></table>\n`;
  };
}
