import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { renderMarkdown } from '../src/core/render';

const report = readFileSync(join(__dirname, 'corpus', 'ai-report.md'), 'utf8');

function dom(html: string): HTMLElement {
  const div = document.createElement('div');
  div.innerHTML = html;
  return div;
}

describe('rendering what AI tools produce', () => {
  const root = dom(renderMarkdown(report));

  it('renders tables with alignment and inline colour', () => {
    const table = root.querySelector('table')!;
    expect(table.querySelectorAll('tbody tr')).toHaveLength(3);
    expect(table.querySelector('th')!.getAttribute('style')).toBe('text-align: left');
    const met = table.querySelector('span[style]')!;
    expect(met.textContent).toBe('Met');
    expect(met.getAttribute('style')).toBe('color: green');
    expect(table.querySelector('s')!.textContent).toBe('Not met');
  });

  it('keeps colour spans in paragraphs', () => {
    const span = [...root.querySelectorAll('p span')].find((s) => s.textContent === 'two critical findings')!;
    expect(span.getAttribute('style')).toBe('color: #b42332; font-weight: 600');
  });

  it('renders task lists, including nested ones', () => {
    const boxes = root.querySelectorAll<HTMLInputElement>('input.task-list-item-checkbox');
    expect(boxes).toHaveLength(4);
    expect(boxes[0]!.hasAttribute('checked')).toBe(true);
    expect(boxes[1]!.hasAttribute('checked')).toBe(false);
    expect([...boxes].every((b) => b.disabled)).toBe(true);
    expect(root.querySelector('ul.contains-task-list')).not.toBeNull();
  });

  it('renders GitHub alerts without the marker text', () => {
    const alerts = root.querySelectorAll('.markdown-alert');
    expect([...alerts].map((a) => a.className)).toEqual([
      'markdown-alert markdown-alert-note',
      'markdown-alert markdown-alert-warning',
      'markdown-alert markdown-alert-caution',
    ]);
    expect(alerts[0]!.textContent).not.toContain('[!NOTE]');
    expect(alerts[0]!.querySelector('.markdown-alert-title')!.textContent).toBe('Note');
    expect(alerts[0]!.querySelector('strong')!.textContent).toBe('2026-10-20');
  });

  it('marks math for KaTeX and leaves money alone', () => {
    const inline = root.querySelectorAll('.math-inline');
    expect(inline).toHaveLength(1);
    expect(inline[0]!.getAttribute('data-tex')).toBe('R = \\sum_{i=1}^{n} w_i s_i');
    expect(root.textContent).toContain('$5 to $10 per seat');
    const display = root.querySelector('.math-display')!;
    expect(display.getAttribute('data-tex')).toContain('\\frac{R}{1 + e^{-k(t - t_0)}}');
  });

  it('hands Mermaid source to the diagram renderer as text', () => {
    const pre = root.querySelector('pre[data-mermaid]')!;
    expect(pre.textContent).toContain('flowchart LR');
    expect(pre.children).toHaveLength(0);
  });

  it('highlights code', () => {
    expect(root.querySelector('code.language-json .hljs-attr')).not.toBeNull();
    expect(root.querySelector('code.language-powershell .hljs-built_in')).not.toBeNull();
  });

  it('renders footnotes, autolinks, details and nested ordered lists', () => {
    expect(root.querySelector('.footnotes')!.textContent).toContain('legacy VPN concentrator');
    expect(root.querySelector('a[href="https://example.com"]')).not.toBeNull();
    expect(root.querySelector('a[href="mailto:security@example.com"]')).not.toBeNull();
    expect(root.querySelector('details summary')!.textContent).toBe('Raw data');
    expect(root.querySelector('ol ol li')!.textContent).toBe('Nested step');
  });

  it('gives headings GitHub-style ids and blocks source line numbers', () => {
    expect(root.querySelector('h2')!.id).toBe('executive-summary');
    expect(root.querySelector('h1')!.getAttribute('data-line')).toBe('0');
    expect(root.querySelector('table')!.getAttribute('data-line')).not.toBeNull();
  });

  it('deduplicates heading ids', () => {
    const r = dom(renderMarkdown('# A\n\n# A\n'));
    expect([...r.querySelectorAll('h1')].map((h) => h.id)).toEqual(['a', 'a-1']);
  });

  it('keeps an alert whose content starts on the next paragraph', () => {
    const r = dom(renderMarkdown('> [!TIP]\n>\n> Body text\n'));
    expect(r.querySelector('.markdown-alert-tip p:not(.markdown-alert-title)')!.textContent).toBe('Body text');
  });

  it('does not treat a quote that mentions a marker mid-line as an alert', () => {
    expect(dom(renderMarkdown('> See [!NOTE] here\n')).querySelector('.markdown-alert')).toBeNull();
  });

  it('renders ```math fences and single-line $$ blocks', () => {
    const r = dom(renderMarkdown('```math\nx^2\n```\n\n$$ y^2 $$\n'));
    expect([...r.querySelectorAll('.math-display')].map((m) => m.getAttribute('data-tex')?.trim())).toEqual(['x^2', 'y^2']);
  });
});

describe('docs folders and vaults', () => {
  it('shows front matter as a table instead of a rule and heading', () => {
    const r = dom(renderMarkdown('---\ntitle: "Firewall standard"\ntags:\n  - network\n  - cmmc\nowner: SecOps\n---\n\n# Body\n'));
    const table = r.querySelector('table.front-matter')!;
    expect([...table.querySelectorAll('th')].map((t) => t.textContent)).toEqual(['title', 'tags', 'owner']);
    expect(table.querySelector('td')!.textContent).toBe('Firewall standard');
    expect(r.querySelector('hr')).toBeNull();
    expect(r.querySelector('h1')!.textContent).toBe('Body');
  });

  it('leaves a document that merely starts with a rule alone', () => {
    const r = dom(renderMarkdown('---\n\nText\n'));
    expect(r.querySelector('table.front-matter')).toBeNull();
    expect(r.querySelector('hr')).not.toBeNull();
  });

  it('never evaluates front matter', () => {
    const r = dom(renderMarkdown('---\nx: <img src=x onerror=alert(1)>\n---\n'));
    expect(r.querySelector('img')).toBeNull();
    expect(r.querySelector('td')!.textContent).toBe('<img src=x onerror=alert(1)>');
  });

  it('renders wiki links and image embeds', () => {
    const r = dom(renderMarkdown('See [[Network Plan|the plan]] and [[install#Prereqs]].\n\n![[diagram.png]]\n'));
    const links = [...r.querySelectorAll<HTMLAnchorElement>('a.wikilink')];
    expect(links.map((a) => [a.dataset.wikilink, a.textContent])).toEqual([
      ['Network Plan', 'the plan'],
      ['install#Prereqs', 'install'],
    ]);
    expect(r.querySelector('img')!.dataset.localSrc).toBe('diagram.png');
  });

  it('keeps a hostile wiki link inert', () => {
    const r = dom(renderMarkdown('[[x" onclick="alert(1)|y]]'));
    const a = r.querySelector('a')!;
    expect(a.hasAttribute('onclick')).toBe(false);
    expect(a.dataset.wikilink).toBe('x" onclick="alert(1)');
  });

  it('falls back to the <img> of a <picture>', () => {
    const r = dom(renderMarkdown('<picture><source media="(prefers-color-scheme: dark)" srcset="logo-dark.png"><img alt="Logo" src="logo.png"></picture>\n'));
    expect(r.querySelector('source')).toBeNull();
    expect(r.querySelector('img')!.dataset.localSrc).toBe('logo.png');
  });
});
