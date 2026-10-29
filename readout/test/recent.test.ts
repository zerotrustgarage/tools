import { describe, expect, it } from 'vitest';
import { MAX_RECENT, parseRecent, pushRecent, removeRecent } from '../src/core/recent';
import { createRecentFiles, type HandleOps, type RecentBackend } from '../src/platform/recent-web';

// The recent-files list as data (core/recent.ts) and the browser's store built on it
// (platform/recent-web.ts). The desktop's list is Rust's, tested in src-tauri/src/recent.rs.

interface Entry {
  id: string;
}
const entry = (id: string): Entry => ({ id });
const ids = (list: Entry[]) => list.map((e) => e.id);

describe('ordering, duplicates and the cap', () => {
  it('puts the newest first and moves a repeat to the top without duplicating it', () => {
    let list: Entry[] = [];
    for (const id of ['a', 'b', 'c']) list = pushRecent(list, entry(id));
    expect(ids(list)).toEqual(['c', 'b', 'a']);
    list = pushRecent(list, entry('a'));
    expect(ids(list)).toEqual(['a', 'c', 'b']);
  });

  it('keeps only the newest ten by default, and the cap can be set', () => {
    let list: Entry[] = [];
    for (let n = 0; n < 15; n++) list = pushRecent(list, entry(`f${n}`));
    expect(list).toHaveLength(MAX_RECENT);
    expect(MAX_RECENT).toBe(10);
    expect(list[0]!.id).toBe('f14');
    expect(list.at(-1)!.id).toBe('f5');
    expect(ids(pushRecent(list, entry('x'), 3))).toEqual(['x', 'f14', 'f13']);
  });

  it('does not change the list it was given', () => {
    const list = [entry('a')];
    pushRecent(list, entry('b'));
    removeRecent(list, 'a');
    expect(ids(list)).toEqual(['a']);
  });

  it('removes one line by id and leaves the rest in order', () => {
    expect(ids(removeRecent([entry('a'), entry('b'), entry('c')], 'b'))).toEqual(['a', 'c']);
    expect(ids(removeRecent([entry('a')], 'zzz'))).toEqual(['a']);
  });
});

describe('reading stored data that may be damaged', () => {
  const isHandle = (value: unknown): value is { getFile: () => void } =>
    typeof value === 'object' && value !== null && typeof (value as { getFile?: unknown }).getFile === 'function';
  const handle = { getFile: () => undefined };

  it('accepts a well-formed list unchanged', () => {
    const raw = [
      { id: '1', name: 'a.md', detail: 'docs', handle },
      { id: '2', name: 'b.md', handle },
    ];
    expect(parseRecent(raw, isHandle)).toEqual(raw);
  });

  it.each([undefined, null, 0, 'text', {}, { 0: 'x' }, true])('gives an empty list for %j', (raw) => {
    expect(parseRecent(raw, isHandle)).toEqual([]);
  });

  it('drops unusable lines and keeps the good ones', () => {
    const raw = [
      null,
      7,
      'x',
      { id: '', name: 'a.md', handle },
      { id: '1', name: '', handle },
      { id: '2', name: 'no-handle.md' },
      { id: '3', name: 'not-a-handle.md', handle: { path: '/etc/passwd' } },
      { id: 4, name: 'numeric-id.md', handle },
      { id: '5', name: 'ok.md', detail: 9, handle },
      { id: '5', name: 'repeat.md', handle },
    ];
    expect(parseRecent(raw, isHandle)).toEqual([{ id: '5', name: 'ok.md', handle }]);
  });

  it('applies the cap to a stored list that is too long', () => {
    const raw = Array.from({ length: 40 }, (_, n) => ({ id: `f${n}`, name: `f${n}.md`, handle }));
    expect(parseRecent(raw, isHandle)).toHaveLength(MAX_RECENT);
    expect(parseRecent(raw, isHandle, 4).map((e) => e.id)).toEqual(['f0', 'f1', 'f2', 'f3']);
  });
});

// A handle is a plain object here; sameness is identity of its `key`.
interface FakeHandle {
  key: string;
  getFile(): Promise<File>;
}
const handleFor = (key: string): FakeHandle => ({ key, getFile: async () => new File(['# ' + key], key + '.md') });

function memoryBackend(initial: unknown = undefined) {
  const state = { value: initial, writes: 0 };
  const backend: RecentBackend = {
    read: async () => state.value,
    write: async (value) => {
      state.value = value;
      state.writes++;
    },
  };
  return { backend, state };
}

const gone = new Set<string>();
const ops: HandleOps = {
  isHandle: (value): value is FileSystemFileHandle => typeof value === 'object' && value !== null && typeof (value as FakeHandle).getFile === 'function',
  same: async (a, b) => (a as unknown as FakeHandle).key === (b as unknown as FakeHandle).key,
  read: async (handle) => (gone.has((handle as unknown as FakeHandle).key) ? null : (handle as unknown as FakeHandle).getFile()),
  within: async (_folder, handle) => ((handle as unknown as FakeHandle).key.startsWith('repo/') ? (handle as unknown as FakeHandle).key.slice(5) : null),
};
const ref = (key: string, folderPath?: string) => ({ name: key + '.md', handle: handleFor(key) as unknown as FileSystemFileHandle, folderPath });
const names = async (recent: ReturnType<typeof createRecentFiles>) => (await recent.list()).map((e) => e.name);

