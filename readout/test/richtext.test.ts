import { describe, expect, it, vi } from 'vitest';
import designTokens from '../../design-tokens.json';
import { buildRichText, fragmentForRange, tint } from '../src/core/richtext';
import { renderMarkdown } from '../src/core/render';
import { postprocess } from '../src/core/postprocess';
import { writeRichText } from '../src/platform/clipboard';

// RD11: the rendered preview as rich text for Outlook, Teams and Word. A real paste into those
// applications cannot be tested here; these tests hold what the pasted markup must be: safe,
// self-contained, with the styles inline and the palette from design-tokens.json.

const T = designTokens.tokens;

/** The preview's DOM for a Markdown source, as the app builds it. */
async function previewOf(source: string): Promise<HTMLElement> {
  const article = document.createElement('article');
  article.innerHTML = renderMarkdown(source);
  await postprocess(article, { mathOutput: 'htmlAndMathml', dark: false });
  document.body.append(article);
  return article;
}

async function copyAll(source: string, options?: Parameters<typeof buildRichText>[1]) {
  const preview = await previewOf(source);
  const range = document.createRange();
  range.selectNodeContents(preview);
  const out = await buildRichText(range.cloneContents(), options);
  preview.remove();
  return { ...out, dom: new DOMParser().parseFromString(out.html, 'text/html').body };
}

