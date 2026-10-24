import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ExternalChangeWatcher,
  decide,
  diffFiles,
  findRename,
  type Choice,
  type WatchHost,
} from '../src/core/reload';

// External changes: the decisions are pure, and the watcher runs against an in-memory "disk" with
// fake timers. Every scenario below is something another program does to the open file or
// folder (git pull, an agent rewriting a file, a rename, a delete).

const enc = (text: string) => new TextEncoder().encode(text);
const dec = (bytes: Uint8Array) => new TextDecoder().decode(bytes);
const INTERVAL = 100;

class FakeApp {
  disk = new Map<string, Uint8Array<ArrayBuffer>>();
  files: string[] = [];
  current: string | null = null;
  lone = false;
  dirty = false;
  buffer: Uint8Array = new Uint8Array();
  hidden = false;
  open = true;
  answer: Choice = 'mine';
  events: string[] = [];
  asked = 0;
  listCalls = 0;
  readCalls: string[] = [];
  truncated = false;
  /** Runs during the rename read, like a click in the sidebar. */
  duringRename: (() => void) | null = null;
  renameApplied = false;
  reloadThrows = false;
  watcher: ExternalChangeWatcher;

  constructor(initial: Record<string, string>, current: string | null, listEvery = 1) {
    for (const [p, t] of Object.entries(initial)) this.disk.set(p, enc(t));
    this.files = [...this.disk.keys()].sort();
    this.current = current;
    this.watcher = new ExternalChangeWatcher(this.host(), { intervalMs: INTERVAL, listEvery });
    if (current) {
      this.buffer = this.disk.get(current)!;
      this.watcher.rebase(this.buffer);
    }
  }

  host(): WatchHost {
    return {
      active: () => this.open,
      paused: () => this.hidden,
      currentPath: () => (this.lone ? null : this.current),
      hasFile: () => this.current !== null,
      isDirty: () => this.dirty,
      folderFiles: () => (this.lone ? null : this.files),
      listingTruncated: () => this.truncated,
      relist: async () => {
        this.listCalls++;
        const all = [...this.disk.keys()].sort();
        // A truncated listing stops at the cap: here, a.md is beyond it.
        return this.truncated ? { files: all.filter((f) => f !== 'a.md'), truncated: true } : { files: all, truncated: false };
      },
      readCurrent: async () => {
        this.readCalls.push(this.current!);
        return this.disk.get(this.current!) ?? null;
      },
      readFolderFile: async (path) => {
        this.readCalls.push(path);
        return this.disk.get(path) ?? null;
      },
      applyListing: (l) => {
        this.files = l.files;
        this.events.push(`listing:${l.files.length}`);
      },
      reload: (bytes) => {
        if (this.reloadThrows) throw new Error('editor failed');
        this.buffer = bytes;
        this.dirty = false;
        this.watcher.rebase(bytes);
        this.watcher.sync();
        this.events.push(`reload:${dec(bytes)}`);
      },
      resolveConflict: async () => {
        this.asked++;
        return this.answer;
      },
      conflictDeferred: () => this.events.push('deferred'),
      renamed: async (path, stillCurrent) => {
        this.duringRename?.();
        await Promise.resolve();
        if (!stillCurrent()) return;
        this.renameApplied = true;
        this.current = path;
        this.events.push(`renamed:${path}`);
      },
      gone: () => this.events.push('gone'),
    };
  }

  /** Two polls: the first sees a change, the second confirms it is stable. */
  async settle(polls = 2) {
    for (let i = 0; i < polls; i++) await vi.advanceTimersByTimeAsync(INTERVAL);
  }
}

beforeEach(() => vi.useFakeTimers());
afterEach(() => vi.useRealTimers());

