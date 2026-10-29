import designTokens from '../../../design-tokens.json';
import { sanitize } from './sanitize';

// Copy as rich text: the rendered preview, or a selection in it, as HTML that keeps its tables,
// colours, code blocks and callouts when pasted into Outlook, Teams or Word, plus a plain text
// copy for everything else.
//
// Why a separate pass and not the preview's own markup: the preview is styled by a stylesheet
// with custom properties and color-mix(), which a mail or word processor does not have. Pasted
// HTML has to carry its styles inline, with plain values, and the Word engine behind Outlook
// ignores several properties on div and pre, so callouts and code blocks are single-cell tables
// (borders, shading and padding survive in a table cell). Colours come from the light ZTG
// palette in design-tokens.json, whatever theme the preview shows, because the destination is
// a white page.
//
// Safety, in the same layers as the preview (sanitize.ts):
// - the content is sanitized before it is touched and again after it is rebuilt, so nothing this
//   module adds can introduce script, an event handler or a style outside the allow-list;
// - the content is only ever parsed in an inert document (no browsing context), where nothing
//   loads and nothing runs;
// - nothing is fetched: a remote image becomes a text note without its address, and a local
//   image is kept only as a data URL (a blob URL is read locally by the caller's resolver);
// - attributes are reduced to an allow-list, so ids, classes and data-* never reach the clipboard.

const T = designTokens.tokens;
const COLOR = {
  text: T['light-text'],
  bright: T['light-bright'],
  dim: T['light-dim'],
  dim2: T['light-dim2'],
  line: T['light-line'],
  lineStrong: T['light-line-strong'],
  panel: T['light-panel'],
  panel2: T['light-panel2'],
  page: T['light-bg'],
  info: T['light-info'],
  ok: T['light-ok'],
  warn: T['light-warn'],
  crit: T['light-crit'],
  special: T['light-special'],
};

const SANS = "'Segoe UI', Calibri, Arial, sans-serif";
const MONO = "Consolas, 'Courier New', monospace";

/** hex blended toward white by (1 - amount): a pale tint of the colour that a mail client can show. */
export function tint(hex: string, amount: number): string {
  const n = Number.parseInt(hex.slice(1), 16);
  const channel = (shift: number) => {
    const c = (n >> shift) & 255;
    return Math.round(255 - (255 - c) * amount)
      .toString(16)
      .padStart(2, '0');
  };
  return `#${channel(16)}${channel(8)}${channel(0)}`;
}

const CALLOUT_COLOR: Record<string, string> = {
  note: COLOR.info,
  tip: COLOR.ok,
  important: COLOR.special,
  warning: COLOR.warn,
  caution: COLOR.crit,
  quote: COLOR.dim,
};

// highlight.js classes to palette colours (the preview uses the same groups, preview.css).
const HLJS: [RegExp, string][] = [
  [/\bhljs-(?:keyword|selector-tag|literal|doctag)\b/, COLOR.special],
  [/\bhljs-(?:string|regexp|addition)\b/, COLOR.ok],
  [/\bhljs-(?:number|symbol|bullet|variable|template-variable)\b/, COLOR.warn],
  [/\bhljs-(?:comment|quote|meta)\b/, COLOR.dim2],
  [/\bhljs-(?:title|section)\b/, COLOR.info],
  [/\bhljs-(?:type|built_in|deletion|name)\b/, COLOR.crit],
  [/\bhljs-(?:attr|attribute|property|selector-class|selector-id)\b/, COLOR.special],
];

const HEADING_SIZE: Record<string, number> = { h1: 24, h2: 20, h3: 17, h4: 15, h5: 14, h6: 13 };

