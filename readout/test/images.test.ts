import { beforeAll, describe, expect, it, vi } from 'vitest';
import {
  MAX_IMAGE_BYTES,
  imageKindFromMime,
  imageKindFromName,
  imageNameAttempt,
  imageProblem,
  imageStem,
  sanitizeImageStem,
  sniffImage,
  timestampStem,
} from '../src/core/images';
import type { Arrival, DropHandlers, ImageSource, Listing, OpenedFile, Platform, SavedImage } from '../src/platform/types';
import { App } from '../src/ui/app';

// RD13: paste or drop an image into a document. The desktop shell writes the file in Rust
// (src-tauri/src/images.rs has its own tests); this file holds the pure checks the browser build
// uses and the flow around the platform call: what is inserted, what is refused, what is never
// written, and that dropping Markdown is unchanged.

const encode = (text: string) => new TextEncoder().encode(text);
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0]);
const JPEG = new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 0, 0x10]);
const GIF = encode('GIF89a\u0001\u0000');
const WEBP = encode('RIFF$\u0000\u0000\u0000WEBPVP8 ');

describe('image checks shared with the browser build', () => {
  it('reads the type from the first bytes', () => {
    expect(sniffImage(PNG)).toBe('png');
    expect(sniffImage(JPEG)).toBe('jpeg');
    expect(sniffImage(GIF)).toBe('gif');
    expect(sniffImage(encode('GIF87a'))).toBe('gif');
    expect(sniffImage(WEBP)).toBe('webp');
    expect(sniffImage(encode("<svg xmlns='http://www.w3.org/2000/svg'/>"))).toBeNull();
    expect(sniffImage(encode('MZ'))).toBeNull();
    expect(sniffImage(encode('RIFF\u0000\u0000\u0000\u0000WAVE'))).toBeNull();
    expect(sniffImage(new Uint8Array())).toBeNull();
  });

  it('accepts four types by MIME type or extension and refuses SVG', () => {
    expect(imageKindFromMime('image/png')).toBe('png');
    expect(imageKindFromMime('IMAGE/JPG')).toBe('jpeg');
    for (const refused of ['image/svg+xml', 'image/bmp', 'text/html', '']) expect(imageKindFromMime(refused)).toBeNull();
    expect(imageKindFromName('a.JPEG')).toBe('jpeg');
    expect(imageKindFromName('a.webp')).toBe('webp');
    for (const refused of ['a.svg', 'a.exe', 'png', 'a.png.exe']) expect(imageKindFromName(refused)).toBeNull();
  });

  it('refuses empty, oversized and mislabelled bytes', () => {
    expect(imageProblem(PNG, 'png')).toBeNull();
    expect(imageProblem(PNG, 'jpeg')).toMatch(/not the kind/);
    expect(imageProblem(encode('<svg/>'), 'png')).toMatch(/not the kind/);
    expect(imageProblem(new Uint8Array(), 'png')).toMatch(/empty/);
    const big = new Uint8Array(MAX_IMAGE_BYTES + 1);
    big.set(PNG);
    expect(imageProblem(big, 'png')).toMatch(/larger than 20 MB/);
    expect(imageProblem(big.subarray(0, MAX_IMAGE_BYTES), 'png')).toBeNull();
  });

  it('cuts names down to [A-Za-z0-9._-] and avoids Windows device names', () => {
    expect(sanitizeImageStem('Screenshot 2026-10-29.png')).toBe('Screenshot-2026-10-29');
    expect(sanitizeImageStem('../../etc/passwd.png')).toBe('etc-passwd');
    expect(sanitizeImageStem('..\\..\\boot.png')).toBe('boot');
    expect(sanitizeImageStem('a:b*c?.PNG')).toBe('a-b-c');
    expect(sanitizeImageStem('CON.png')).toBe('image-CON');
    expect(sanitizeImageStem('console.png')).toBe('console');
    expect(sanitizeImageStem('屏幕截图.png')).toBeNull();
    expect(sanitizeImageStem('a'.repeat(200) + '.png')).toHaveLength(64);
    expect(sanitizeImageStem('x y/z\u0000.png')).toMatch(/^[A-Za-z0-9._-]+$/);
  });

  it('generates a UTC timestamp name and counts up past a taken one', () => {
    expect(timestampStem(new Date(Date.UTC(2026, 9, 29, 13, 45, 9)))).toBe('image-20261029-134509');
    expect(imageStem(undefined, new Date(0))).toBe('image-19700101-000000');
    expect(imageStem('屏幕.png', new Date(0))).toBe('image-19700101-000000');
    expect(imageStem('diagram.png', new Date(0))).toBe('diagram');
    expect([1, 2, 3].map((n) => imageNameAttempt('shot', 'jpeg', n))).toEqual(['shot.jpg', 'shot-2.jpg', 'shot-3.jpg']);
  });
});