describe('decide (pure)', () => {
  const a = enc('a');
  const b = enc('b');
  it('is none when the disk matches the baseline', () => {
    expect(decide({ baseline: a, disk: enc('a'), dirty: true })).toBe('none');
  });
  it('reloads a clean buffer and asks for a dirty one', () => {
    expect(decide({ baseline: a, disk: b, dirty: false })).toBe('reload');
    expect(decide({ baseline: a, disk: b, dirty: true })).toBe('conflict');
  });
  it('does not ask again about a disk version already set aside, but does for a newer one', () => {
    expect(decide({ baseline: a, disk: b, dirty: true, acknowledged: enc('b') })).toBe('none');
    expect(decide({ baseline: a, disk: enc('c'), dirty: true, acknowledged: b })).toBe('conflict');
  });
  it('reports a missing file', () => {
    expect(decide({ baseline: a, disk: null, dirty: false })).toBe('missing');
  });
  it('diffs listings and finds a rename by content', () => {
    expect(diffFiles(['a.md', 'b.md'], ['b.md', 'c.md'])).toEqual({ added: ['c.md'], removed: ['a.md'] });
    expect(findRename(a, [{ path: 'x.md', bytes: b }, { path: 'y.md', bytes: enc('a') }])).toBe('y.md');
    expect(findRename(a, [{ path: 'x.md', bytes: b }])).toBeNull();
  });
});

describe('clean buffer', () => {
  it('reloads automatically once the change is stable, and not before', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.watcher.start();
    app.disk.set('a.md', enc('two'));
    await app.settle(1);
    expect(app.events).toEqual([]);
    await app.settle(1);
    expect(app.events).toEqual(['reload:two']);
    expect(app.asked).toBe(0);
    await app.settle(3);
    expect(app.events).toEqual(['reload:two']);
  });

  it('does nothing when the bytes are unchanged (an mtime-only touch)', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.watcher.start();
    app.disk.set('a.md', enc('one'));
    await app.settle(5);
    expect(app.events).toEqual([]);
  });

  it('replace: an atomic replace that is briefly missing still ends in one reload', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md', 1);
    app.watcher.start();
    app.disk.delete('a.md'); // temp file renamed over: gone for less than a poll...
    app.disk.set('a.md', enc('two')); // ...and back with new content
    await app.settle(2);
    expect(app.events).toEqual(['reload:two']);
  });
});

describe('dirty buffer', () => {
  it('keep mine: the buffer is untouched and the same disk version is not asked about again', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.dirty = true;
    app.answer = 'mine';
    app.watcher.start();
    app.disk.set('a.md', enc('theirs'));
    await app.settle(2);
    expect(app.asked).toBe(1);
    expect(app.events).toEqual(['deferred']);
    expect(dec(app.buffer)).toBe('one');
    await app.settle(5);
    expect(app.asked).toBe(1);
    // A newer disk version asks again.
    app.disk.set('a.md', enc('theirs 2'));
    await app.settle(2);
    expect(app.asked).toBe(2);
  });

  it('load disk version: the disk text replaces the buffer only on that choice', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.dirty = true;
    app.answer = 'disk';
    app.watcher.start();
    app.disk.set('a.md', enc('theirs'));
    await app.settle(2);
    expect(app.events).toEqual(['reload:theirs']);
    expect(app.dirty).toBe(false);
  });

  it('a canceled confirmation preserves the edits, defers, and review asks again', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.dirty = true;
    app.answer = null;
    app.watcher.start();
    app.disk.set('a.md', enc('theirs'));
    await app.settle(2);
    expect(app.events).toEqual(['deferred']);
    expect(app.dirty).toBe(true);
    expect(dec(app.buffer)).toBe('one');
    await app.settle(4);
    expect(app.asked).toBe(1);
    app.answer = 'disk';
    app.watcher.review();
    await vi.advanceTimersByTimeAsync(0);
    expect(app.asked).toBe(2);
    expect(app.events).toEqual(['deferred', 'reload:theirs']);
  });

  it('matchesBaseline lets a save notice a disk version it has not seen', () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    expect(app.watcher.matchesBaseline(enc('one'))).toBe(true);
    expect(app.watcher.matchesBaseline(enc('other'))).toBe(false);
  });

  it('a save rebases, so the app writing its own file is not an external change', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.dirty = true;
    app.watcher.start();
    app.disk.set('a.md', enc('mine'));
    app.watcher.rebase(enc('mine'));
    app.dirty = false;
    await app.settle(4);
    expect(app.events).toEqual([]);
  });
});

