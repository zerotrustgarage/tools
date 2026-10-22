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

describe('callouts and admonitions', () => {
  const callout = (md: string) => dom(renderMarkdown(md)).querySelector<HTMLElement>('.markdown-alert')!;
  const title = (el: Element) => el.querySelector('.markdown-alert-title')!.textContent;
  const body = (el: Element) =>
    [...el.children]
      .filter((c) => !c.classList.contains('markdown-alert-title'))
      .map((c) => c.textContent)
      .join('|');

  it('renders Obsidian callouts with their type, alias colour and custom title', () => {
    const c = callout('> [!danger] Do **not** reboot\n> The cluster is draining.\n');
    expect(c.tagName).toBe('DIV');
    expect(c.className).toBe('markdown-alert markdown-alert-caution');
    expect(c.dataset.callout).toBe('danger');
    expect(title(c)).toBe('Do not reboot');
    expect(c.querySelector('.markdown-alert-title strong')!.textContent).toBe('not');
    expect(body(c)).toBe('The cluster is draining.');
    expect(c.textContent).not.toContain('[!danger]');
  });

  it('gives an untitled callout its type as the title and an unknown type the note style', () => {
    expect(title(callout('> [!TLDR]\n> Short.\n'))).toBe('Tldr');
    const custom = callout('> [!runbook-step]\n> Text\n');
    expect(custom.className).toBe('markdown-alert markdown-alert-note');
    expect(custom.dataset.callout).toBe('runbook-step');
    expect(callout('> [!quote]\n> Words\n').className).toContain('markdown-alert-quote');
  });

  it('folds Obsidian callouts marked + or -', () => {
    const closed = callout('> [!faq]- Why is the VPN slow?\n> Split tunnelling is off.\n');
    expect(closed.tagName).toBe('DETAILS');
    expect(closed.hasAttribute('open')).toBe(false);
    expect(closed.querySelector('summary.markdown-alert-title')!.textContent).toBe('Why is the VPN slow?');
    expect(body(closed)).toBe('Split tunnelling is off.');
    const open = callout('> [!tip]+\n> Shown\n');
    expect(open.tagName).toBe('DETAILS');
    expect(open.hasAttribute('open')).toBe(true);
    expect(title(open)).toBe('Tip');
  });

  it('nests callouts', () => {
    const outer = callout('> [!note] Outer\n> > [!warning] Inner\n> > Careful\n');
    const inner = outer.querySelector<HTMLElement>('.markdown-alert')!;
    expect(inner.dataset.callout).toBe('warning');
    expect(title(inner)).toBe('Inner');
  });

  it('renders MkDocs admonitions with an indented body', () => {
    const r = dom(renderMarkdown('!!! warning "Change window"\n    Saturday **only**.\n\n    - one\n    - two\n\nAfter\n'));
    const c = r.querySelector<HTMLElement>('.markdown-alert')!;
    expect(c.className).toBe('markdown-alert markdown-alert-warning');
    expect(title(c)).toBe('Change window');
    expect(c.querySelector('p:not(.markdown-alert-title) strong')!.textContent).toBe('only');
    expect(c.querySelectorAll('li')).toHaveLength(2);
    expect(c.getAttribute('data-line')).toBe('0');
    expect(r.lastElementChild!.textContent).toBe('After');
    expect(r.lastElementChild!.closest('.markdown-alert')).toBeNull();
  });

  it('folds MkDocs ??? admonitions and honours an empty title', () => {
    const closed = callout('??? note\n    Hidden\n');
    expect(closed.tagName).toBe('DETAILS');
    expect(closed.hasAttribute('open')).toBe(false);
    expect(title(closed)).toBe('Note');
    expect(callout('???+ example\n    Shown\n').hasAttribute('open')).toBe(true);
    const bare = callout('!!! info ""\n    No title bar\n');
    expect(bare.querySelector('.markdown-alert-title')).toBeNull();
    expect(bare.textContent!.trim()).toBe('No title bar');
  });

  it('keeps indentation rules: a deeper indent is code, and !!! cannot interrupt a paragraph', () => {
    expect(callout('!!! note\n        code here\n').querySelector('pre code')!.textContent).toBe('code here\n');
    expect(dom(renderMarkdown('Text\n!!! note\n    body\n')).querySelector('.markdown-alert')).toBeNull();
  });

  it('renders Docusaurus admonitions in both title forms, nested with more colons', () => {
    const c = callout(':::tip[Pro tip]\nUse **SSO**.\n:::\n');
    expect(c.className).toBe('markdown-alert markdown-alert-tip');
    expect(title(c)).toBe('Pro tip');
    expect(body(c)).toBe('Use SSO.');
    expect(title(callout(':::danger Take care\nBody\n:::\n'))).toBe('Take care');
    expect(title(callout(':::info\nBody\n:::\n'))).toBe('Info');

    const r = dom(renderMarkdown('::::note Outer\n:::warning\nInner\n:::\nStill outer\n::::\n\nAfter\n'));
    const outer = r.querySelector<HTMLElement>('.markdown-alert')!;
    expect(outer.querySelector<HTMLElement>('.markdown-alert')!.dataset.callout).toBe('warning');
    expect(body(outer)).toContain('Still outer');
    expect(r.lastElementChild!.textContent).toBe('After');
  });

  it('leaves other ::: blocks alone', () => {
    const r = dom(renderMarkdown('::: columns\nText\n:::\n'));
    expect(r.querySelector('.markdown-alert')).toBeNull();
    expect(r.textContent).toContain('::: columns');
  });

  it('keeps callout titles inert', () => {
    const c = callout('> [!note] <img src=x onerror=alert(1)> title\n> body\n');
    expect(c.querySelector('img')!.hasAttribute('onerror')).toBe(false);
    const m = callout('!!! note "<script>alert(1)</script>"\n    body\n');
    expect(m.querySelector('script')).toBeNull();
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
