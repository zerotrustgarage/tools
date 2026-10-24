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
const NOT_PARAGRAPH = /^ {0,3}(?:[-*+][ \t]|\d{1,9}[.)][ \t]|>|<|\|)|^ {4,}|^\s*$/;
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

    const atx = ATX.exec(line);
    if (atx) {
      push(atx[1]!.length, atx[2] ?? '', i);
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
