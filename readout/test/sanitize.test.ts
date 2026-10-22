import { describe, expect, it } from 'vitest';
import { renderMarkdown } from '../src/core/render';
import { filterStyle, sanitizeSvg } from '../src/core/sanitize';

// Hostile documents. Several mirror the payloads from MarkText's CVEs.

const render = (md: string) => {
  const div = document.createElement('div');
  div.innerHTML = renderMarkdown(md);
  return div;
};
const html = (md: string) => renderMarkdown(md);

describe('active content is removed', () => {
  it.each([
    ['script tag', '<script>alert(1)</script>'],
    ['event handler', '<img src="x.png" onerror="alert(1)">'],
    ['svg onload', '<svg onload="alert(1)"><circle r="2"/></svg>'],
    ['iframe', '<iframe src="https://evil.example"></iframe>'],
    ['object', '<object data="evil.swf"></object>'],
    ['meta refresh', '<meta http-equiv="refresh" content="0;url=https://evil.example">'],
    ['base', '<base href="https://evil.example/">'],
    ['style element', '<style>body{display:none}</style>'],
  ])('%s', (_name, payload) => {
    const out = html(`text\n\n${payload}\n`);
    expect(out).not.toMatch(/<script|onerror|onload|<iframe|<object|<meta|<base|<style/i);
  });

  it('drops javascript: and data: links', () => {
    const r = render('[a](javascript:alert(1)) [b](data:text/html,x) <a href="vbscript:x">c</a>');
    for (const a of r.querySelectorAll('a')) expect(a.getAttribute('href')).toBeNull();
  });

  it('keeps safe links and adds rel', () => {
    const a = render('[a](https://example.com)').querySelector('a')!;
    expect(a.getAttribute('href')).toBe('https://example.com');
    expect(a.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('escapes a hostile fence language (CVE-2021-29996 shape)', () => {
    const r = render('```"><img src=x onerror=alert(1)>\ncode\n```\n');
    expect(r.querySelector('img')).toBeNull();
    expect(r.innerHTML).not.toContain('onerror');
  });

  it('keeps hostile Mermaid source as text (CVE-2022-24123 shape)', () => {
    const r = render('```mermaid\ngraph TD\nA["<img src=x onerror=alert(1)>"]\n```\n');
    expect(r.querySelector('img')).toBeNull();
    expect(r.querySelector('pre[data-mermaid]')!.textContent).toContain('<img src=x onerror=alert(1)>');
  });

  it('keeps hostile math as text', () => {
    const r = render('$\\href{javascript:alert(1)}{x}$');
    expect(r.querySelector('a')).toBeNull();
    expect(r.querySelector('.math-inline')!.getAttribute('data-tex')).toContain('javascript:');
  });

  it('removes form controls but keeps task checkboxes', () => {
    const r = render('- [ ] task\n\n<form action="https://evil.example"><input type="password" name="p"><button>Go</button></form>\n');
    expect(r.querySelector('form, button, input[type="password"]')).toBeNull();
    expect(r.querySelectorAll('input')).toHaveLength(1);
    expect(r.querySelector('input')!.getAttribute('type')).toBe('checkbox');
  });

  it('removes external references from SVG', () => {
    const r = render('<svg><image href="https://tracker.example/p.png"/><use xlink:href="https://evil.example/x.svg#a"/></svg>');
    expect(r.innerHTML).not.toContain('tracker.example');
    expect(r.innerHTML).not.toContain('evil.example');
  });

  it('cleans Mermaid output again before insertion', () => {
    const out = sanitizeSvg('<svg><style>.a{fill:red}</style><g onclick="alert(1)"><text>ok</text></g><script>alert(1)</script></svg>');
    expect(out).toContain('<style>');
    expect(out).not.toMatch(/onclick|<script/);
  });
});

describe('remote content is never fetched', () => {
  it('turns remote images into placeholders', () => {
    const img = render('![badge](https://tracker.example/pixel.png)').querySelector('img')!;
    expect(img.getAttribute('src')).toBeNull();
    expect(img.dataset.remoteSrc).toBe('https://tracker.example/pixel.png');
  });

  it('marks relative images for the platform to resolve', () => {
    const img = render('![diagram](images/net.png)').querySelector('img')!;
    expect(img.getAttribute('src')).toBeNull();
    expect(img.dataset.localSrc).toBe('images/net.png');
  });

  it('keeps data images', () => {
    const img = render('<img src="data:image/png;base64,iVBORw0KGgo=" alt="x">').querySelector('img')!;
    expect(img.getAttribute('src')).toMatch(/^data:image\/png/);
  });

  it('drops srcset', () => {
    expect(render('<img src="a.png" srcset="https://tracker.example/a.png 2x">').querySelector('img')!.hasAttribute('srcset')).toBe(false);
  });
});

describe('inline styles', () => {
  it.each([
    ['color: red; font-weight: bold', 'color: red; font-weight: bold'],
    ['color: rgb(1, 2, 3)', 'color: rgb(1, 2, 3)'],
    ['background-color: oklch(70% 0.1 120)', 'background-color: oklch(70% 0.1 120)'],
    ['background: url(https://tracker.example/x)', ''],
    ['background-color: url(https://tracker.example/x)', ''],
    ['color: red; background-image: url(x)', 'color: red'],
    ['color: \\75 rl(x)', ''],
    ['position: fixed; top: 0; left: 0; z-index: 9999', ''],
    ['display: block', 'display: block'],
    ['display: -webkit-box', ''],
    ['width: expression(alert(1))', ''],
    ['font-family: "Evil"; color: blue', 'color: blue'],
    ['color: red; @import x', 'color: red'],
  ])('%s', (input, expected) => {
    expect(filterStyle(input)).toBe(expected);
  });

  it('removes a style attribute with nothing left', () => {
    expect(render('<span style="position:fixed">x</span>').querySelector('span')!.hasAttribute('style')).toBe(false);
  });
});
