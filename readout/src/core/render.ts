import markdownit, { type MarkdownIt } from 'markdown-it';
import footnote from 'markdown-it-footnote';
import hljs from 'highlight.js/lib/common';
import apache from 'highlight.js/lib/languages/apache';
import dockerfile from 'highlight.js/lib/languages/dockerfile';
import dos from 'highlight.js/lib/languages/dos';
import nginx from 'highlight.js/lib/languages/nginx';
import powershell from 'highlight.js/lib/languages/powershell';
import { alertsPlugin } from './plugins/alerts';
import { frontMatterPlugin } from './plugins/frontmatter';
import { mathPlaceholder, mathPlugin } from './plugins/math';
import { tasksPlugin } from './plugins/tasks';
import { wikilinksPlugin } from './plugins/wikilinks';
import { sanitize } from './sanitize';

// Markdown to sanitized HTML. The parser follows CommonMark plus the GitHub extensions AI tools
// rely on (tables, task lists, strikethrough, autolinks, footnotes, alerts, math, Mermaid),
// plus what docs folders and vaults add (YAML front matter, [[wikilinks]]),
// with raw HTML allowed so inline colors survive, then sanitized (sanitize.ts). Rendering is a
// view of the source: nothing here ever writes back to the document.

const { escapeHtml } = markdownit().utils;

// Beyond highlight.js's common set: the languages MSP and infrastructure documents use most.
hljs.registerLanguage('powershell', powershell);
hljs.registerLanguage('dos', dos);
hljs.registerLanguage('dockerfile', dockerfile);
hljs.registerLanguage('nginx', nginx);
hljs.registerLanguage('apache', apache);

function highlight(code: string, lang: string): string {
  const name = lang.trim().split(/\s+/)[0]?.toLowerCase() ?? '';
  if (name && hljs.getLanguage(name)) {
    try {
      return hljs.highlight(code, { language: name, ignoreIllegals: true }).value;
    } catch {
      // fall through to plain text
    }
  }
  return escapeHtml(code);
}

/** GitHub-style heading slugs, deduplicated within one document. */
export function slugger(): (text: string) => string {
  const seen = new Map<string, number>();
  return (text) => {
    const base =
      text
        .toLowerCase()
        .trim()
        .replace(/[^\p{L}\p{N}\s_-]/gu, '')
        .replace(/\s/g, '-') || 'section';
    const count = seen.get(base) ?? 0;
    seen.set(base, count + 1);
    return count === 0 ? base : `${base}-${count}`;
  };
}

// Block tokens that get data-line, so the preview can follow the editor's scroll position.
const LINE_MAPPED = new Set([
  'paragraph_open',
  'heading_open',
  'blockquote_open',
  'bullet_list_open',
  'ordered_list_open',
  'list_item_open',
  'table_open',
  'tr_open',
  'hr',
]);

function createParser(): MarkdownIt {
  const md = markdownit('default', {
    html: true,
    linkify: true,
    typographer: false,
    breaks: false,
  });
  md.use(frontMatterPlugin).use(footnote).use(tasksPlugin).use(alertsPlugin).use(mathPlugin).use(wikilinksPlugin);

  md.core.ruler.push('ztg_annotations', (state) => {
    const slug = slugger();
    const tokens = state.tokens;
    for (let i = 0; i < tokens.length; i++) {
      const t = tokens[i]!;
      if (t.map && LINE_MAPPED.has(t.type)) t.attrSet('data-line', String(t.map[0]));
      if (t.type === 'heading_open') {
        const text = (tokens[i + 1]?.children ?? [])
          .filter((c) => c.type === 'text' || c.type === 'code_inline')
          .map((c) => c.content)
          .join('');
        t.attrSet('id', slug(text));
      }
    }
  });

  md.renderer.rules.fence = (tokens, idx) => {
    const token = tokens[idx]!;
    const info = token.info.trim();
    const lang = info.split(/\s+/)[0]?.toLowerCase() ?? '';
    const line = token.map ? ` data-line="${token.map[0]}"` : '';
    if (lang === 'mermaid')
      return `<pre class="mermaid-source"${line} data-mermaid="">${escapeHtml(token.content)}</pre>\n`;
    if (lang === 'math') return mathPlaceholder(token.content, true, escapeHtml, token.map?.[0]);
    const cls = lang ? ` class="hljs language-${escapeHtml(lang)}"` : ' class="hljs"';
    return `<pre${line}><code${cls}>${highlight(token.content, lang)}</code></pre>\n`;
  };
  md.renderer.rules.code_block = (tokens, idx) => {
    const token = tokens[idx]!;
    const line = token.map ? ` data-line="${token.map[0]}"` : '';
    return `<pre${line}><code class="hljs">${escapeHtml(token.content)}</code></pre>\n`;
  };
  return md;
}

let parser: MarkdownIt | undefined;

/** Unsanitized HTML; only for tests of the parser itself. */
export function renderUnsafe(source: string): string {
  parser ??= createParser();
  return parser.render(source);
}

/** Sanitized HTML for the preview and for export. Requires a DOM (browser, WebView or jsdom). */
export function renderMarkdown(source: string): string {
  return sanitize(renderUnsafe(source));
}
