// The heading outline of a Markdown document. Extraction is a plain line scan, so it needs no DOM
// and runs on every edit; it follows the same rules as the preview's parser for the cases that
// matter: ATX and setext headings, none inside fenced code, indented code, HTML comments or the
// front matter. A heading inside a blockquote or a list item is not listed. Headings are
// identified by their line, never by their text, so duplicates stay distinct.

export interface OutlineHeading {
  /** 1 to 6. */
  level: number;
  /** The heading as plain text: inline Markdown markers are removed. */
  text: string;
  /** Zero-based line of the heading (its first line, for a multi-line setext heading). */
  line: number;
}

const ATX = /^ {0,3}(#{1,6})(?:[ \t]+(.*))?$/;
const SETEXT = /^ {0,3}(=+|-+)[ \t]*$/;
const FENCE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
const NOT_PARAGRAPH = /^ {0,3}(?:[-*+][ \t]|\d{1,9}[.)][ \t]|>|\|)|^ {4,}|^\s*$/;
const HTML_BLOCK_NAMES =
  'address|article|aside|blockquote|body|center|details|dd|div|dl|dt|fieldset|figcaption|figure|footer|form|h[1-6]|header|hr|html|iframe|legend|li|main|menu|nav|ol|optgroup|option|p|section|summary|table|tbody|td|tfoot|th|thead|title|tr|ul';
const HTML_BLOCK = new RegExp(`^ {0,3}</?(?:${HTML_BLOCK_NAMES})(?:[ \\t]|/?>|$)`, 'i');
const HTML_RAW = /^ {0,3}<(pre|script|style|textarea)(?:[ \t>]|$)/i;
const HTML_TAG_ALONE = /^ {0,3}(?:<[A-Za-z][A-Za-z0-9-]*(?:\s[^>]*)?\/?>|<\/[A-Za-z][A-Za-z0-9-]*\s*>)[ \t]*$/;
const THEMATIC = /^ {0,3}(?:(?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$/;

/** Inline Markdown reduced to the words a reader sees. */
export function plainHeadingText(source: string): string {
  // Escaped punctuation is parked in the private use area so the marker rules skip it.
  return source
    .replace(/\\([!-/:-@[-`{-~])/g, (_m, c: string) => String.fromCharCode(0xe000 + c.charCodeAt(0)))
    .replace(/[ \t]+#+[ \t]*$/, '')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[\[([^\]|#]*)(?:#[^\]|]*)?(?:\|([^\]]*))?\]\]/g, (_m, target: string, label?: string) => label || target)
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]*)\]\[[^\]]*\]/g, '$1')
    .replace(/<([A-Za-z][A-Za-z0-9+.-]{1,31}:[^\s<>]*)>/g, '$1')
    .replace(/<\/?[A-Za-z][^>]*>/g, '')
    .replace(/(`+)([^`]+?)\1/g, '$2')
    .replace(/(\*\*|__|~~|\*|(?<![\p{L}\p{N}])_|_(?![\p{L}\p{N}]))/gu, '')
    .replace(/[\ue021-\ue07e]/g, (c) => String.fromCharCode(c.charCodeAt(0) - 0xe000))
    .replace(/\s+/g, ' ')
    .trim();
}

export function extractOutline(text: string): OutlineHeading[] {
  const lines = text.split(/\r\n|\r|\n/);
  const out: OutlineHeading[] = [];
  let i = 0;

  // Front matter only counts when it is closed, as in the parser.
  if (lines[0] !== undefined && /^---[ \t]*$/.test(lines[0])) {
    for (let n = 1; n < lines.length; n++) {
      if (/^(?:---|\.\.\.)[ \t]*$/.test(lines[n]!)) {
        i = n + 1;
        break;
      }
    }
  }

  let fence: { char: string; size: number } | null = null;
  let comment = false;
  // An HTML block hides everything in it from the parser: it ends at a blank line, or for
  // pre, script, style and textarea at the closing tag.
  let html: 'blank' | RegExp | null = null;
  let paragraph: number[] = [];

  const push = (level: number, raw: string, line: number) => {
    const plain = plainHeadingText(raw);
    if (plain) out.push({ level, text: plain, line });
  };

  for (; i < lines.length; i++) {
    const line = lines[i]!;

    if (fence) {
      const close = FENCE.exec(line);
      if (close && close[1]![0] === fence.char && close[1]!.length >= fence.size && close[2]!.trim() === '') fence = null;
      continue;
    }
    if (comment) {
      if (line.includes('-->')) comment = false;
      continue;
    }

    if (html) {
      if (html === 'blank' ? line.trim() === '' : html.test(line)) html = null;
      continue;
    }
    const open = FENCE.exec(line);
    if (open && !(open[1]![0] === '`' && open[2]!.includes('`'))) {
      fence = { char: open[1]![0]!, size: open[1]!.length };
      paragraph = [];
      continue;
    }
    if (/^ {0,3}<!--/.test(line)) {
      if (!line.includes('-->', line.indexOf('<!--') + 4)) comment = true;
      paragraph = [];
      continue;
    }

    const raw = HTML_RAW.exec(line);
    if (raw) {
      const close = new RegExp(`</${raw[1]}>`, 'i');
      if (!close.test(line)) html = close;
      paragraph = [];
      continue;
    }
    // A bare tag line cannot interrupt a paragraph; the named block tags can.
    if (HTML_BLOCK.test(line) || (paragraph.length === 0 && HTML_TAG_ALONE.test(line))) {
      html = 'blank';
      paragraph = [];
      continue;
    }

    const atx = ATX.exec(line);
    if (atx) {
      // "## ##" is an empty heading: the second run is a closing sequence.
      push(atx[1]!.length, (atx[2] ?? '').replace(/^#+[ \t]*$/, ''), i);
      paragraph = [];
      continue;
    }

    const setext = paragraph.length > 0 ? SETEXT.exec(line) : null;
    if (setext) {
      push(setext[1]![0] === '=' ? 1 : 2, paragraph.map((n) => lines[n]!.trim()).join(' '), paragraph[0]!);
      paragraph = [];
      continue;
    }

    // Indented text continues an open paragraph; otherwise it is code and starts nothing.
    if (/^ {4,}\S/.test(line) && paragraph.length > 0) paragraph.push(i);
    else if (THEMATIC.test(line) || NOT_PARAGRAPH.test(line)) paragraph = [];
    else paragraph.push(i);
  }
  return out;
}