describe('the browser store', () => {
  it('survives a round trip: what one session adds the next session lists', async () => {
    const { backend, state } = memoryBackend();
    const first = createRecentFiles(backend, ops);
    await first.add(ref('a'));
    await first.add(ref('b'));
    // A new store over the same backend is a new page load.
    const second = createRecentFiles({ read: async () => structuredCloneKeepingFunctions(state.value), write: backend.write }, ops);
    expect(await names(second)).toEqual(['b.md', 'a.md']);
  });

  it('puts the newest first, merges the same file reached another way, and caps the list', async () => {
    const { backend } = memoryBackend();
    const recent = createRecentFiles(backend, ops, 3);
    for (const key of ['a', 'b', 'c']) await recent.add(ref(key));
    await recent.add(ref('a'));
    expect(await names(recent)).toEqual(['a.md', 'c.md', 'b.md']);
    // The same file through a folder is still one line, with its folder shown.
    await recent.add(ref('b', 'docs/b'));
    const listed = await recent.list();
    expect(listed.map((e) => e.name)).toEqual(['b.md', 'a.md', 'c.md']);
    expect(listed[0]!.detail).toBe('docs');
    await recent.add(ref('d'));
    expect(await names(recent)).toEqual(['d.md', 'b.md', 'a.md']);
  });

  it('ignores a file that has no handle (an upload, a drop)', async () => {
    const { backend, state } = memoryBackend();
    const recent = createRecentFiles(backend, ops);
    await recent.add({ name: 'dropped.md' });
    expect(await recent.list()).toEqual([]);
    expect(state.writes).toBe(0);
  });

  it('tolerates corrupt storage: an empty list, and the next add starts a clean one', async () => {
    for (const bad of ['garbage', 42, { files: [] }, [null, 1, { id: 'x' }]]) {
      const { backend, state } = memoryBackend(bad);
      const recent = createRecentFiles(backend, ops);
      expect(await recent.list()).toEqual([]);
      await recent.add(ref('a'));
      expect(await names(recent)).toEqual(['a.md']);
      expect((state.value as unknown[]).length).toBe(1);
    }
  });

  it('tolerates storage that throws', async () => {
    const broken: RecentBackend = {
      read: async () => {
        throw new Error('blocked');
      },
      write: async () => {
        throw new Error('blocked');
      },
    };
    const recent = createRecentFiles(broken, ops);
    expect(await recent.list()).toEqual([]);
    await expect(recent.add(ref('a'))).resolves.toBeUndefined();
    await expect(recent.remove('x')).resolves.toBeUndefined();
    await expect(recent.clear()).resolves.toBeUndefined();
  });

  it('removes one line and clears the list', async () => {
    const { backend } = memoryBackend();
    const recent = createRecentFiles(backend, ops);
    for (const key of ['a', 'b', 'c']) await recent.add(ref(key));
    const [, middle] = await recent.list();
    await recent.remove(middle!.id);
    expect(await names(recent)).toEqual(['c.md', 'a.md']);
    await recent.clear();
    expect(await recent.list()).toEqual([]);
  });

  it('does not lose a line when two files are added at once', async () => {
    const { backend } = memoryBackend();
    const recent = createRecentFiles(backend, ops);
    await Promise.all(['a', 'b', 'c', 'd'].map((key) => recent.add(ref(key))));
    expect((await names(recent)).sort()).toEqual(['a.md', 'b.md', 'c.md', 'd.md']);
  });

  it('opens an entry, and reports one whose file has gone as null', async () => {
    const { backend } = memoryBackend();
    const recent = createRecentFiles(backend, ops);
    await recent.add(ref('a'));
    await recent.add(ref('b'));
    const [b, a] = await recent.list();
    const opened = await recent.open(a!.id, null);
    expect(opened?.ref.name).toBe('a.md');
    expect(new TextDecoder().decode(opened!.bytes)).toBe('# a');
    gone.add('b');
    expect(await recent.open(b!.id, null)).toBeNull();
    // The line stays, so the reader can remove it.
    expect(await names(recent)).toEqual(['b.md', 'a.md']);
    gone.clear();
    expect(await recent.open('unknown-id', null)).toBeNull();
  });

  it('gives a file inside the open folder its folder path, and others none', async () => {
    const { backend } = memoryBackend();
    const recent = createRecentFiles(backend, ops);
    await recent.add(ref('repo/x'));
    await recent.add(ref('elsewhere'));
    const [elsewhere, inside] = await recent.list();
    const listing = { folder: { name: 'repo', handle: {} as FileSystemDirectoryHandle }, files: ['x'], truncated: false };
    expect((await recent.open(inside!.id, listing))?.ref.folderPath).toBe('x');
    expect((await recent.open(elsewhere!.id, listing))?.ref.folderPath).toBeUndefined();
    expect((await recent.open(inside!.id, null))?.ref.folderPath).toBeUndefined();
  });

  it('lets a refused access surface as an error rather than a missing file', async () => {
    const { backend } = memoryBackend();
    const refusing: HandleOps = {
      ...ops,
      read: async () => {
        throw new Error('Access to that file was not granted.');
      },
    };
    const recent = createRecentFiles(backend, refusing);
    await recent.add(ref('a'));
    await expect(recent.open((await recent.list())[0]!.id, null)).rejects.toThrow('not granted');
  });
});

// IndexedDB's structured clone keeps a handle usable; a fake handle's methods need the same.
function structuredCloneKeepingFunctions(value: unknown): unknown {
  return Array.isArray(value) ? value.map((item) => ({ ...(item as object) })) : value;
}
