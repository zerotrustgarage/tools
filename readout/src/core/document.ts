// Byte-exact document handling. The editor never re-serializes Markdown: the text the user sees
// is the file, and saving an unedited document must reproduce the original bytes exactly. Three
// things would otherwise change silently on a round trip, so each is detected on load and
// restored on save:
//
// - the UTF-8 byte order mark, which TextDecoder strips by default;
// - CRLF line endings, which CodeMirror normalizes to LF unless told the file's separator;
// - invalid UTF-8, which a lenient decoder would replace with U+FFFD. Such a file is opened
//   read-only instead, because saving it would destroy the bytes that did not decode.
//
// A file that mixes line endings also opens read-only, because the editor can keep only one
// separator. The reader can lift that with an explicit conversion (convertDocument): the text is
// normalized to one ending in memory, the document counts as changed, and nothing reaches the
// disk until the reader saves. Invalid UTF-8 is never converted.

export type LineEnding = '\n' | '\r\n';

/** How many of each line ending a file holds (a CRLF counts once, not as a CR and an LF). */
export interface LineEndingCounts {
  crlf: number;
  lf: number;
  cr: number;
}

export interface DocumentFormat {
  /** True when the file began with the UTF-8 byte order mark (EF BB BF). */
  bom: boolean;
  /**
   * The separator handed to CodeMirror. A file that uses only CRLF keeps CRLF. Everything else
   * (LF only, no line breaks, or mixed endings) uses LF, and a mixed file is reported so the
   * caller can keep it read-only: CodeMirror can only preserve one separator.
   */
  eol: LineEnding;
  mixedEndings: boolean;
  /** False when the bytes are not valid UTF-8; such a document must not be saved. */
  validUtf8: boolean;
  /** The line endings found when the file was read; absent for a new document. */
  endings?: LineEndingCounts;
}

export interface DecodedDocument {
  text: string;
  format: DocumentFormat;
}

const BOM = [0xef, 0xbb, 0xbf];

export const NEW_DOCUMENT_FORMAT: DocumentFormat = Object.freeze({
  bom: false,
  eol: '\n',
  mixedEndings: false,
  validUtf8: true,
});

export function countLineEndings(text: string): LineEndingCounts {
  const counts = { crlf: 0, lf: 0, cr: 0 };
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    if (c === 13) {
      if (text.charCodeAt(i + 1) === 10) {
        counts.crlf++;
        i++;
      } else counts.cr++;
    } else if (c === 10) counts.lf++;
  }
  return counts;
}

export function detectLineEndings(text: string): { eol: LineEnding; mixed: boolean; counts: LineEndingCounts } {
  const counts = countLineEndings(text);
  const kinds = (counts.crlf > 0 ? 1 : 0) + (counts.lf > 0 ? 1 : 0) + (counts.cr > 0 ? 1 : 0);
  return { eol: counts.crlf > 0 && kinds === 1 ? '\r\n' : '\n', mixed: kinds > 1 || counts.cr > 0, counts };
}

/**
 * The ending most of the file uses, which is what a conversion offers first. A tie, a file of
 * lone CRs and a file with no line break all choose LF.
 */
export function majorityEnding(counts: LineEndingCounts): LineEnding {
  return counts.crlf > counts.lf && counts.crlf >= counts.cr ? '\r\n' : '\n';
}

/** Every line ending in text (CRLF, LF or a lone CR) replaced by target. Nothing else changes. */
export function normalizeLineEndings(text: string, target: LineEnding): string {
  return text.replace(/\r\n|\r|\n/g, target);
}

/** True when the document is read-only only because of its line endings, so a conversion can lift it. */
export function canConvertLineEndings(format: DocumentFormat): boolean {
  return format.validUtf8 && format.mixedEndings;
}

/**
 * The text and format after an explicit conversion to target. The byte order mark flag and every
 * character other than a line ending are kept. A document whose bytes did not decode as UTF-8 is
 * refused: converting it would save U+FFFD in place of the bytes that were there.
 */
export function convertDocument(text: string, format: DocumentFormat, target: LineEnding): DecodedDocument {
  if (!format.validUtf8) throw new Error('Refusing to convert a document that is not valid UTF-8');
  const converted = normalizeLineEndings(text, target);
  const counts = countLineEndings(converted);
  return { text: converted, format: { ...format, eol: target, mixedEndings: false, endings: counts } };
}

export function decodeDocument(bytes: Uint8Array): DecodedDocument {
  const bom = bytes.length >= 3 && BOM.every((b, i) => bytes[i] === b);
  const body = bom ? bytes.subarray(3) : bytes;
  let text: string;
  let validUtf8 = true;
  try {
    // ignoreBOM keeps a second BOM (if any) as text; the first one was removed above.
    text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(body);
  } catch {
    validUtf8 = false;
    text = new TextDecoder('utf-8', { fatal: false, ignoreBOM: true }).decode(body);
  }
  const { eol, mixed, counts } = detectLineEndings(text);
  return { text, format: { bom, eol, mixedEndings: mixed, validUtf8, endings: counts } };
}

/** Why the document must stay read-only, or null when it can be edited and saved safely. */
export function readOnlyReason(format: DocumentFormat): string | null {
  if (!format.validUtf8)
    return 'This file is not valid UTF-8. It is open read-only so saving cannot alter bytes that did not decode.';
  if (format.mixedEndings)
    return 'This file mixes line endings (CRLF, LF or CR). It is open read-only so saving cannot normalize them. You can convert them to one ending; nothing is written until you save.';
  return null;
}

/**
 * Encodes text from the editor. The text must already use format.eol between lines (CodeMirror
 * does this when its lineSeparator facet is set to format.eol).
 */
export function encodeDocument(text: string, format: DocumentFormat): Uint8Array {
  if (!format.validUtf8 || format.mixedEndings)
    throw new Error('Refusing to save a document that was opened read-only');
  const body = new TextEncoder().encode(text);
  if (!format.bom) return body;
  const out = new Uint8Array(body.length + 3);
  out.set(BOM, 0);
  out.set(body, 3);
  return out;
}