/** Declarations every element of this kind gets; the author's own inline style is added after, so it wins. */
function baseStyle(tag: string): string {
  const heading = HEADING_SIZE[tag];
  if (heading !== undefined)
    return `font-family: ${SANS}; font-size: ${heading}px; font-weight: 600; line-height: 1.25; color: ${tag === 'h6' ? COLOR.dim : COLOR.bright}; margin: 18px 0 8px`;
  switch (tag) {
    case 'p':
      return `margin: 0 0 10px`;
    case 'ul':
    case 'ol':
      return `margin: 0 0 10px; padding-left: 28px`;
    case 'li':
      return `margin: 0 0 3px`;
    case 'blockquote':
      return `margin: 0 0 10px; padding: 0 14px; color: ${COLOR.dim}; border-left: 4px solid ${COLOR.lineStrong}`;
    case 'hr':
      return `border: 0; border-top: 2px solid ${COLOR.line}; margin: 16px 0`;
    case 'a':
      return `color: ${COLOR.info}; text-decoration: underline`;
    case 'table':
      return `border-collapse: collapse; border: 1px solid ${COLOR.lineStrong}; margin: 0 0 12px`;
    case 'th':
      return `border: 1px solid ${COLOR.lineStrong}; padding: 6px 12px; background-color: ${COLOR.panel2}; color: ${COLOR.bright}; font-weight: 600; text-align: left; vertical-align: top`;
    case 'td':
      return `border: 1px solid ${COLOR.lineStrong}; padding: 6px 12px; vertical-align: top`;
    case 'code':
      return `font-family: ${MONO}; font-size: 13px; background-color: ${COLOR.panel2}; padding: 1px 4px`;
    case 'kbd':
      return `font-family: ${MONO}; font-size: 12px; border: 1px solid ${COLOR.lineStrong}; background-color: ${COLOR.page}; padding: 1px 4px`;
    case 'mark':
      return `background-color: ${tint(COLOR.warn, 0.25)}`;
    case 'img':
      return `max-width: 100%; height: auto`;
    default:
      return '';
  }
}

const ATTRIBUTES = new Set([
  'style', 'href', 'src', 'alt', 'title', 'colspan', 'rowspan', 'align', 'valign', 'bgcolor', 'border',
  'cellpadding', 'cellspacing', 'width', 'height', 'start',
]);
const SAFE_IMAGE = /^data:image\/(?:png|jpe?g|gif|webp|bmp);/i;
const SAFE_HREF = /^(?:https?:|mailto:)/i;
const MAX_LOCAL_IMAGE_BYTES = 2 * 1024 * 1024;

export interface RichTextOptions {
  /**
   * Turns a blob: image address (a local image the preview resolved) into a data URL, or null
   * when it cannot be read. Without it, local images are described in text instead.
   */
  resolveImage?: (src: string) => Promise<string | null>;
}

export interface RichText {
  html: string;
  text: string;
}

/** True when the preview content under boundary has nothing a clipboard could carry. */
export function isEmptyFragment(content: DocumentFragment): boolean {
  return (content.textContent ?? '').trim() === '' && content.querySelector('img, table, hr') === null;
}

// ---- selection ---------------------------------------------------------------------------------

// Wrappers kept around a partial selection, so a selection inside a table cell is still a table
// and one inside a code block is still code. Paragraphs and headings are not wrapped: copying part
// of a sentence should paste as text, not as a new paragraph.
const KEEP_AROUND = new Set([
  'table', 'thead', 'tbody', 'tfoot', 'tr', 'td', 'th', 'ul', 'ol', 'li', 'blockquote', 'pre', 'details',
  'code', 'kbd', 'mark', 'a', 'strong', 'b', 'em', 'i', 'del', 's', 'sub', 'sup', 'span',
]);

/**
 * A copy of what range covers, wrapped in the structure it sits in (up to, not including,
 * boundary). The preview is not modified.
 */
export function fragmentForRange(range: Range, boundary: Element): DocumentFragment {
  let fragment = range.cloneContents();
  let node: Node | null = range.commonAncestorContainer;
  if (node.nodeType !== 1) node = node.parentNode;
  for (let current = node as Element | null; current && current !== boundary; current = current.parentElement) {
    const keep = KEEP_AROUND.has(current.tagName.toLowerCase()) || current.classList.contains('markdown-alert');
    if (!keep) continue;
    const shell = current.cloneNode(false) as Element;
    shell.append(fragment);
    fragment = range.commonAncestorContainer.ownerDocument!.createDocumentFragment();
    fragment.append(shell);
  }
  return fragment;
}

// ---- building ----------------------------------------------------------------------------------

type Doc = Document;

const el = (doc: Doc, tag: string, style?: string, ...children: (Node | string)[]): HTMLElement => {
  const node = doc.createElement(tag);
  if (style) node.setAttribute('style', style);
  node.append(...children);
  return node;
};

const isElement = (node: Node): node is HTMLElement => node.nodeType === 1;
const tagOf = (node: Element) => node.tagName.toLowerCase();

