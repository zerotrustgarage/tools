import type { MarkdownIt, Token } from 'markdown-it';

// GitHub alerts: a blockquote whose first line is exactly [!NOTE], [!TIP], [!IMPORTANT],
// [!WARNING] or [!CAUTION] (case-insensitive). AI tools emit these constantly, and an editor
// that shows them as plain quotes with a literal "[!WARNING]" loses the document's intent.

export const ALERT_TYPES = ['note', 'tip', 'important', 'warning', 'caution'] as const;
const MARKER = /^\[!(note|tip|important|warning|caution)\][ \t]*(?:\n|$)/i;

function closeIndex(tokens: Token[], open: number): number {
  let depth = 0;
  for (let i = open; i < tokens.length; i++) {
    const t = tokens[i]!;
    if (t.type === 'blockquote_open') depth++;
    else if (t.type === 'blockquote_close' && --depth === 0) return i;
  }
  return -1;
}

export function alertsPlugin(md: MarkdownIt): void {
  md.core.ruler.after('text_join', 'github_alerts', (state) => {
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
      const kind = match[1]!.toLowerCase();

      open.tag = 'div';
      open.attrJoin('class', `markdown-alert markdown-alert-${kind}`);
      tokens[close]!.tag = 'div';

      // Drop the marker line from the paragraph; drop the paragraph if nothing else is in it.
      const children = inline.children ?? [];
      const brk = children.findIndex((c) => c.type === 'softbreak' || c.type === 'hardbreak');
      const rest = brk < 0 ? [] : children.slice(brk + 1);
      const title = new state.Token('html_block', '', 0);
      title.content = `<p class="markdown-alert-title">${kind[0]!.toUpperCase()}${kind.slice(1)}</p>\n`;
      if (rest.length === 0) {
        tokens.splice(i + 1, 3, title); // paragraph_open, inline, paragraph_close
      } else {
        inline.children = rest;
        inline.content = inline.content.slice(match[0].length);
        tokens.splice(i + 1, 0, title);
      }
    }
  });
}
