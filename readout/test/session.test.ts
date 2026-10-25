import { beforeAll, describe, expect, it, vi } from 'vitest';
import type { Arrival, FileRef, Listing, OpenedFile, Platform } from '../src/platform/types';
import { App } from '../src/ui/app';

// The stored session: what was open last reopens at the next start, the empty start never
// overwrites it, and a document is remembered with a folder only when it is in that folder.

const encode = (text: string) => new TextEncoder().encode(text);
const listing: Listing = { folder: { name: 'repo', root: '/repo' }, files: ['README.md', 'docs/setup.md'], truncated: false };
const inFolder = (folderPath: string): OpenedFile => ({
  ref: { name: folderPath.split('/').pop()!, path: `/repo/${folderPath}`, folderPath },
  bytes: encode(`# ${folderPath}\n`),
});
const lone: OpenedFile = { ref: { name: 'notes.md', path: '/elsewhere/notes.md' }, bytes: encode('# notes\n') };

type PlatformOverrides = Partial<Pick<Platform, 'readFolderFile' | 'relist'>>;

function fakePlatform(initial: Arrival | null, overrides: PlatformOverrides = {}) {
  const remembered: [Listing | null, FileRef | null][] = [];
  const platform: Platform = {
    kind: 'desktop',
    open: async () => lone,
    openFolder: async () => listing,
    readFolderFile: async (_listing, folderPath) => inFolder(folderPath),
    followLink: async () => null,
    reopen: async () => null,
    save: async (ref) => ref,
    saveAs: async (_bytes, name) => ({ name, path: `/elsewhere/${name}` }),
    exportFile: async () => undefined,
    openExternal: async () => undefined,
    loadLocalImage: async () => null,
    initial: async () => initial,
    remember: (l, ref) => void remembered.push([l, ref]),
    onDrop: () => undefined,
    onCloseRequested: () => undefined,
    setTitle: () => undefined,
    ...overrides,
  };
  return { platform, remembered };
}

/** The user's actions, called directly: every App's shortcuts stay on the shared window. */
interface Actions {
  open(): Promise<void>;
  openFolder(): Promise<void>;
  newDocument(): Promise<void>;
  closeFolder(): Promise<void>;
  save(as: boolean): Promise<boolean>;
  openFolderPath(folderPath: string, fragment: string | null, remember: boolean): Promise<boolean>;
  watcher: { tick(force?: boolean): Promise<void> };
}

