import { describe, expect, it } from 'vitest';
import { DEFAULT_BOUNDS, isFolderRelative, makeSnippet, matchLines, searchFolder, type FileMatches } from '../src/core/search';

const enc = (text: string) => new TextEncoder().encode(text);

function folder(files: Record<string, Uint8Array | string | Error | null>) {
  const reads: string[] = [];
  return {
    reads,
    files: Object.keys(files),
    read: async (path: string) => {
      reads.push(path);
      const value = files[path];
      if (value instanceof Error) throw value;
      return typeof value === 'string' ? enc(value) : (value ?? null);
    },
  };
}

async function run(query: string, f: ReturnType<typeof folder>, extra: Partial<Parameters<typeof searchFolder>[1]> = {}) {
  const found: FileMatches[] = [];
  const summary = await searchFolder(query, { files: f.files, read: f.read, onFile: (m) => found.push(m), ...extra });
  return { found, summary };
}

describe('folder search matching', () => {
  it('groups matches by file in listing order, with zero-based line and column', async () => {
    const f = folder({ 'a.md': 'one\nTwo needle here\nthree', 'b.md': 'nothing', 'docs/c.md': 'needle\n\nneedle again' });
    const { found, summary } = await run('needle', f);
    expect(found.map((x) => x.path)).toEqual(['a.md', 'docs/c.md']);
    expect(found[0]!.matches).toMatchObject([{ line: 1, column: 4, length: 6 }]);
    expect(found[1]!.matches.map((m) => m.line)).toEqual([0, 2]);
    expect(found[0]!.matches[0]!.snippet).toEqual({ before: 'Two ', hit: 'needle', after: ' here' });
    expect(summary).toMatchObject({ filesScanned: 3, matchedLines: 3, stoppedBy: null, cancelled: false });
  });

  it('is case-insensitive and treats the query as literal text, not a pattern', async () => {
    const f = folder({ 'a.md': 'Price (USD) $5.00 [x]\nprice usd', 'b.md': 'a.b' });
    expect((await run('(usd) $5.00', f)).found[0]!.matches).toHaveLength(1);
    expect((await run('[X]', f)).found).toHaveLength(1);
    expect((await run('.', f)).found.map((x) => x.path)).toEqual(['a.md', 'b.md']);
    expect((await run('a.*b', f)).found).toHaveLength(0);
  });

  it('handles Unicode: case folding, astral characters, CJK, and letters that change length', async () => {
    const f = folder({
      'u.md': 'Der \u00dcBER-Test\n\ud83c\udf89 party \u00e9t\u00e9\n\u65e5\u672c\u8a9e\u306e\u898b\u51fa\u3057\n\u0130stanbul',
    });
    const { found } = await run('\u00fcber', f);
    expect(found[0]!.matches[0]).toMatchObject({ line: 0, column: 4, length: 4 });
    const party = (await run('party', f)).found[0]!.matches[0]!;
    // The emoji is two UTF-16 units, so the offset is 3, and the snippet keeps it whole.
    expect(party).toMatchObject({ line: 1, column: 3 });
    expect(party.snippet.before).toBe('\ud83c\udf89 ');
    expect((await run('\u898b\u51fa', f)).found[0]!.matches[0]).toMatchObject({ line: 2, column: 4, length: 2 });
    const istanbul = (await run('\u0130stanbul', f)).found[0]!.matches[0]!;
    expect(istanbul).toMatchObject({ line: 3, column: 0 });
  });

  it('ends lines at LF, CRLF and CR, as the editor does', () => {
    const { matches } = matchLines('a\r\nb x\rc x\nd', 'x', 10);
    expect(matches.map((m) => m.line)).toEqual([1, 2]);
  });

  it('does not split a surrogate pair when it cuts a long line', () => {
    const line = '\ud83c\udf89'.repeat(80) + 'needle' + '\ud83c\udf89'.repeat(80);
    const s = makeSnippet(line, 160, 6);
    expect(s.hit).toBe('needle');
    for (const part of [s.before, s.after]) expect(part.replace(/\u2026/g, '')).not.toMatch(/(^[\udc00-\udfff])|([\ud800-\udbff]$)/);
    expect(s.before.startsWith('\u2026')).toBe(true);
    expect(s.after.endsWith('\u2026')).toBe(true);
  });

  it('returns nothing for an empty query without reading anything', async () => {
    const f = folder({ 'a.md': 'x' });
    const { found, summary } = await run('', f);
    expect(found).toEqual([]);
    expect(f.reads).toEqual([]);
    expect(summary.filesScanned).toBe(0);
  });
});

