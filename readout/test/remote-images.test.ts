import { describe, expect, it } from 'vitest';
import { postprocess } from '../src/core/postprocess';
import { renderMarkdown } from '../src/core/render';
import { describeRemoteImages, summarizeRemoteImages } from '../src/core/remote-images';

async function render(markdown: string): Promise<HTMLElement> {
  const root = document.createElement('div');
  root.innerHTML = renderMarkdown(markdown);
  await postprocess(root, { mathOutput: 'htmlAndMathml', dark: false });
  return root;
}

describe('remote image notice (RD14)', () => {
  it('counts every remote image once and lists each host once', async () => {
    const root = await render(
      [
        '![a](https://cdn.example/a.png)',
        '![b](https://cdn.example/b.png)',
        '![c](http://old.example/c.gif)',
        '<img src="//proto.example/d.png">',
        '![local](images/x.png)',
        '![inline](data:image/png;base64,AAAA)',
      ].join('\n\n'),
    );
    const summary = summarizeRemoteImages(root);
    expect(summary.count).toBe(4);
    expect(summary.hosts).toEqual(['cdn.example', 'old.example', 'proto.example']);
    expect(summary.insecure).toBe(1);
  });

  it('says nothing for a document without remote images', async () => {
    const root = await render('# Plain\n\n![local](images/x.png)\n\n[a link](https://example.com)\n');
    expect(summarizeRemoteImages(root).count).toBe(0);
    expect(describeRemoteImages(summarizeRemoteImages(root))).toBeNull();
  });

  it('words the note in the singular and the plural and names the hosts', async () => {
    const one = describeRemoteImages(summarizeRemoteImages(await render('![a](https://t.example/p.png)')))!;
    expect(one.text).toBe('1 remote image not loaded');
    expect(one.title).toContain('From: t.example');
    expect(one.title).not.toContain('plain http');
    const two = describeRemoteImages(summarizeRemoteImages(await render('![a](https://t.example/p.png)\n\n![b](http://u.example/q.png)')))!;
    expect(two.text).toBe('2 remote images not loaded');
    expect(two.title).toContain('1 use plain http.');
  });

  it('caps the host list', () => {
    const hosts = Array.from({ length: 11 }, (_, i) => `h${i}.example`);
    const note = describeRemoteImages({ count: 11, hosts, insecure: 0 })!;
    expect(note.title).toContain('h7.example and 3 more');
    expect(note.title).not.toContain('h8.example');
  });

  it('never gives an image an address to fetch', async () => {
    const root = await render('![a](https://t.example/p.png)\n\n<img src="https://t.example/q.png" srcset="https://t.example/r.png 2x">');
    expect(root.querySelectorAll('img').length).toBe(0);
    expect(root.innerHTML).not.toMatch(/\ssrc(set)?=/i);
  });
});