describe('delete and rename', () => {
  it('delete: reports once, keeps the buffer, and a reappearing file is picked up', async () => {
    const app = new FakeApp({ 'a.md': 'one', 'b.md': 'b' }, 'a.md');
    app.dirty = true;
    app.watcher.start();
    app.disk.delete('a.md');
    await app.settle(6);
    expect(app.events.filter((e) => e === 'gone')).toHaveLength(1);
    expect(dec(app.buffer)).toBe('one');
    expect(app.asked).toBe(0);
    app.dirty = false;
    app.disk.set('a.md', enc('back'));
    await app.settle(3);
    expect(app.events).toContain('reload:back');
  });

  it('delete of a lone file (no folder) is reported after it is stable', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.lone = true;
    app.watcher.start();
    app.disk.delete('a.md');
    await app.settle(1);
    expect(app.events).toEqual([]);
    await app.settle(4);
    expect(app.events).toEqual(['gone']);
  });

  it('rename: the buffer follows the file to its new path, clean or dirty', async () => {
    for (const dirty of [false, true]) {
      const app = new FakeApp({ 'a.md': 'one', 'b.md': 'b' }, 'a.md');
      app.dirty = dirty;
      app.watcher.start();
      app.disk.set('c.md', app.disk.get('a.md')!);
      app.disk.delete('a.md');
      await app.settle(3);
      expect(app.events).toContain('renamed:c.md');
      expect(app.events).not.toContain('gone');
      expect(app.asked).toBe(0);
      expect(app.dirty).toBe(dirty);
      app.watcher.stop();
    }
  });

  it('rename with changed content is reported as gone, not guessed', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.watcher.start();
    app.disk.set('c.md', enc('different'));
    app.disk.delete('a.md');
    await app.settle(3);
    expect(app.events).toContain('gone');
    expect(app.events.some((e) => e.startsWith('renamed'))).toBe(false);
  });
});

describe('safety edges', () => {
  it('a reload that throws leaves the baseline alone, so a later change is still seen', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.reloadThrows = true;
    app.watcher.start();
    app.disk.set('a.md', enc('two'));
    await app.settle(2);
    expect(dec(app.buffer)).toBe('one');
    expect(app.watcher.matchesBaseline(enc('two'))).toBe(false);
    app.reloadThrows = false;
    await app.settle(2);
    expect(app.events).toEqual(['reload:two']);
  });

  it('a file beyond a truncated listing is not reported deleted and is still read', async () => {
    const app = new FakeApp({ 'a.md': 'one', 'b.md': 'b' }, 'a.md');
    app.truncated = true;
    app.watcher.start();
    await app.settle(3); // listing lacks a.md but says it is cut off
    expect(app.events.filter((e) => e === 'gone')).toEqual([]);
    app.disk.set('a.md', enc('two'));
    await app.settle(3);
    expect(app.events).toContain('reload:two');
    expect(app.events).not.toContain('gone');
  });

  it('an empty file is not followed to an arbitrary new empty file', () => {
    expect(findRename(enc(''), [{ path: 'new.md', bytes: enc('') }])).toBeNull();
  });
});

