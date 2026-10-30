// A small PDF text reader for the print scenarios, so they need no poppler on the machine. It reads
// what Chromium writes (Skia PDF): Flate streams, Type 0 and Type 3 fonts with a ToUnicode map, and text shown
// as hex strings with Tj and TJ. It returns the text of each page, in page order. It is not a
// general PDF parser; a PDF it does not understand yields empty pages, which fails the test loudly.
import { inflateSync } from 'node:zlib';

export function pdfPages(buffer) {
  const raw = buffer.toString('latin1');
  const objects = new Map();
  for (const m of raw.matchAll(/(\d+) 0 obj([\s\S]*?)endobj/g)) {
    const body = m[2];
    const at = body.indexOf('stream');
    const dict = at < 0 ? body : body.slice(0, at);
    let data = null;
    if (at >= 0) {
      const start = body.slice(at + 6).startsWith('\r\n') ? at + 8 : at + 7;
      const end = body.lastIndexOf('endstream');
      data = Buffer.from(body.slice(start, end), 'latin1');
      if (/FlateDecode/.test(dict)) data = inflateSync(data);
    }
    objects.set(Number(m[1]), { dict, data });
  }

  // ToUnicode maps, by font object.
  const cmaps = new Map();
  const cmapOf = (number) => {
    if (cmaps.has(number)) return cmaps.get(number);
    const map = new Map();
    const target = /\/ToUnicode (\d+) 0 R/.exec(objects.get(number)?.dict ?? '');
    const stream = target ? objects.get(Number(target[1]))?.data?.toString('latin1') : null;
    const utf16 = (hex) => Buffer.from(hex, 'hex').swap16().toString('utf16le');
    for (const block of stream?.matchAll(/beginbfchar([\s\S]*?)endbfchar/g) ?? []) {
      for (const p of block[1].matchAll(/<([0-9a-f]+)>\s*<([0-9a-f]+)>/gi)) map.set(parseInt(p[1], 16), utf16(p[2]));
    }
    for (const block of stream?.matchAll(/beginbfrange([\s\S]*?)endbfrange/g) ?? []) {
      for (const p of block[1].matchAll(/<([0-9a-f]+)>\s*<([0-9a-f]+)>\s*<([0-9a-f]+)>/gi)) {
        const first = parseInt(p[1], 16);
        const last = parseInt(p[2], 16);
        const base = utf16(p[3]);
        for (let code = first; code <= last; code++) map.set(code, base.slice(0, -1) + String.fromCharCode(base.charCodeAt(base.length - 1) + code - first));
      }
    }
    cmaps.set(number, map);
    return map;
  };
  // Font resource names (/F4) to font objects; Skia numbers them once for the whole file.
  const fonts = new Map();
  for (const { dict } of objects.values()) for (const m of dict.matchAll(/\/(F\d+) (\d+) 0 R/g)) fonts.set(m[1], Number(m[2]));

  const decode = (stream) => {
    let font = null;
    let out = '';
    const show = (hex) => {
      const map = font === null ? new Map() : cmapOf(font);
      // Type 0 fonts (Identity-H) use two-byte codes; Chromium's Type 3 fonts use one.
      const width = font !== null && /\/Subtype\s*\/Type3/.test(objects.get(font)?.dict ?? '') ? 2 : 4;
      for (const code of hex.match(width === 2 ? /.{2}/g : /.{4}/g) ?? []) out += map.get(parseInt(code, 16)) ?? '';
    };
    for (const m of stream.matchAll(/\/(F\d+) [\d.]+ Tf|\[((?:<[0-9a-f]*>|[-\d.\s])*)\]\s*TJ|<([0-9a-f]*)>\s*Tj|\bET\b/gi)) {
      if (m[1]) font = fonts.get(m[1]) ?? null;
      else if (m[2] !== undefined) for (const h of m[2].matchAll(/<([0-9a-f]*)>/gi)) show(h[1]);
      else if (m[3] !== undefined) show(m[3]);
      out += m[0] === 'ET' ? ' ' : '';
    }
    // Chromium maps the hyphen glyph to a soft hyphen in its ToUnicode maps.
    return out.replace(/\u00ad/g, '-').replace(/\s+/g, ' ').trim();
  };

  const pages = [];
  for (const { dict } of objects.values()) {
    if (!/\/Type\s*\/Page\b(?!s)/.test(dict)) continue;
    const refs = /\/Contents\s*\[([^\]]*)\]/.exec(dict)?.[1] ?? /\/Contents\s*(\d+ 0 R)/.exec(dict)?.[1] ?? '';
    pages.push([...refs.matchAll(/(\d+) 0 R/g)].map((r) => decode(objects.get(Number(r[1]))?.data?.toString('latin1') ?? '')).join(' '));
  }
  return pages;
}