describe('tables, code and callouts keep their look with inline styles', () => {
  it('styles a table inline, with the header shaded and alignment kept', async () => {
    const { dom, html } = await copyAll('| Name | Count |\n| :--- | ---: |\n| a | 1 |\n| b | 2 |\n');
    const table = dom.querySelector('table')!;
    expect(table.getAttribute('style')).toContain('border-collapse: collapse');
    expect(table.getAttribute('style')).toContain(T['light-line-strong']);
    const th = dom.querySelectorAll('th');
    expect(th).toHaveLength(2);
    expect(th[0]!.getAttribute('style')).toContain(`background-color: ${T['light-panel2']}`);
    expect(th[1]!.getAttribute('style')).toContain('text-align: right');
    expect(dom.querySelectorAll('td')).toHaveLength(4);
    expect(dom.querySelector('td')!.getAttribute('style')).toContain('border: 1px solid');
    // Nothing the destination cannot resolve.
    expect(html).not.toMatch(/var\(|color-mix|class=|id=|data-/);
  });

  it('puts a code block in a shaded single-cell table with highlighted tokens coloured inline', async () => {
    const { dom } = await copyAll('```js\nconst x = "hi"; // note\n```\n');
    const cell = dom.querySelector('table td')!;
    expect(cell.getAttribute('bgcolor')).toBe(T['light-panel2']);
    const pre = cell.querySelector('pre')!;
    expect(pre.getAttribute('style')).toContain('Consolas');
    expect(pre.getAttribute('style')).toContain('white-space: pre-wrap');
    expect(pre.textContent).toContain('const x = "hi"; // note');
    const colors = [...pre.querySelectorAll('span')].map((s) => s.getAttribute('style') ?? '');
    expect(colors.some((c) => c.includes(T['light-special']))).toBe(true); // keyword
    expect(colors.some((c) => c.includes(T['light-ok']))).toBe(true); // string
    expect(colors.some((c) => c.includes(T['light-dim2']))).toBe(true); // comment
  });

  it('lists tasks with check boxes and without bullets, and states each property once', async () => {
    const { dom, text } = await copyAll('- [x] done\n- [ ] todo\n\n| a |\n|:-:|\n| 1 |\n');
    expect(dom.querySelector('ul')!.getAttribute('style')).toContain('list-style-type: none');
    expect(dom.querySelector('li')!.textContent).toBe('\u2611 done');
    expect(text).toContain('\u2610 todo');
    for (const node of dom.querySelectorAll('[style]')) {
      const names = (node.getAttribute('style') ?? '').split(';').map((d) => d.split(':')[0]!.trim());
      expect(new Set(names).size, node.getAttribute('style') ?? '').toBe(names.length);
    }
  });

  it('inline code is monospaced and shaded', async () => {
    const { dom } = await copyAll('Use `Get-Item` here.\n');
    const code = dom.querySelector('p code')!;
    expect(code.getAttribute('style')).toContain('Consolas');
    expect(code.getAttribute('style')).toContain(T['light-panel2']);
  });

  it('turns each callout into a tinted table with a coloured edge and a coloured title', async () => {
    const kinds: [string, string][] = [['NOTE', 'light-info'], ['TIP', 'light-ok'], ['IMPORTANT', 'light-special'], ['WARNING', 'light-warn'], ['CAUTION', 'light-crit']];
    for (const [kind, token] of kinds) {
      const { dom, text } = await copyAll(`> [!${kind}]\n> Mind the **gap**.\n`);
      const cell = dom.querySelector('table td')!;
      const color = T[token as keyof typeof T];
      expect(cell.getAttribute('style')).toContain(`border-left: 4px solid ${color}`);
      expect(cell.getAttribute('bgcolor')).toBe(tint(color, 0.1));
      const title = cell.querySelector('p')!;
      expect(title.textContent).toBe(kind[0] + kind.slice(1).toLowerCase());
      expect(title.getAttribute('style')).toContain(`color: ${color}`);
      expect(cell.querySelector('strong')!.textContent).toBe('gap');
      expect(text).toContain('Mind the gap.');
    }
  });

  it('includes a folded callout and a folded details block, open', async () => {
    const { dom, text } = await copyAll('> [!NOTE]- Hidden title\n> Secret body\n\n<details><summary>More</summary>\n\nInside\n\n</details>\n');
    expect(dom.querySelector('details, summary')).toBeNull();
    expect(dom.textContent).toContain('Hidden title');
    expect(dom.textContent).toContain('Secret body');
    expect(dom.textContent).toContain('Inside');
    expect(text).toContain('Secret body');
  });

  it('keeps an author colour span and styles headings, lists, quotes and links', async () => {
    const { dom } = await copyAll('# Head\n\n- one\n- two\n\n> quoted\n\n[site](https://example.com) and <span style="color:#b42332">red</span>\n');
    expect(dom.querySelector('h1')!.getAttribute('style')).toContain(T['light-bright']);
    expect(dom.querySelector('ul')!.getAttribute('style')).toContain('padding-left');
    expect(dom.querySelector('blockquote')!.getAttribute('style')).toContain('border-left: 4px solid');
    expect(dom.querySelector('a')!.getAttribute('href')).toBe('https://example.com');
    expect(dom.querySelector('a')!.getAttribute('style')).toContain(T['light-info']);
    expect(dom.querySelector('span')!.getAttribute('style')).toContain('color: #b42332');
  });

  it('uses only palette colours from design-tokens.json (plus tints of them) for its own styles', async () => {
    const { html } = await copyAll(
      '# H\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n```js\nlet a = 1; // c\n```\n\n> [!WARNING]\n> w\n\n`c` <kbd>K</kbd> <mark>m</mark>\n',
    );
    const palette = new Set(Object.values(T).filter((v) => /^#/.test(v)).map((v) => v.toLowerCase()));
    for (const token of Object.keys(T).filter((k) => /^light-(info|ok|warn|crit|special)$/.test(k)))
      palette.add(tint(T[token as keyof typeof T], 0.1).toLowerCase());
    palette.add(tint(T['light-warn'], 0.25).toLowerCase());
    const used = new Set((html.match(/#[0-9a-fA-F]{6}\b/g) ?? []).map((c) => c.toLowerCase()));
    expect(used.size).toBeGreaterThan(5);
    for (const color of used) expect(palette.has(color), color).toBe(true);
  });
});

describe('what is removed', () => {
  it('strips script, event handlers, javascript links, forms and iframes', async () => {
    const hostile =
      '<script>alert(1)</script>\n\n<img src="data:image/png;base64,AAAA" onerror="alert(2)" alt="ok">\n\n<a href="javascript:alert(3)" onclick="x()">link</a>\n\n<iframe src="https://evil.example/"></iframe>\n\n<form action="https://evil.example"><input name="q"><button>go</button></form>\n\n<p onmouseover="alert(4)" style="position:fixed;color:red;background:url(https://evil.example/x.png)">styled</p>\n';
    const { html, dom } = await copyAll(hostile);
    expect(html).not.toMatch(/<script|onerror|onclick|onmouseover|javascript:|<iframe|<form|<input|<button|evil\.example|position|url\(/i);
    expect(dom.textContent).toContain('styled');
    const styled = [...dom.querySelectorAll('p')].find((p) => p.textContent === 'styled')!;
    expect(styled.getAttribute('style')).toContain('color: red');
  });

  it('strips the same from raw markup passed straight in, not only from the rendered preview', async () => {
    const holder = document.createElement('div');
    holder.innerHTML = '<p onclick="bad()">hi<script>bad()</script><img src="x" onerror="bad()"></p>';
    const range = document.createRange();
    range.selectNodeContents(holder);
    const { html } = await buildRichText(range.cloneContents());
    expect(html).not.toMatch(/<script|onclick|onerror|bad\(\)/);
  });

  it('never emits a fetchable image address for a remote image', async () => {
    const { html, text } = await copyAll('![logo](https://tracker.example/pixel.png "t")\n\n![proto](//tracker.example/p.gif)\n\n<img src="http://tracker.example/a.png" srcset="https://tracker.example/b.png 2x">\n');
    expect(html).not.toMatch(/tracker\.example/);
    expect(html).not.toMatch(/<img/i);
    expect(html).toContain('Remote image not copied');
    expect(text).toContain('Remote image not copied');
  });

  it('keeps a data image and refuses an SVG data image or a blob without a resolver', async () => {
    const png = 'data:image/png;base64,iVBORw0KGgo=';
    const { dom } = await copyAll(`![dot](${png})\n\n<img alt="vec" src="data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=">\n`);
    const imgs = [...dom.querySelectorAll('img')];
    expect(imgs).toHaveLength(1);
    expect(imgs[0]!.getAttribute('src')).toBe(png);
    expect(dom.textContent).toContain('Image not copied');
  });

  it('turns a local image into a data URL through the resolver, and describes it when that fails', async () => {
    const holder = document.createElement('div');
    holder.innerHTML = '<p><img src="blob:null/1" alt="one"> <img src="blob:null/2" alt="two"></p>';
    const range = document.createRange();
    range.selectNodeContents(holder);
    const resolveImage = vi.fn(async (src: string) => (src.endsWith('/1') ? 'data:image/png;base64,AAAA' : null));
    const { html } = await buildRichText(range.cloneContents(), { resolveImage });
    expect(html).toContain('src="data:image/png;base64,AAAA"');
    expect(html).not.toContain('blob:');
    expect(html).toContain('Image not copied: two');
    const without = await buildRichText(range.cloneContents());
    expect(without.html).not.toMatch(/<img|blob:/);
  });

  it('replaces math with its TeX and drops diagrams, task boxes and footnote back-links', async () => {
    const { html, text } = await copyAll('Inline $a^2$ and\n\n$$\nx = 1\n$$\n\n- [x] done\n- [ ] todo\n\nNote[^1]\n\n[^1]: The note.\n');
    expect(html).not.toMatch(/katex|<math|<input|footnote-backref/);
    expect(text).toContain('$a^2$');
    expect(text).toContain('$$x = 1$$');
    expect(text).toContain('☑ done');
    expect(text).toContain('☐ todo');
  });

  it('unwraps links that are not web or mail addresses', async () => {
    const { dom } = await copyAll('[next](other.md) [top](#top) [[Wiki Page]] [web](https://example.com) [mail](mailto:a@example.com)\n');
    const hrefs = [...dom.querySelectorAll('a')].map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual(['https://example.com', 'mailto:a@example.com']);
    expect(dom.textContent).toContain('next');
    expect(dom.textContent).toContain('Wiki Page');
  });
});

describe('plain text', () => {
  it('lays out blocks, lists, tables and code readably', async () => {
    const { text } = await copyAll('# Title\n\nPara with [a link](https://example.com).\n\n1. first\n2. second\n   - nested\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```\ncode line\n```\n\n---\n');
    expect(text).toBe(
      ['Title', 'Para with a link (https://example.com).', '1. first\n2. second\n   - nested', 'A\tB\n1\t2', 'code line', '---'].join('\n\n'),
    );
  });
});

describe('selection', () => {
  it('copies only the selected block and keeps the table it is in', async () => {
    const preview = await previewOf('First paragraph.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n\nLast paragraph.\n');
    const cells = preview.querySelectorAll('td');
    const range = document.createRange();
    range.setStart(cells[0]!, 0);
    range.setEnd(cells[1]!, 1);
    const fragment = fragmentForRange(range, preview);
    expect(fragment.querySelector('table')).not.toBeNull();
    const { html, text } = await buildRichText(fragment);
    expect(html).toContain('<table');
    expect(html).not.toContain('First paragraph');
    expect(html).not.toContain('Last paragraph');
    expect(text).toBe('1\t2');
    preview.remove();
  });

  it('keeps bold and code around text selected inside them, but not the paragraph', async () => {
    const preview = await previewOf('Say **loudly now** please and `run` it.\n');
    const strong = preview.querySelector('strong')!.firstChild!;
    const range = document.createRange();
    range.setStart(strong, 0);
    range.setEnd(strong, 6);
    const { html, text } = await buildRichText(fragmentForRange(range, preview));
    expect(html).toContain('<strong>loudly</strong>');
    expect(html).not.toContain('<p');
    expect(text).toBe('loudly');

    const code = preview.querySelector('code')!.firstChild!;
    range.setStart(code, 0);
    range.setEnd(code, 3);
    const copied = await buildRichText(fragmentForRange(range, preview));
    expect(copied.html).toContain('<code');
    expect(copied.text).toBe('run');
    preview.remove();
  });

  it('does not modify the preview', async () => {
    const preview = await previewOf('# A\n\n> [!TIP]\n> t\n\n```\nx\n```\n');
    const before = preview.innerHTML;
    const range = document.createRange();
    range.selectNodeContents(preview);
    await buildRichText(fragmentForRange(range, preview));
    expect(preview.innerHTML).toBe(before);
    preview.remove();
  });
});

describe('tint', () => {
  it('blends toward white', () => {
    expect(tint('#000000', 0.1)).toBe('#e6e6e6');
    expect(tint('#245c9a', 1)).toBe('#245c9a');
    expect(tint('#245c9a', 0)).toBe('#ffffff');
  });
});

describe('writeRichText', () => {
  it('writes one clipboard item with text/html and text/plain', async () => {
    const written: Record<string, Blob>[] = [];
    class FakeItem {
      constructor(public data: Record<string, Blob>) {
        written.push(data);
      }
    }
    vi.stubGlobal('ClipboardItem', FakeItem);
    const write = vi.fn(async () => undefined);
    vi.stubGlobal('navigator', { ...navigator, clipboard: { write } });
    await writeRichText('<b>x</b>', 'x');
    expect(write).toHaveBeenCalledOnce();
    expect(Object.keys(written[0]!).sort()).toEqual(['text/html', 'text/plain']);
    expect(await written[0]!['text/html']!.text()).toBe('<b>x</b>');
    expect(await written[0]!['text/plain']!.text()).toBe('x');
    vi.unstubAllGlobals();
  });

  it('falls back to a copy event carrying both formats when the async API refuses', async () => {
    vi.stubGlobal('ClipboardItem', class {});
    vi.stubGlobal('navigator', { ...navigator, clipboard: { write: vi.fn(async () => Promise.reject(new Error('denied'))) } });
    const data = new Map<string, string>();
    document.execCommand = vi.fn(() => {
      const event = new Event('copy', { cancelable: true }) as ClipboardEvent;
      Object.defineProperty(event, 'clipboardData', { value: { setData: (t: string, v: string) => data.set(t, v) } });
      document.dispatchEvent(event);
      return event.defaultPrevented;
    });
    await writeRichText('<i>y</i>', 'y');
    expect([...data]).toEqual([['text/html', '<i>y</i>'], ['text/plain', 'y']]);
    vi.unstubAllGlobals();
  });

  it('rejects when nothing can copy', async () => {
    vi.stubGlobal('ClipboardItem', undefined);
    document.execCommand = vi.fn(() => false);
    await expect(writeRichText('a', 'a')).rejects.toThrow();
    vi.unstubAllGlobals();
  });
});
