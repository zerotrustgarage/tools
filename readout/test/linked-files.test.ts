import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import type { Arrival, LinkedFileCheck, Listing, OpenedFile, Platform } from '../src/platform/types';
import { App } from '../src/ui/app';
import { opensInReadout } from '../src/core/paths';

// RD16: a link to a file that is not Markdown asks first and opens it in the default application
// (desktop only). Rust resolves the path and judges the type (src-tauri/src/linked.rs, with its
// own tests); this file holds the flow around it: the confirmation, what Cancel does, how a
// refusal reads, and that Markdown links, external links and the browser build are unchanged.

const encode = (text: string) => new TextEncoder().encode(text);
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

const DOC = [
  '# Links',
  '',
  '[Spec](files/spec.pdf)',
  '[Run it](files/run.exe)',
  '[Spaced](files/my%20spec.pdf)',
  '[Other page](other.md)',
  '[Docs folder](docs)',
  '[Site](https://example.com/x)',
  '[Mail](mailto:a@example.com)',
  '',
].join('\n');

const listing: Listing = { folder: { name: 'repo', root: '/repo' }, files: ['README.md', 'other.md'], truncated: false };
const inFolder = (folderPath: string, text = DOC): OpenedFile => ({
  ref: { name: folderPath, path: `/repo/${folderPath}`, folderPath },
  bytes: encode(text),
});
const lone: OpenedFile = { ref: { name: 'notes.md', path: '/elsewhere/notes.md' }, bytes: encode(DOC) };

const confirm = (name: string, kind = 'PDF document (.pdf)'): LinkedFileCheck => ({
  status: 'confirm',
  path: `/repo/files/${name}`,
  name,
  kind,
  message: '',
});
const refuse = (message: string): LinkedFileCheck => ({ status: 'refused', path: '', name: '', kind: '', message });
const folderCheck: LinkedFileCheck = { status: 'folder', path: '', name: '', kind: '', message: '' };

interface Rig {
  host: HTMLElement;
  inspect: ReturnType<typeof vi.fn>;
  open: ReturnType<typeof vi.fn>;
  external: ReturnType<typeof vi.fn>;
  follow: ReturnType<typeof vi.fn>;
  read: ReturnType<typeof vi.fn>;
}

async function started(
  initial: Arrival,
  answer: (target: string) => LinkedFileCheck | Error,
  options: { desktopOnlyLinks?: boolean } = {},
): Promise<Rig> {
  const inspect = vi.fn(async (_from: unknown, target: string) => {
    const result = answer(target);
    if (result instanceof Error) throw result;
    return result;
  });
  const open = vi.fn(async () => undefined);
  const external = vi.fn(async () => undefined);
  const follow = vi.fn(async () => null);
  const read = vi.fn(async (_l: Listing, folderPath: string) => inFolder(folderPath, '# Other\n'));
  const platform: Platform = {
    kind: options.desktopOnlyLinks === false ? 'web' : 'desktop',
    open: async () => null,
    openFolder: async () => null,
    readFolderFile: read,
    followLink: follow,
    reopen: async () => null,
    save: async (ref) => ref,
    saveAs: async () => null,
    exportFile: async () => undefined,
    openExternal: external,
    copyRichText: async () => undefined,
    loadLocalImage: async () => null,
    initial: async () => initial,
    remember: () => undefined,
    onDrop: () => undefined,
    onCloseRequested: () => undefined,
    setTitle: () => undefined,
    ...(options.desktopOnlyLinks === false ? {} : { inspectLinkedFile: inspect, openLinkedFile: open }),
  };
  const host = document.createElement('div');
  document.body.replaceChildren(host);
  const app = new App(platform);
  await app.start(host);
  await settle();
  return { host, inspect, open, external, follow, read };
}

const link = (host: HTMLElement, href: string) => {
  const anchor = host.querySelector<HTMLAnchorElement>(`.markdown-body a[href="${href}"]`);
  expect(anchor, `a link to ${href}`).not.toBeNull();
  return anchor!;
};
const click = async (host: HTMLElement, href: string) => {
  link(host, href).click();
  await settle();
};
const dialog = () => document.querySelector<HTMLDialogElement>('dialog.modal');
const dialogButtons = () => [...document.querySelectorAll<HTMLButtonElement>('dialog.modal button')];
const pressed = async (label: string) => {
  dialogButtons().find((b) => b.textContent === label)!.click();
  await settle();
};
const banner = (host: HTMLElement) => host.querySelector<HTMLElement>('.banner')!;

