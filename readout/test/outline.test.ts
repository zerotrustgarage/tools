import { describe, expect, it } from 'vitest';
import { extractOutline, plainHeadingText } from '../src/core/outline';

const flat = (text: string) => extractOutline(text).map((h) => `${h.level}:${h.line}:${h.text}`);

describe('outline extraction', () => {
  it('reads ATX headings with their levels and zero-based lines', () => {
    expect(flat('# One\n\ntext\n\n## Two ##\n###### Six\n####### seven\n#NoSpace')).toEqual(['1:0:One', '2:4:Two', '6:5:Six']);
  });

  it('reads setext headings, including multi-line ones, but not a rule after a blank line', () => {
    const text = 'Title\n=====\n\nSub\n---\n\nfirst line\nsecond line\n---\n\n---\n\nplain';
    expect(flat(text)).toEqual(['1:0:Title', '2:3:Sub', '2:6:first line second line']);
  });

  it('keeps duplicate headings as separate entries on their own lines', () => {
    const items = extractOutline('# Setup\n## Notes\n# Setup\n## Notes\n');
    expect(items.map((h) => h.line)).toEqual([0, 1, 2, 3]);
    expect(items.map((h) => h.text)).toEqual(['Setup', 'Notes', 'Setup', 'Notes']);
  });

  it('handles Unicode headings', () => {
    expect(flat('# Überblick\n## 日本語の見出し\n## Ελληνικά 🎉\nمرحبا\n=====')).toEqual([
      '1:0:Überblick',
      '2:1:日本語の見出し',
      '2:2:Ελληνικά 🎉',
      '1:3:مرحبا',
    ]);
  });

  it('ignores headings in fenced code, indented code, comments and front matter', () => {
    const text = [
      '---',
      'title: x',
      '# not a heading',
      '---',
      '# Real',
      '```md',
      '# in fence',
      '```',
      '~~~~',
      '## tilde fence',
      '~~~',
      '~~~~',
      '    # indented code',
      '<!--',
      '# in comment',
      '-->',
      '<!-- # one line -->',
      '## After',
    ].join('\n');
    expect(flat(text)).toEqual(['1:4:Real', '2:17:After']);
  });

  it('ignores headings inside HTML blocks, which end at a blank line', () => {
    expect(flat('<div>\n# inside html\n</div>\n\n# After\n')).toEqual(['1:4:After']);
    expect(flat('<details>\n<summary>x</summary>\n## hidden\n\n## Shown')).toEqual(['2:4:Shown']);
    expect(flat('<pre>\ncode\n\n# still pre\n</pre>\n# Out')).toEqual(['1:5:Out']);
    expect(flat('<custom-tag>\n# hidden\n\n# Shown')).toEqual(['1:3:Shown']);
    // Inline HTML at the start of a paragraph is not a block.
    expect(flat('<b>Bold</b> title\n=====')).toEqual(['1:0:Bold title']);
  });

  it('skips an empty ATX heading such as "## ##"', () => {
    expect(flat('## ##\n# \n#\n### Real ###')).toEqual(['3:3:Real']);
  });

  it('treats an unclosed front matter fence as a rule, not metadata', () => {
    expect(flat('---\n# Heading')).toEqual(['1:1:Heading']);
  });

  it('counts lines the way the editor does for CRLF and CR files', () => {
    expect(flat('# A\r\n\r\n## B\r\n')).toEqual(['1:0:A', '2:2:B']);
    expect(flat('# A\r\r## B')).toEqual(['1:0:A', '2:2:B']);
  });

  it('returns nothing for a document without headings', () => {
    expect(extractOutline('')).toEqual([]);
    expect(extractOutline('just text\n\n- list\n- items\n')).toEqual([]);
    expect(extractOutline('#\n##   \n')).toEqual([]);
  });

  it('reduces inline Markdown to plain text', () => {
    expect(plainHeadingText('The **bold** [link](http://x) and `code` and ![img](a.png)')).toBe('The bold link and code and img');
    expect(plainHeadingText('snake_case_name and _em_')).toBe('snake_case_name and em');
    expect(plainHeadingText('[[Page#Sec|Label]] and \\*literal\\*')).toBe('Label and *literal*');
  });
});