function noteSpan(doc: Doc, text: string): HTMLElement {
  return el(doc, 'span', `color: ${COLOR.dim}; font-style: italic`, text);
}

/** Replaces images and other content that cannot be carried: nothing is kept that could be fetched. */
async function settleImages(root: Element, doc: Doc, options: RichTextOptions): Promise<void> {
  for (const placeholder of [...root.querySelectorAll('a.remote-image-placeholder')])
    placeholder.replaceWith(noteSpan(doc, '[Remote image not copied]'));
  for (const img of [...root.querySelectorAll('img')]) {
    const alt = img.getAttribute('alt')?.trim() ?? '';
    const src = img.getAttribute('src')?.trim() ?? '';
    const blob = img.getAttribute('data-blob-src') ?? '';
    const remote = img.hasAttribute('data-remote-src');
    if (!remote && SAFE_IMAGE.test(src)) continue;
    if (!remote && blob && options.resolveImage) {
      const data = await options.resolveImage(blob).catch(() => null);
      if (data && SAFE_IMAGE.test(data) && data.length <= MAX_LOCAL_IMAGE_BYTES * 1.4) {
        img.setAttribute('src', data);
        continue;
      }
    }
    img.replaceWith(noteSpan(doc, remote ? `[Remote image not copied${alt ? `: ${alt}` : ''}]` : `[Image not copied${alt ? `: ${alt}` : ''}]`));
  }
}

/** Everything that is not plain markup becomes plain markup: math as its TeX, diagrams and controls as text. */
function simplify(root: Element, doc: Doc): void {
  for (const math of [...root.querySelectorAll<HTMLElement>('.math[data-tex]')]) {
    const tex = math.getAttribute('data-tex') ?? '';
    if (math.classList.contains('math-display'))
      math.replaceWith(el(doc, 'p', `margin: 0 0 10px; font-family: ${MONO}; font-size: 13px`, `$$${tex}$$`));
    else math.replaceWith(el(doc, 'code', undefined, `$${tex}$`));
  }
  for (const figure of [...root.querySelectorAll('figure.mermaid-diagram')])
    figure.replaceWith(el(doc, 'p', `margin: 0 0 10px; color: ${COLOR.dim}; font-style: italic`, '[Diagram not copied. Diagrams do not paste as rich text.]'));
  for (const svg of [...root.querySelectorAll('svg')]) svg.remove();

  for (const box of [...root.querySelectorAll<HTMLInputElement>('input')]) {
    const done = box.hasAttribute('checked');
    // The list item's own text already starts with a space.
    const spaced = box.nextSibling?.nodeType === 3 && /^\s/.test(box.nextSibling.textContent ?? '');
    box.replaceWith(doc.createTextNode((done ? '☑' : '☐') + (spaced ? '' : ' ')));
  }
  for (const back of [...root.querySelectorAll('a.footnote-backref')]) back.remove();

  // A link is kept only when it points somewhere a reader can follow after pasting.
  for (const a of [...root.querySelectorAll('a')]) {
    const href = a.getAttribute('href')?.trim() ?? '';
    if (!SAFE_HREF.test(href)) a.replaceWith(...a.childNodes);
  }

  // Folds have no meaning on paper: the title becomes a line and the body is always included.
  for (const details of [...root.querySelectorAll('details:not(.markdown-alert)')]) {
    const div = el(doc, 'div');
    for (const child of [...details.childNodes]) {
      if (isElement(child) && tagOf(child) === 'summary') div.append(el(doc, 'p', undefined, el(doc, 'strong', undefined, ...child.childNodes)));
      else div.append(child);
    }
    details.replaceWith(div);
  }
}

/** Declarations from each style in turn; a property set again later replaces the earlier value. */
function mergeStyles(...styles: string[]): string {
  const declarations = new Map<string, string>();
  for (const style of styles)
    for (const declaration of style.split(';')) {
      const colon = declaration.indexOf(':');
      if (colon < 0) continue;
      const property = declaration.slice(0, colon).trim().toLowerCase();
      declarations.delete(property);
      declarations.set(property, declaration.slice(colon + 1).trim());
    }
  return [...declarations].map(([property, value]) => `${property}: ${value}`).join('; ');
}