const listing: Listing = { folder: { name: 'repo', root: '/repo' }, files: ['README.md'], truncated: false };
const saved: OpenedFile = { ref: { name: 'notes.md', path: '/repo/notes.md', folderPath: 'notes.md' }, bytes: encode('# Notes\n\nbefore after\n') };

interface Rig {
  host: HTMLElement;
  saveImage: ReturnType<typeof vi.fn>;
  handlers: () => DropHandlers;
  opened: Arrival[];
  paste: (file: File | null, text?: string) => Promise<boolean>;
  editor: () => string;
  undo: () => void;
}

async function started(initial: Arrival | null, result: SavedImage | Error = { name: 'image-20261029-134509.png', link: 'images/image-20261029-134509.png' }): Promise<Rig> {
  let handlers!: DropHandlers;
  const opened: Arrival[] = [];
  const saveImage = vi.fn(async (_ref: unknown, _listing: unknown, _image: ImageSource) => {
    if (result instanceof Error) throw result;
    return result;
  });
  const platform: Platform = {
    kind: 'desktop',
    open: async () => null,
    openFolder: async () => null,
    readFolderFile: async () => saved,
    followLink: async () => null,
    reopen: async () => null,
    save: async (ref) => ref,
    saveAs: async () => null,
    exportFile: async () => undefined,
    openExternal: async () => undefined,
    copyRichText: async () => undefined,
    loadLocalImage: async () => null,
    initial: async () => initial,
    remember: () => undefined,
    onDrop: (h) => {
      handlers = { ...h, open: (a) => { opened.push(a); h.open(a); } };
    },
    onCloseRequested: () => undefined,
    setTitle: () => undefined,
    saveImage,
  };
  const host = document.createElement('div');
  document.body.replaceChildren(host);
  const app = new App(platform);
  await app.start(host);
  await settle();
  const content = () => host.querySelector<HTMLElement>('.cm-content')!;
  return {
    host,
    saveImage,
    handlers: () => handlers,
    opened,
    paste: async (file, text = '') => {
      const event = new Event('paste', { bubbles: true, cancelable: true });
      Object.defineProperty(event, 'clipboardData', {
        value: { files: file ? [file] : [], getData: (type: string) => (type === 'text/plain' ? text : '') },
      });
      content().dispatchEvent(event);
      await settle();
      await settle();
      return event.defaultPrevented;
    },
    editor: () => [...host.querySelectorAll('.cm-line')].map((l) => l.textContent).join('\n'),
    undo: () => {
      content().dispatchEvent(new KeyboardEvent('keydown', { key: 'z', ctrlKey: true, bubbles: true }));
    },
  };
}

const banner = (host: HTMLElement) => host.querySelector<HTMLElement>('.banner')!;
const file = (bytes: Uint8Array, name: string, type: string) => new File([bytes as BlobPart], name, { type });

beforeAll(() => {
  const query = { matches: false, addEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined };
  window.matchMedia ??= vi.fn(() => query) as unknown as typeof window.matchMedia;
  Element.prototype.scrollTo ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  // jsdom has no layout; CodeMirror only needs these to measure.
  Range.prototype.getClientRects ??= () => [] as unknown as DOMRectList;
  Range.prototype.getBoundingClientRect ??= () => new DOMRect();
  // jsdom's File has no arrayBuffer in some versions.
  File.prototype.arrayBuffer ??= function (this: File) {
    return new Response(this).arrayBuffer();
  };
});