beforeAll(() => {
  const query = { matches: false, addEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined };
  window.matchMedia ??= vi.fn(() => query) as unknown as typeof window.matchMedia;
  Element.prototype.scrollTo ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
  // jsdom has no modal dialog; the app only needs it to open, close and report the close.
  HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
    this.removeAttribute('open');
    this.dispatchEvent(new Event('close'));
  };
});

afterEach(() => {
  document.body.innerHTML = '';
});

describe('a link to a document file in an open folder', () => {
  it('shows the full path and the type, with Cancel as the default and Open as a plain button', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, (t) => confirm(t.split('/').pop()!));
    await click(rig.host, 'files/spec.pdf');
    expect(rig.inspect).toHaveBeenCalledWith(expect.objectContaining({ folderPath: 'README.md' }), 'files/spec.pdf');
    expect(dialog()).not.toBeNull();
    expect(dialog()!.textContent).toContain('/repo/files/spec.pdf');
    expect(dialog()!.textContent).toContain('Type: PDF document (.pdf)');
    expect(dialog()!.textContent).toContain('does not run programs');
    const [open, cancel] = dialogButtons();
    expect(open!.textContent).toBe('Open');
    expect(cancel!.textContent).toBe('Cancel');
    expect(document.activeElement).toBe(cancel);
    expect(cancel!.className).toBe('primary');
    expect(open!.className).not.toContain('primary');
    // Nothing opens until Open is clicked.
    expect(rig.open).not.toHaveBeenCalled();
    await pressed('Cancel');
  });

  it('opens the file only after Open, passing the decoded target and the confirmed path', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, (t) => confirm(t.split('/').pop()!));
    await click(rig.host, 'files/my%20spec.pdf');
    expect(rig.inspect).toHaveBeenCalledWith(expect.anything(), 'files/my spec.pdf');
    await pressed('Open');
    expect(rig.open).toHaveBeenCalledTimes(1);
    expect(rig.open).toHaveBeenCalledWith(expect.anything(), 'files/my spec.pdf', expect.objectContaining({ path: '/repo/files/my spec.pdf' }));
    expect(dialog()).toBeNull();
    expect(banner(rig.host).hidden).toBe(true);
  });

  it('does nothing on Cancel, on Escape, and leaves the document where it was', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, (t) => confirm(t.split('/').pop()!));
    await click(rig.host, 'files/spec.pdf');
    await pressed('Cancel');
    expect(rig.open).not.toHaveBeenCalled();
    expect(dialog()).toBeNull();
    expect(rig.host.querySelector('.doc-title')!.textContent).toContain('README.md');

    await click(rig.host, 'files/spec.pdf');
    dialog()!.dispatchEvent(new Event('cancel'));
    dialog()!.close();
    await settle();
    expect(rig.open).not.toHaveBeenCalled();
    expect(rig.read).not.toHaveBeenCalled();
  });

  it('reports a failure to open', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, (t) => confirm(t.split('/').pop()!));
    rig.open.mockRejectedValueOnce('The link now points to a different file');
    await click(rig.host, 'files/spec.pdf');
    await pressed('Open');
    expect(banner(rig.host).hidden).toBe(false);
    expect(banner(rig.host).textContent).toContain('The link now points to a different file');
  });
});

describe('a refused link', () => {
  it('shows the reason from Rust and never asks or opens', async () => {
    const message = 'Readout does not run programs, so it will not open "run.exe". Open it from File Explorer yourself if you trust it.';
    const rig = await started({ listing, file: inFolder('README.md') }, () => refuse(message));
    await click(rig.host, 'files/run.exe');
    expect(banner(rig.host).hidden).toBe(false);
    expect(banner(rig.host).textContent).toBe(message);
    expect(dialog()).toBeNull();
    expect(rig.open).not.toHaveBeenCalled();
  });

  it('shows a failed inspection as a notice', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, () => new Error('That file was not opened through this window'));
    await click(rig.host, 'files/spec.pdf');
    expect(banner(rig.host).textContent).toContain('Could not open files/spec.pdf');
    expect(dialog()).toBeNull();
    expect(rig.open).not.toHaveBeenCalled();
  });
});

