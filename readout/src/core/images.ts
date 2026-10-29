// Pasting or dropping an image into a document (RD13). The desktop build saves through Rust
// (src-tauri/src/images.rs, which holds the same rules and is the authority there); the browser
// build has no Rust, so it uses these functions before it writes through a directory handle.

/** The most an image may weigh: 20 MiB, as in Rust. */
export const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
/** The folder beside the document that receives images. */
export const IMAGE_FOLDER = 'images';

export type ImageKind = 'png' | 'jpeg' | 'gif' | 'webp';

const MIME: Record<ImageKind, string> = { png: 'image/png', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp' };
const EXTENSION: Record<ImageKind, string> = { png: 'png', jpeg: 'jpg', gif: 'gif', webp: 'webp' };

export const IMAGE_REFUSED = 'Only PNG, JPEG, GIF and WebP images can be added. SVG and other types are not saved.';

/** The kind a MIME type names, or null for SVG and everything else. */
export function imageKindFromMime(type: string): ImageKind | null {
  const t = type.trim().toLowerCase();
  if (t === 'image/jpg') return 'jpeg';
  return (Object.keys(MIME) as ImageKind[]).find((kind) => MIME[kind] === t) ?? null;
}

/** The kind a file name's extension names, or null. */
export function imageKindFromName(name: string): ImageKind | null {
  const extension = /\.([A-Za-z0-9]+)$/.exec(name)?.[1]?.toLowerCase();
  if (extension === 'jpg' || extension === 'jpeg') return 'jpeg';
  return (Object.keys(EXTENSION) as ImageKind[]).find((kind) => EXTENSION[kind] === extension) ?? null;
}

export const imageExtension = (kind: ImageKind): string => EXTENSION[kind];
export const imageMime = (kind: ImageKind): string => MIME[kind];

const startsWith = (bytes: Uint8Array, prefix: readonly number[], at = 0) =>
  bytes.length >= at + prefix.length && prefix.every((b, i) => bytes[at + i] === b);
const ascii = (text: string) => [...text].map((c) => c.charCodeAt(0));

/** The kind the bytes say they are, from their first bytes alone. */
export function sniffImage(bytes: Uint8Array): ImageKind | null {
  if (startsWith(bytes, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])) return 'png';
  if (startsWith(bytes, [0xff, 0xd8, 0xff])) return 'jpeg';
  if (startsWith(bytes, ascii('GIF87a')) || startsWith(bytes, ascii('GIF89a'))) return 'gif';
  if (startsWith(bytes, ascii('RIFF')) && startsWith(bytes, ascii('WEBP'), 8)) return 'webp';
  return null;
}

/** Why bytes cannot be saved as the claimed kind, or null when they can. */
export function imageProblem(bytes: Uint8Array, claimed: ImageKind): string | null {
  if (bytes.length === 0) return 'The image is empty.';
  if (bytes.length > MAX_IMAGE_BYTES) return `The image is larger than ${MAX_IMAGE_BYTES / (1024 * 1024)} MB.`;
  return sniffImage(bytes) === claimed ? null : 'The file is not the kind of image it claims to be.';
}

const RESERVED = /^(con|prn|aux|nul|com[1-9]|lpt[1-9])$/i;

/** A file name cut down to [A-Za-z0-9._-] without its image extension, or null when nothing is left. */
export function sanitizeImageStem(raw: string): string | null {
  const dot = raw.lastIndexOf('.');
  const stripped = dot > 0 && imageKindFromName(raw) ? raw.slice(0, dot) : raw;
  const cleaned = stripped
    .replace(/[^A-Za-z0-9._-]/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^[._-]+/, '')
    .slice(0, 64)
    .replace(/[._-]+$/, '');
  if (!cleaned) return null;
  return RESERVED.test(cleaned.split('.')[0]!) ? `image-${cleaned}` : cleaned;
}

/** `image-YYYYMMDD-HHMMSS` in UTC, the same as the desktop build. */
export function timestampStem(now: Date): string {
  const p = (n: number, width = 2) => String(n).padStart(width, '0');
  return (
    `image-${p(now.getUTCFullYear(), 4)}${p(now.getUTCMonth() + 1)}${p(now.getUTCDate())}` +
    `-${p(now.getUTCHours())}${p(now.getUTCMinutes())}${p(now.getUTCSeconds())}`
  );
}

/** The name a new image starts from: the dropped file's cleaned name, else a timestamp. */
export const imageStem = (original: string | undefined, now: Date): string =>
  (original ? sanitizeImageStem(original) : null) ?? timestampStem(now);

/** The candidate names for a stem: `stem.ext`, then `stem-2.ext`, `stem-3.ext`, and so on. */
export const imageNameAttempt = (stem: string, kind: ImageKind, attempt: number): string =>
  `${attempt <= 1 ? stem : `${stem}-${attempt}`}.${EXTENSION[kind]}`;

/** The text inserted at the cursor. The link is relative to the document and needs no escaping. */
export const imageMarkdown = (link: string): string => `![](${link})`;
