import DOMPurify from 'dompurify';

// The preview renders untrusted input: a Markdown file from an AI tool, an email or a download.
// MarkText's history (CVE-2021-29996, CVE-2022-24123, CVE-2022-25069, CVE-2023-2318) shows what
// happens when such HTML reaches a privileged renderer. Defence here is layered:
//
// 1. DOMPurify removes scripts, event handlers, javascript: URLs and other active content.
// 2. The hooks below narrow it further: inline styles keep only presentational properties and
//    never load anything, forms and their controls are removed (only display-only task list
//    checkboxes remain), and remote images become placeholders so opening a file cannot
//    announce itself to a tracking server.
// 3. The page's Content Security Policy (index.html, tauri.conf.json) forbids inline script and
//    remote loads even if something slipped past 1 and 2.
// 4. On the desktop, the WebView holds no file system or shell permission; it can only call the
//    app's narrow commands (src-tauri/src/lib.rs).

// Presentational CSS properties AI output actually uses (color spans, highlighted cells,
// aligned columns). Layout-escaping properties such as position, z-index and transform are
// left out so content cannot draw over the application's own controls.
const STYLE_PROPERTIES = new Set([
  'color',
  'background-color',
  'font-weight',
  'font-style',
  'font-size',
  'font-family',
  'font-variant',
  'text-decoration',
  'text-decoration-color',
  'text-decoration-line',
  'text-decoration-style',
  'text-align',
  'text-transform',
  'vertical-align',
  'white-space',
  'line-height',
  'letter-spacing',
  'border',
  'border-color',
  'border-style',
  'border-width',
  'border-radius',
  'border-top',
  'border-right',
  'border-bottom',
  'border-left',
  'border-collapse',
  'padding',
  'padding-top',
  'padding-right',
  'padding-bottom',
  'padding-left',
  'margin',
  'margin-top',
  'margin-right',
  'margin-bottom',
  'margin-left',
  'width',
  'max-width',
  'min-width',
  'height',
  'max-height',
  'min-height',
  'display',
  'opacity',
  'list-style-type',
]);
const DISPLAY_VALUES = new Set(['inline', 'block', 'inline-block', 'none', 'table', 'table-cell', 'table-row', 'list-item']);

/** Keeps only allow-listed declarations whose values cannot fetch, execute or escape. */
export function filterStyle(style: string): string {
  const kept: string[] = [];
  for (const declaration of style.split(';')) {
    const colon = declaration.indexOf(':');
    if (colon < 0) continue;
    const property = declaration.slice(0, colon).trim().toLowerCase();
    const value = declaration.slice(colon + 1).trim();
    if (!STYLE_PROPERTIES.has(property) || !value) continue;
    // A backslash could spell url( or expression( through CSS escapes, so it is refused
    // outright rather than decoded. Parentheses are allowed only for colour functions.
    if (/[\\<>{}@"]/.test(value)) continue;
    if (/\(/.test(value) && !/^(?:(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\([^()]*\)|[^()])*$/i.test(value))
      continue;
    if (property === 'display' && !DISPLAY_VALUES.has(value.toLowerCase())) continue;
    kept.push(`${property}: ${value}`);
  }
  return kept.join('; ');
}

const SAFE_LINK = /^(?:https?:|mailto:|#|[^:]*$)/i;
const REMOTE = /^(?:https?:)?\/\//i;

let configured = false;
function configure(purify: typeof DOMPurify): void {
  if (configured) return;
  configured = true;

  purify.addHook('afterSanitizeAttributes', (node) => {
    const el = node as Element;
    if (el.hasAttribute('style')) {
      const filtered = filterStyle(el.getAttribute('style') ?? '');
      if (filtered) el.setAttribute('style', filtered);
      else el.removeAttribute('style');
    }
    // data-line is the source-line marker the scroll sync reads (render.ts); only a plain number is kept.
    const line = el.getAttribute('data-line');
    if (line !== null && !/^\d{1,9}$/.test(line)) el.removeAttribute('data-line');
    const tag = el.tagName.toLowerCase();
    if (tag === 'input') {
      // Only the display-only checkbox of a task list item survives; any other control is
      // removed so a document cannot pose as a form.
      if (el.getAttribute('type') !== 'checkbox') {
        el.parentNode?.removeChild(el);
        return;
      }
      el.setAttribute('disabled', '');
      for (const attr of [...el.attributes])
        if (!['type', 'class', 'checked', 'disabled'].includes(attr.name)) el.removeAttribute(attr.name);
    }
    if (tag === 'a') {
      // Wiki links keep their target for the app to resolve.
      if (el.hasAttribute('data-wikilink')) el.setAttribute('href', '#');
      const href = el.getAttribute('href');
      if (href !== null && !SAFE_LINK.test(href.trim())) el.removeAttribute('href');
      el.removeAttribute('target');
      el.setAttribute('rel', 'noopener noreferrer');
    }
    if (tag === 'img') {
      el.removeAttribute('srcset');
      const src = el.getAttribute('src')?.trim() ?? '';
      if (REMOTE.test(src)) {
        // Remote images are never fetched; the placeholder offers the address instead.
        el.removeAttribute('src');
        el.setAttribute('data-remote-src', src);
        el.classList.add('remote-image');
      } else if (src && !/^(?:data:image\/|blob:)/i.test(src)) {
        // A path relative to the document; the platform resolves it (postprocess.ts).
        el.removeAttribute('src');
        el.setAttribute('data-local-src', src);
      }
    }
    // SVG can reference external resources through href/xlink:href on image and use.
    if (tag === 'image' || tag === 'use' || tag === 'feimage') {
      for (const name of ['href', 'xlink:href']) {
        const value = el.getAttribute(name);
        if (value !== null && !value.startsWith('#')) el.removeAttribute(name);
      }
    }
  });
}

export function sanitize(html: string): string {
  configure(DOMPurify);
  return DOMPurify.sanitize(html, {
    USE_PROFILES: { html: true, svg: true, svgFilters: true },
    ADD_ATTR: ['checked', 'disabled'],
    // <source> inside <picture> would load its srcset; the <img> fallback is kept instead.
    FORBID_TAGS: ['style', 'source', 'form', 'button', 'textarea', 'select', 'option', 'iframe', 'object', 'embed', 'link', 'meta', 'base'],
    FORBID_ATTR: ['action', 'formaction', 'background', 'ping'],
  });
}

/** Sanitizes Mermaid's SVG output again before it is inserted (defence in depth). */
export function sanitizeSvg(svg: string): string {
  configure(DOMPurify);
  return DOMPurify.sanitize(svg, {
    USE_PROFILES: { svg: true, svgFilters: true },
    ADD_TAGS: ['style'],
  });
}