describe('pasting an image', () => {
  it('saves a png beside the document and inserts the relative link', async () => {
    const rig = await started({ file: saved });
    expect(await rig.paste(file(PNG, 'image.png', 'image/png'))).toBe(true);
    expect(rig.saveImage).toHaveBeenCalledTimes(1);
    const [ref, , image] = rig.saveImage.mock.calls[0]!;
    expect(ref).toEqual(saved.ref);
    expect(image).toMatchObject({ kind: 'bytes', type: 'image/png' });
    expect([...(image as { bytes: Uint8Array }).bytes]).toEqual([...PNG]);
    expect(rig.editor()).toContain('![](images/image-20261029-134509.png)');
    expect(banner(rig.host).textContent).toMatch(/Undo removes the link only; the image file stays/);
  });

  it('is one undo step: undo removes the link, the saved file stays', async () => {
    const rig = await started({ file: saved });
    const before = rig.editor();
    await rig.paste(file(JPEG, 'x.jpg', 'image/jpeg'));
    expect(rig.editor().split('![](').length).toBe(2);
    rig.undo();
    expect(rig.editor()).toBe(before);
    // Nothing deletes the file: saveImage is the only call there ever was.
    expect(rig.saveImage).toHaveBeenCalledTimes(1);
  });

  it('refuses SVG and writes nothing', async () => {
    const rig = await started({ file: saved });
    expect(await rig.paste(file(encode('<svg onload="x()"/>'), 'a.svg', 'image/svg+xml'))).toBe(true);
    expect(rig.saveImage).not.toHaveBeenCalled();
    expect(banner(rig.host).textContent).toMatch(/Only PNG, JPEG, GIF and WebP/);
    expect(rig.editor()).not.toContain('![]');
  });

  it('refuses bytes that are not the claimed type, and empty images', async () => {
    const rig = await started({ file: saved });
    await rig.paste(file(encode('<svg/>'), 'a.png', 'image/png'));
    expect(banner(rig.host).textContent).toMatch(/not the kind of image/);
    await rig.paste(file(new Uint8Array(), 'a.png', 'image/png'));
    expect(banner(rig.host).textContent).toMatch(/empty/);
    expect(rig.saveImage).not.toHaveBeenCalled();
  });

  it('tells the reader to save an unsaved document first and writes nothing', async () => {
    const rig = await started(null);
    await rig.paste(file(PNG, 'image.png', 'image/png'));
    expect(rig.saveImage).not.toHaveBeenCalled();
    expect(banner(rig.host).textContent).toMatch(/Save the document first/);
    expect(rig.editor()).not.toContain('![]');
  });

  it('leaves a clipboard that has text to the editor', async () => {
    const rig = await started({ file: saved });
    await rig.paste(file(PNG, 'image.png', 'image/png'), 'a table copied from Excel');
    expect(rig.saveImage).not.toHaveBeenCalled();
    expect(rig.editor()).not.toContain('![]');
  });

  it('reports a failed write and inserts nothing', async () => {
    const rig = await started({ file: saved }, new Error('The images folder is a link.'));
    await rig.paste(file(PNG, 'image.png', 'image/png'));
    expect(banner(rig.host).textContent).toBe('No image was added: The images folder is a link.');
    expect(rig.editor()).not.toContain('![]');
  });
});

describe('dropping an image', () => {
  it('copies a dropped file from disk and inserts the link', async () => {
    const rig = await started({ file: saved });
    rig.handlers().image!('Network Map.png', { kind: 'dropped', path: '/home/me/Network Map.png', name: 'Network Map.png' });
    await settle();
    await settle();
    expect(rig.saveImage.mock.calls[0]![2]).toEqual({ kind: 'dropped', path: '/home/me/Network Map.png', name: 'Network Map.png' });
    expect(rig.editor()).toContain('![](images/image-20261029-134509.png)');
  });

  it('refuses an image type that may not be saved', async () => {
    const rig = await started({ file: saved });
    rig.handlers().image!('logo.svg', null);
    await settle();
    expect(rig.saveImage).not.toHaveBeenCalled();
    expect(banner(rig.host).textContent).toMatch(/logo.svg was not added\. Only PNG, JPEG, GIF and WebP/);
  });

  it('refuses like any other file when nothing is open, and asks for a save once there is text', async () => {
    const rig = await started(null);
    const drop = () => rig.handlers().image!('a.png', { kind: 'dropped', path: '/x/a.png', name: 'a.png' });
    drop();
    await settle();
    expect(banner(rig.host).textContent).toBe('Only Markdown files (.md, .markdown, .txt) and folders open here: a.png');
    await rig.paste(null, 'some text');
    expect(rig.editor()).toContain('some text');
    drop();
    await settle();
    expect(banner(rig.host).textContent).toMatch(/Save the document first/);
    expect(rig.saveImage).not.toHaveBeenCalled();
  });

  it('keeps opening a dropped Markdown file as before', async () => {
    const rig = await started({ file: saved });
    const arrival: Arrival = { file: { ref: { name: 'other.md', path: '/repo/other.md' }, bytes: encode('# Other\n') } };
    rig.handlers().open(arrival);
    await settle();
    await settle();
    expect(rig.opened).toEqual([arrival]);
    expect(rig.saveImage).not.toHaveBeenCalled();
    expect(rig.editor()).toContain('# Other');
  });
});