describe('burst', () => {
  it('a git-pull-like burst refreshes the tree once and reads only the open file and rename candidates', async () => {
    const app = new FakeApp({ 'a.md': 'one', 'docs/x.md': 'x' }, 'a.md');
    app.watcher.start();
    // Files keep arriving across several polls, and the open file is rewritten twice.
    for (let i = 0; i < 6; i++) {
      for (let j = 0; j < 10; j++) app.disk.set(`docs/new-${i}-${j}.md`, enc('n'));
      app.disk.set('a.md', enc(`rev ${i}`));
      await vi.advanceTimersByTimeAsync(INTERVAL);
    }
    expect(app.events).toEqual([]); // never stable while the burst runs
    await app.settle(3);
    expect(app.events.filter((e) => e.startsWith('listing'))).toEqual(['listing:62']);
    expect(app.events.filter((e) => e.startsWith('reload'))).toEqual(['reload:rev 5']);
    expect(app.readCalls.every((p) => p === 'a.md')).toBe(true);
  });

  it('a burst that deletes the open file reads at most a bounded number of rename candidates', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.watcher.start();
    for (let i = 0; i < 100; i++) app.disk.set(`n${i}.md`, enc('n'));
    app.disk.delete('a.md');
    app.readCalls = [];
    await app.settle(4);
    expect(app.events).toContain('gone');
    expect(app.readCalls.length).toBeLessThanOrEqual(20 + 2);
  });

  it('lists the folder less often than it reads the file', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md', 4);
    app.watcher.start();
    await app.settle(8);
    expect(app.listCalls).toBe(2);
    expect(app.readCalls.length).toBe(8);
  });
});

describe('lifecycle', () => {
  it('start is idempotent and stop leaves no timer behind', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.watcher.start();
    app.watcher.start();
    expect(vi.getTimerCount()).toBe(1);
    app.watcher.stop();
    expect(vi.getTimerCount()).toBe(0);
    expect(app.watcher.running).toBe(false);
    app.disk.set('a.md', enc('two'));
    await app.settle(5);
    expect(app.events).toEqual([]);
    expect(app.readCalls).toEqual([]);
  });

  it('sync follows the host: closing the folder stops polling, opening starts it', () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.watcher.sync();
    expect(vi.getTimerCount()).toBe(1);
    app.open = false;
    app.watcher.sync();
    expect(vi.getTimerCount()).toBe(0);
    app.open = true;
    app.watcher.sync();
    expect(vi.getTimerCount()).toBe(1);
    app.watcher.stop();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('a read still in flight when the folder changes is discarded', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    let release!: (b: Uint8Array) => void;
    const host = app.host();
    host.readCurrent = () => new Promise<Uint8Array | null>((r) => (release = r));
    const watcher = new ExternalChangeWatcher(host, { intervalMs: INTERVAL, listEvery: 1000 });
    const events: string[] = [];
    host.reload = (b) => void events.push(dec(b));
    watcher.rebase(enc('one'));
    watcher.start();
    // Forced, so the two-poll settle cannot hide a missing stale check.
    void watcher.tick(true);
    await vi.advanceTimersByTimeAsync(0);
    watcher.rebase(enc('other file'));
    release(enc('two'));
    await vi.advanceTimersByTimeAsync(INTERVAL * 3);
    expect(events).toEqual([]);
    watcher.stop();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('a rename whose read straddles a document change is not applied to the new document', async () => {
    const app = new FakeApp({ 'a.md': 'one', 'b.md': 'b' }, 'a.md');
    app.watcher.start();
    app.duringRename = () => app.watcher.rebase(enc('b')); // the reader opened b.md
    app.disk.set('c.md', app.disk.get('a.md')!);
    app.disk.delete('a.md');
    await app.settle(3);
    expect(app.renameApplied).toBe(false);
    expect(app.current).toBe('a.md');
  });

  it('pauses while hidden or a dialog is open, then catches up', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    app.watcher.start();
    app.hidden = true;
    app.disk.set('a.md', enc('two'));
    await app.settle(5);
    expect(app.events).toEqual([]);
    app.hidden = false;
    await app.settle(2);
    expect(app.events).toEqual(['reload:two']);
  });

  it('a failing relist or read changes nothing and does not stop polling', async () => {
    const app = new FakeApp({ 'a.md': 'one' }, 'a.md');
    const host = app.host();
    host.relist = async () => {
      throw new Error('folder gone');
    };
    const w = new ExternalChangeWatcher(host, { intervalMs: INTERVAL, listEvery: 1 });
    w.rebase(enc('one'));
    w.start();
    await vi.advanceTimersByTimeAsync(INTERVAL * 4);
    expect(app.events).toEqual([]);
    expect(w.running).toBe(true);
    w.stop();
  });
});