describe('folder search bounds', () => {
  it('stops at the file limit and says so', async () => {
    const files: Record<string, string> = {};
    for (let i = 0; i < 10; i++) files[`f${i}.md`] = 'hit';
    const f = folder(files);
    const { found, summary } = await run('hit', f, { bounds: { maxFiles: 3 } });
    expect(f.reads).toEqual(['f0.md', 'f1.md', 'f2.md']);
    expect(found).toHaveLength(3);
    expect(summary.stoppedBy).toBe('files');
  });

  it('skips files over the size limit and counts them', async () => {
    const f = folder({ 'big.md': 'hit ' + 'x'.repeat(100), 'small.md': 'hit' });
    const { found, summary } = await run('hit', f, { bounds: { maxFileBytes: 50 } });
    expect(found.map((x) => x.path)).toEqual(['small.md']);
    expect(summary.skippedLarge).toBe(1);
    expect(summary.filesScanned).toBe(1);
  });

  it('applies the size limit to text held in memory too', async () => {
    const summary = await searchFolder('hit', { files: ['a.md'], read: async () => 'hit'.repeat(100), bounds: { maxFileBytes: 50 } });
    expect(summary.skippedLarge).toBe(1);
  });

  it('stops at the result limit across files', async () => {
    const f = folder({ 'a.md': 'hit\nhit\nhit', 'b.md': 'hit\nhit\nhit', 'c.md': 'hit' });
    const { found, summary } = await run('hit', f, { bounds: { maxResults: 4 } });
    expect(found.flatMap((x) => x.matches)).toHaveLength(4);
    expect(found.map((x) => x.path)).toEqual(['a.md', 'b.md']);
    expect(summary.stoppedBy).toBe('results');
    expect(summary.matchedLines).toBe(4);
  });

  it('caps matches per file and flags the file as having more', async () => {
    const f = folder({ 'a.md': 'hit\n'.repeat(20), 'b.md': 'hit' });
    const { found } = await run('hit', f, { bounds: { maxPerFile: 5 } });
    expect(found[0]!.matches).toHaveLength(5);
    expect(found[0]!.more).toBe(true);
    expect(found[1]!.more).toBe(false);
  });

  it('stops when the time budget is spent', async () => {
    let clock = 0;
    const files: Record<string, string> = {};
    for (let i = 0; i < 40; i++) files[`f${i}.md`] = 'hit';
    const f = folder(files);
    const { summary } = await run('hit', f, {
      bounds: { timeBudgetMs: 100 },
      now: () => clock,
      read: async (path) => {
        clock += 30;
        return f.read(path);
      },
    });
    expect(summary.stoppedBy).toBe('time');
    expect(f.reads.length).toBeLessThan(40);
    expect(f.reads.length).toBeGreaterThan(0);
  });

  it('has sane defaults', () => {
    expect(DEFAULT_BOUNDS).toMatchObject({ maxFiles: 2000, maxFileBytes: 2097152, maxResults: 500, maxPerFile: 50, timeBudgetMs: 10000 });
  });
});

describe('folder search skips what it cannot search', () => {
  it('skips binary files (NUL bytes, invalid UTF-8) and unreadable files, with counts', async () => {
    const f = folder({
      'nul.md': new Uint8Array([104, 105, 116, 0, 104, 105, 116]),
      'latin1.md': new Uint8Array([104, 105, 116, 0xe9]),
      'gone.md': new Error('permission denied'),
      'missing.md': null,
      'ok.md': 'hit',
    });
    const { found, summary } = await run('hit', f);
    expect(found.map((x) => x.path)).toEqual(['ok.md']);
    expect(summary).toMatchObject({ skippedBinary: 2, skippedUnreadable: 2, filesScanned: 1 });
  });

  it('strips a UTF-8 byte order mark so line one still matches from column zero', async () => {
    const f = folder({ 'a.md': new Uint8Array([0xef, 0xbb, 0xbf, ...enc('hit')]) });
    expect((await run('hit', f)).found[0]!.matches[0]).toMatchObject({ line: 0, column: 0 });
  });
});

describe('folder search stays inside the folder', () => {
  it('never reads a path that is not plainly folder-relative', async () => {
    const f = folder({ '../secret.md': 'hit', '/etc/hosts.md': 'hit', 'C:\\x.md': 'hit', 'a\\b.md': 'hit', 'a/../../c.md': 'hit', 'ok/in.md': 'hit' });
    const { found, summary } = await run('hit', f);
    expect(f.reads).toEqual(['ok/in.md']);
    expect(found.map((x) => x.path)).toEqual(['ok/in.md']);
    expect(summary.skippedUnreadable).toBe(5);
  });

  it('classifies paths', () => {
    for (const ok of ['a.md', 'docs/a b.md', '.github/x.md']) expect(isFolderRelative(ok)).toBe(true);
    for (const bad of ['', '/a.md', '../a.md', 'a/./b.md', 'a//b.md', 'a\\b.md', 'C:x.md']) expect(isFolderRelative(bad)).toBe(false);
  });

  it('searches only the listing it is given, so a new folder starts from nothing', async () => {
    const one = folder({ 'a.md': 'hit' });
    const two = folder({ 'b.md': 'miss' });
    await run('hit', one);
    const { found } = await run('hit', two);
    expect(found).toEqual([]);
    expect(two.reads).toEqual(['b.md']);
  });
});

describe('folder search cancellation', () => {
  it('does nothing when already cancelled', async () => {
    const f = folder({ 'a.md': 'hit' });
    const controller = new AbortController();
    controller.abort();
    const { found, summary } = await run('hit', f, { signal: controller.signal });
    expect(f.reads).toEqual([]);
    expect(found).toEqual([]);
    expect(summary.cancelled).toBe(true);
  });

  it('stops between batches and reports nothing from reads that finish after cancelling', async () => {
    const files: Record<string, string> = {};
    for (let i = 0; i < 20; i++) files[`f${i}.md`] = 'hit';
    const f = folder(files);
    const controller = new AbortController();
    const { found, summary } = await run('hit', f, {
      signal: controller.signal,
      read: async (path) => {
        const out = await f.read(path);
        // Cancel while the second batch is in flight.
        if (path === 'f5.md') controller.abort();
        return out;
      },
    });
    expect(summary.cancelled).toBe(true);
    // Batch one (f0-f3) was reported; batch two finished after the abort and was dropped.
    expect(found.map((x) => x.path)).toEqual(['f0.md', 'f1.md', 'f2.md', 'f3.md']);
    expect(f.reads.length).toBe(8);
  });
});