function tableBox(doc: Doc, cellStyle: string, bg: string, ...children: Node[]): HTMLElement {
  const td = el(doc, 'td', `${cellStyle}; background-color: ${bg}`, ...children);
  td.setAttribute('bgcolor', bg);
  const table = el(doc, 'table', 'border-collapse: collapse; width: 100%; margin: 0 0 12px', el(doc, 'tbody', undefined, el(doc, 'tr', undefined, td)));
  table.setAttribute('width', '100%');
  table.setAttribute('cellpadding', '0');
  table.setAttribute('cellspacing', '0');
  return table;
}

function calloutKey(node: Element): string {
  for (const name of node.classList) {
    const m = /^markdown-alert-(note|tip|important|warning|caution|quote)$/.exec(name);
    if (m) return m[1]!;
  }
  return 'note';
}

/** Adds the inline styles and rebuilds callouts and code blocks as tables. Works children first. */
function style(node: Element, doc: Doc): void {
  for (const child of [...node.children]) {
    const tag = tagOf(child);

    if (child.classList.contains('markdown-alert')) {
      const color = CALLOUT_COLOR[calloutKey(child)] ?? COLOR.info;
      const body: Node[] = [];
      for (const part of [...child.childNodes]) {
        if (isElement(part) && part.classList.contains('markdown-alert-title'))
          body.push(el(doc, 'p', `margin: 0 0 4px; font-weight: 600; color: ${color}`, ...part.childNodes));
        else body.push(part);
      }
      const box = tableBox(doc, `border-left: 4px solid ${color}; padding: 8px 16px; color: ${COLOR.text}`, tint(color, 0.1), ...body);
      child.replaceWith(box);
      // The table is built here with its own styles; only what it holds gets the usual ones.
      style(box.querySelector('td')!, doc);
      continue;
    }

    if (tag === 'pre') {
      const code = child.querySelector('code');
      if (code) code.removeAttribute('style');
      child.setAttribute('style', `margin: 0; font-family: ${MONO}; font-size: 13px; line-height: 1.45; white-space: pre-wrap; color: ${COLOR.text}`);
      const box = tableBox(doc, `border: 1px solid ${COLOR.line}; padding: 10px 14px`, COLOR.panel2);
      child.replaceWith(box);
      box.querySelector('td')!.append(child);
      colorCode(child);
      continue;
    }

    // A task list shows its own check boxes, so it gets no bullets.
    const taskList = child.classList.contains('contains-task-list') ? 'list-style-type: none; padding-left: 4px' : '';
    const merged = mergeStyles(baseStyle(tag), taskList, child.getAttribute('style') ?? '');
    if (merged) child.setAttribute('style', merged);
    style(child, doc);
  }
}

function colorCode(pre: Element): void {
  for (const span of pre.querySelectorAll('span[class]')) {
    const classes = span.getAttribute('class') ?? '';
    const rule = HLJS.find(([pattern]) => pattern.test(classes));
    const extra = /\bhljs-(?:comment|quote|meta)\b|\bhljs-emphasis\b/.test(classes) ? '; font-style: italic' : /\bhljs-strong\b/.test(classes) ? '; font-weight: 600' : '';
    if (rule || extra) span.setAttribute('style', `${rule ? `color: ${rule[1]}` : ''}${extra}`.replace(/^; /, ''));
  }
}

function scrub(root: Element): void {
  for (const node of [root, ...root.querySelectorAll('*')]) {
    for (const attr of [...node.attributes]) if (!ATTRIBUTES.has(attr.name)) node.removeAttribute(attr.name);
  }
}

// ---- plain text --------------------------------------------------------------------------------

const BLOCK = new Set([
  'address', 'article', 'aside', 'blockquote', 'details', 'div', 'dl', 'figure', 'footer', 'h1', 'h2', 'h3', 'h4',
  'h5', 'h6', 'header', 'hr', 'ol', 'p', 'pre', 'section', 'summary', 'table', 'ul',
]);

const collapse = (s: string) => s.replace(/\s+/g, ' ').trim();

function inlineText(node: Node): string {
  if (node.nodeType === 3) return node.textContent ?? '';
  if (!isElement(node)) return '';
  const tag = tagOf(node);
  if (tag === 'br') return '\n';
  if (tag === 'img') return node.getAttribute('alt') ?? '';
  let out = '';
  for (const child of node.childNodes) out += inlineText(child);
  if (tag === 'a') {
    const href = node.getAttribute('href') ?? '';
    const label = collapse(out);
    if (SAFE_HREF.test(href) && label !== '' && label !== href && href !== `mailto:${label}`) return `${label} (${href})`;
  }
  return out;
}