describe('links that are not files', () => {
  it('opens a Markdown link in the app without asking Rust about a file', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, () => confirm('x'));
    await click(rig.host, 'other.md');
    expect(rig.read).toHaveBeenCalledWith(listing, 'other.md');
    expect(rig.inspect).not.toHaveBeenCalled();
    expect(dialog()).toBeNull();
  });

  it('keeps the Markdown notice for a folder with no README', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, () => folderCheck);
    await click(rig.host, 'docs');
    expect(banner(rig.host).textContent).toBe('Only Markdown files open here: docs');
    expect(dialog()).toBeNull();
  });

  it('opens web and mail links in the browser, as before, and never asks about a file', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, () => confirm('x'));
    await click(rig.host, 'https://example.com/x');
    await click(rig.host, 'mailto:a@example.com');
    expect(rig.external.mock.calls.map((c) => c[0])).toEqual(['https://example.com/x', 'mailto:a@example.com']);
    expect(rig.inspect).not.toHaveBeenCalled();
    expect(dialog()).toBeNull();
  });
});

describe('a lone file (no open folder)', () => {
  it('judges a non-Markdown link before anything else and opens it after the confirmation', async () => {
    const rig = await started({ file: lone }, (t) => confirm(t.split('/').pop()!));
    await click(rig.host, 'files/spec.pdf');
    expect(rig.inspect).toHaveBeenCalledWith(lone.ref, 'files/spec.pdf');
    expect(dialog()).not.toBeNull();
    expect(rig.follow).not.toHaveBeenCalled();
    await pressed('Open');
    expect(rig.open).toHaveBeenCalledTimes(1);
  });

  it('refuses a program without touching the Markdown link path', async () => {
    const rig = await started({ file: lone }, () => refuse('Readout does not run programs, so it will not open "run.exe".'));
    await click(rig.host, 'files/run.exe');
    expect(banner(rig.host).textContent).toContain('does not run programs');
    expect(rig.follow).not.toHaveBeenCalled();
  });

  it('follows a Markdown link as before, and a folder link falls through to the README rule', async () => {
    const rig = await started({ file: lone }, () => folderCheck);
    await click(rig.host, 'other.md');
    expect(rig.follow).toHaveBeenCalledWith(lone.ref, 'other.md');
    expect(rig.inspect).not.toHaveBeenCalled();
    await click(rig.host, 'docs');
    expect(rig.inspect).toHaveBeenCalledWith(lone.ref, 'docs');
    expect(rig.follow).toHaveBeenCalledWith(lone.ref, 'docs');
    expect(dialog()).toBeNull();
  });
});

describe('the browser build', () => {
  it('is unchanged: a document file in a folder is not opened, and nothing is asked', async () => {
    const rig = await started({ listing, file: inFolder('README.md') }, () => confirm('x'), { desktopOnlyLinks: false });
    await click(rig.host, 'files/spec.pdf');
    expect(banner(rig.host).textContent).toBe('Only Markdown files open here: files/spec.pdf');
    expect(rig.inspect).not.toHaveBeenCalled();
    expect(dialog()).toBeNull();
  });

  it('keeps following a lone file link through followLink', async () => {
    const rig = await started({ file: lone }, () => confirm('x'), { desktopOnlyLinks: false });
    await click(rig.host, 'files/spec.pdf');
    expect(rig.follow).toHaveBeenCalledWith(lone.ref, 'files/spec.pdf');
    expect(dialog()).toBeNull();
  });
});

describe('which links Readout opens itself', () => {
  it('names Markdown and text, and nothing else', () => {
    for (const path of ['a.md', 'a.MARKDOWN', 'a.mdown', 'a.mkd', 'a.mkdn', 'notes.txt', 'docs/Notes.TXT']) {
      expect(opensInReadout(path), path).toBe(true);
    }
    for (const path of ['a.pdf', 'run.exe', 'a.md.exe', 'README', 'docs', 'a.md:stream']) {
      expect(opensInReadout(path), path).toBe(false);
    }
  });
});
