import { describe, expect, it, vi } from 'vitest';
import { searchFolder } from '../src/core/search';
import { FileTooLargeError, type Listing } from '../src/platform/types';
import { webPlatform } from '../src/platform/web';

// readFolderFile with maxBytes: the browser build refuses on File.size, before arrayBuffer().

function upload(files: Record<string, string>) {
  const entries = new Map<string, File>();
  for (const [path, text] of Object.entries(files)) entries.set(path, new File([text], path.split('/').pop()!));
  const listing: Listing = { folder: { name: 'repo', entries }, files: Object.keys(files), truncated: false };
  return { listing, entries };
}

describe('web readFolderFile maxBytes', () => {
  it('rejects an oversized file as too large without reading it', async () => {
    const { listing, entries } = upload({ 'big.md': 'x'.repeat(100) });
    const spy = vi.spyOn(entries.get('big.md')!, 'arrayBuffer');
    const error = await webPlatform.readFolderFile(listing, 'big.md', { maxBytes: 99 }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(FileTooLargeError);
    expect(error).toMatchObject({ path: 'big.md', size: 100, maxBytes: 99 });
    expect(spy).not.toHaveBeenCalled();
  });

  it('reads a file at the limit', async () => {
    const { listing } = upload({ 'ok.md': 'x'.repeat(100) });
    const opened = await webPlatform.readFolderFile(listing, 'ok.md', { maxBytes: 100 });
    expect(opened.bytes).toHaveLength(100);
  });

  it('reads any size when maxBytes is absent, as before', async () => {
    const { listing, entries } = upload({ 'big.md': 'x'.repeat(5000) });
    const spy = vi.spyOn(entries.get('big.md')!, 'arrayBuffer');
    expect((await webPlatform.readFolderFile(listing, 'big.md')).bytes).toHaveLength(5000);
    expect((await webPlatform.readFolderFile(listing, 'big.md', {})).bytes).toHaveLength(5000);
    expect(spy).toHaveBeenCalledTimes(2);
  });
});

describe('search with a pre-read size refusal', () => {
  it('counts a FileTooLargeError as large, not unreadable, and passes its bound', async () => {
    const { listing, entries } = upload({ 'big.md': 'needle'.repeat(50), 'small.md': 'a needle' });
    const spy = vi.spyOn(entries.get('big.md')!, 'arrayBuffer');
    const bounds: number[] = [];
    const found: string[] = [];
    const summary = await searchFolder('needle', {
      files: listing.files,
      bounds: { maxFileBytes: 100 },
      read: async (path, maxBytes) => {
        bounds.push(maxBytes);
        return (await webPlatform.readFolderFile(listing, path, { maxBytes })).bytes;
      },
      onFile: (f) => found.push(f.path),
    });
    expect(bounds).toEqual([100, 100]);
    expect(found).toEqual(['small.md']);
    expect(summary).toMatchObject({ filesScanned: 1, skippedLarge: 1, skippedUnreadable: 0 });
    expect(spy).not.toHaveBeenCalled();
  });

  it('still counts a plain read error as unreadable', async () => {
    const summary = await searchFolder('x', {
      files: ['a.md'],
      read: async () => {
        throw new Error('nope');
      },
    });
    expect(summary).toMatchObject({ skippedLarge: 0, skippedUnreadable: 1 });
  });
});