function listText(list: Element, indent: string): string {
  const ordered = tagOf(list) === 'ol';
  let n = Number(list.getAttribute('start') ?? '1');
  if (!Number.isFinite(n)) n = 1;
  const lines: string[] = [];
  for (const item of list.children) {
    if (tagOf(item) !== 'li') continue;
    const marker = ordered ? `${n++}. ` : '- ';
    const body = blocksText(item, indent + ' '.repeat(marker.length), true).split('\n');
    lines.push(indent + marker + (body[0] ?? '').trimStart(), ...body.slice(1));
  }
  return lines.join('\n');
}

function tableText(table: Element): string {
  const rows: string[] = [];
  for (const row of table.querySelectorAll('tr')) {
    const cells = [...row.children].filter((c) => /^t[dh]$/.test(tagOf(c))).map((c) => collapse(inlineText(c)));
    rows.push(cells.join('\t'));
  }
  return rows.join('\n');
}

/** The children of node as text: blocks separated by a blank line (or one line break inside a list item). */
function blocksText(node: Node, indent = '', tight = false): string {
  const parts: string[] = [];
  let run = '';
  const flush = () => {
    const text = run.replace(/[ \t]*\n[ \t]*/g, '\n').replace(/[ \t]+/g, ' ').trim();
    if (text) parts.push(indent && parts.length > 0 ? indent + text : text);
    run = '';
  };
  for (const child of node.childNodes) {
    if (!isElement(child) || !BLOCK.has(tagOf(child))) {
      run += inlineText(child);
      continue;
    }
    flush();
    const tag = tagOf(child);
    let text: string;
    if (tag === 'ul' || tag === 'ol') text = listText(child, indent);
    else if (tag === 'table') text = tableText(child);
    else if (tag === 'pre') text = (child.textContent ?? '').replace(/\n+$/, '');
    else if (tag === 'hr') text = '---';
    else if (tag === 'blockquote') text = blocksText(child).split('\n').map((l) => `> ${l}`.trimEnd()).join('\n');
    else text = blocksText(child, '', tight);
    if (text) parts.push(text);
  }
  flush();
  return parts.join(tight ? '\n' : '\n\n');
}

// ---- entry point -------------------------------------------------------------------------------

/**
 * The clipboard payload for content taken from the rendered preview (a fragment from
 * fragmentForRange, or the preview's own children). The content is read, never modified.
 */
export async function buildRichText(content: DocumentFragment, options: RichTextOptions = {}): Promise<RichText> {
  // An inert document: no browsing context, so parsing here loads and runs nothing.
  const inert = document.implementation.createHTMLDocument('');
  const holder = inert.createElement('div');
  holder.append(inert.importNode(content, true));
  // The sanitizer does not accept blob: addresses (the preview sets them after sanitizing), so a
  // local image's address is set aside as data and settled by settleImages. It is never kept as src.
  for (const img of holder.querySelectorAll('img[src^="blob:"]')) {
    img.setAttribute('data-blob-src', img.getAttribute('src') ?? '');
    img.removeAttribute('src');
  }

  const clean = sanitize(holder.innerHTML);
  const body = new DOMParser().parseFromString(`<!doctype html><body>${clean}`, 'text/html').body;
  const doc = body.ownerDocument;

  await settleImages(body, doc, options);
  simplify(body, doc);
  const text = blocksText(body);

  style(body, doc);
  const wrapper = el(doc, 'div', `font-family: ${SANS}; font-size: 14px; line-height: 1.5; color: ${COLOR.text}`, ...body.childNodes);
  scrub(wrapper);
  // The rebuilt markup goes through the same sanitizer once more: whatever this module added is
  // held to the preview's rules too.
  return { html: sanitize(wrapper.outerHTML), text };
}

/**
 * Reads a blob: address the preview made for a local image and returns it as a data URL, or null
 * when it is too large or unreadable. Only blob: addresses are read (they are local to this
 * page); anything else is refused, so this can never reach the network.
 */
export async function blobUrlToDataUrl(url: string): Promise<string | null> {
  if (!/^blob:/i.test(url)) return null;
  try {
    const blob = await (await fetch(url)).blob();
    if (blob.size > MAX_LOCAL_IMAGE_BYTES) return null;
    return await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(blob);
    });
  } catch {
    return null;
  }
}
