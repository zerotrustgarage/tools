import type { MarkdownIt, StateInline } from 'markdown-it';

// Obsidian and wiki-style links: [[Page]], [[Page|label]], [[Page#Heading]], and embeds
// ![[image.png]]. A link renders as an anchor carrying data-wikilink; the app resolves the name
// against the open folder when it is clicked (core/paths.ts, resolveWikiLink). An image embed
// becomes an image with a relative source. Note embeds (![[Page]]) render as links.

const IMAGE = /\.(png|jpe?g|gif|webp|svg|bmp|avif)$/i;

function wikilink(state: StateInline, silent: boolean): boolean {
  const src = state.src;
  let pos = state.pos;
  const embed = src.charCodeAt(pos) === 0x21; /* ! */
  if (embed) pos++;
  if (src.slice(pos, pos + 2) !== '[[') return false;
  const end = src.indexOf(']]', pos + 2);
  if (end < 0) return false;
  const inner = src.slice(pos + 2, end);
  if (!inner.trim() || inner.includes('\n') || inner.includes('[')) return false;
  if (!silent) {
    const [target = '', label] = inner.split('|');
    if (embed && IMAGE.test(target.trim())) {
      const token = state.push('wikilink_image', 'img', 0);
      token.attrSet('src', target.trim());
      token.attrSet('alt', (label ?? target).trim());
    } else {
      const token = state.push('wikilink', 'a', 0);
      token.attrSet('href', '#');
      token.attrSet('data-wikilink', target.trim());
      token.attrSet('class', 'wikilink');
      token.content = (label ?? target.split('#')[0] ?? target).trim() || target.trim();
    }
  }
  state.pos = end + 2;
  return true;
}

export function wikilinksPlugin(md: MarkdownIt): void {
  md.inline.ruler.before('link', 'wikilink', wikilink);
  const escape = md.utils.escapeHtml;
  md.renderer.rules.wikilink = (tokens, idx) => {
    const t = tokens[idx]!;
    return `<a href="#" class="wikilink" data-wikilink="${escape(String(t.attrGet('data-wikilink') ?? ''))}">${escape(t.content)}</a>`;
  };
  md.renderer.rules.wikilink_image = (tokens, idx) => {
    const t = tokens[idx]!;
    return `<img src="${escape(encodeURI(String(t.attrGet('src') ?? '')))}" alt="${escape(String(t.attrGet('alt') ?? ''))}">`;
  };
}