async function started(initial: Arrival | null, overrides?: PlatformOverrides) {
  const { platform, remembered } = fakePlatform(initial, overrides);
  const host = document.createElement('div');
  document.body.replaceChildren(host);
  const app = new App(platform);
  await app.start(host);
  return { host, remembered, act: app as unknown as Actions };
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const title = (host: HTMLElement) => host.querySelector('.doc-title')!.textContent;
const banner = (host: HTMLElement) => host.querySelector<HTMLElement>('.banner')!;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

// What jsdom lacks and the app calls while laying out.
beforeAll(() => {
  const query = { matches: false, addEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined };
  window.matchMedia ??= vi.fn(() => query) as unknown as typeof window.matchMedia;
  Element.prototype.scrollTo ??= () => undefined;
  Element.prototype.scrollIntoView ??= () => undefined;
});

describe('the stored session', () => {
  it('does not overwrite the stored session while starting empty', async () => {
    const { host, remembered } = await started(null);
    expect(title(host)).toBe('Untitled.md');
    expect(remembered).toEqual([]);
  });

  it('reopens the document that was open in a folder instead of its README', async () => {
    const { host, remembered } = await started({ listing, file: inFolder('docs/setup.md') });
    expect(title(host)).toBe('docs/setup.md');
    expect(host.querySelector('.sidebar')!.hasAttribute('hidden')).toBe(false);
    expect(remembered).toEqual([[listing, inFolder('docs/setup.md').ref]]);
  });

  it('remembers a file the app was started with', async () => {
    const { remembered } = await started({ file: lone });
    expect(remembered).toEqual([[null, lone.ref]]);
  });

  it('remembers a lone file, then a folder with its document, then forgets on close', async () => {
    const { host, remembered, act } = await started(null);
    await act.open();
    expect(remembered.at(-1)).toEqual([null, lone.ref]);

    await act.openFolder();
    expect(title(host)).toBe('README.md');
    expect(remembered.at(-1)).toEqual([listing, inFolder('README.md').ref]);

    await act.newDocument();
    expect(remembered.at(-1)).toEqual([listing, null]);

    await act.closeFolder();
    expect(remembered.at(-1)).toEqual([null, null]);
  });

  it('keeps only the folder when the document is saved outside it', async () => {
    const { remembered, act } = await started({ listing, file: inFolder('README.md') });
    expect(await act.save(true)).toBe(true);
    expect(remembered.at(-1)).toEqual([listing, null]);
  });

  it('offers a session that needs access again, and opens it on the click', async () => {
    const open = vi.fn(async () => ({ file: lone }));
    const { host, remembered } = await started({ pending: { name: 'repo', open } });
    const banner = host.querySelector<HTMLElement>('.banner')!;
    expect(banner.hidden).toBe(false);
    expect(banner.textContent).toContain('repo');
    expect(open).not.toHaveBeenCalled();
    expect(remembered).toEqual([]);

    banner.querySelector('button')!.click();
    await settle();
    expect(open).toHaveBeenCalledOnce();
    expect(title(host)).toBe('notes.md');
    expect(remembered.at(-1)).toEqual([null, lone.ref]);
  });
});

describe('following renamed files', () => {
  it('does not show a stale error if the renamed-file read fails after another document opens', async () => {
    const failure = deferred<OpenedFile>();
    const readStarted = deferred<void>();
    const renamed = { ...inFolder('docs/renamed.md'), bytes: inFolder('docs/setup.md').bytes };
    let renamedReads = 0;
    const { host, act } = await started({ listing, file: inFolder('docs/setup.md') }, {
      relist: async () => ({ ...listing, files: ['README.md', 'docs/renamed.md'], truncated: false }),
      readFolderFile: async (_listing, path) => {
        if (path === 'docs/renamed.md') {
          renamedReads++;
          if (renamedReads === 1) return renamed;
          readStarted.resolve();
          return failure.promise;
        }
        return inFolder(path);
      },
    });

    const tick = act.watcher.tick(true);
    await readStarted.promise;
    expect(title(host)).toBe('docs/setup.md');

    expect(await act.openFolderPath('README.md', null, true)).toBe(true);
    expect(title(host)).toBe('README.md');
    failure.reject(new Error('the renamed file could not be read'));
    await tick;

    expect(banner(host).hidden).toBe(true);
    expect(banner(host).textContent).not.toContain('renamed.md');
  });

  it('shows a failed-follow notice when the rename read fails for the current document', async () => {
    const failure = deferred<OpenedFile>();
    const readStarted = deferred<void>();
    let renamedReads = 0;
    const { host, act } = await started({ listing, file: inFolder('docs/setup.md') }, {
      relist: async () => ({ ...listing, files: ['README.md', 'docs/renamed.md'], truncated: false }),
      readFolderFile: async (_listing, path) => {
        if (path === 'docs/renamed.md') {
          renamedReads++;
          if (renamedReads === 1) return { ...inFolder(path), bytes: inFolder('docs/setup.md').bytes };
          readStarted.resolve();
          return failure.promise;
        }
        return inFolder(path);
      },
    });

    const tick = act.watcher.tick(true);
    await readStarted.promise;
    failure.reject(new Error('the renamed file could not be read'));
    await tick;

    expect(banner(host).hidden).toBe(false);
    expect(banner(host).textContent).toContain('renamed.md');
    expect(banner(host).textContent).toContain('could not be followed');
  });
});
