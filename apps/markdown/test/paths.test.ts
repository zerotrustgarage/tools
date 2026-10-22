import { describe, expect, it } from 'vitest';
import {
  buildTree,
  defaultDocument,
  documentFor,
  resolveInFolder,
  resolveWikiLink,
  skipFolder,
  splitHref,
} from '../src/core/paths';

const files = [
  '.github/CONTRIBUTING.md',
  'README.md',
  'docs/README.md',
  'docs/setup/install.md',
  'docs/setup/Network Plan.md',
  'notes/install.md',
  'vault/Daily/2026-10-20.md',
];

describe('links inside an open folder', () => {
  it('resolves relative, parent and root-relative links', () => {
    expect(resolveInFolder('docs/setup/install.md', 'Network%20Plan.md')).toBe('docs/setup/Network%20Plan.md');
    expect(resolveInFolder('docs/setup/install.md', '../README.md')).toBe('docs/README.md');
    expect(resolveInFolder('docs/setup/install.md', './../../README.md')).toBe('README.md');
    expect(resolveInFolder('docs/setup/install.md', '/notes/install.md')).toBe('notes/install.md');
    expect(resolveInFolder('README.md', 'docs')).toBe('docs');
  });

  it('refuses to leave the folder or name a drive', () => {
    expect(resolveInFolder('README.md', '../outside.md')).toBeNull();
    expect(resolveInFolder('docs/a.md', '../../x.md')).toBeNull();
    expect(resolveInFolder('docs/a.md', 'C:/Windows/x.md')).toBeNull();
    expect(resolveInFolder('docs/a.md', '')).toBeNull();
  });

  it('splits and decodes hrefs, and rejects schemes', () => {
    expect(splitHref('Network%20Plan.md#vlan-10')).toEqual({ path: 'Network Plan.md', fragment: 'vlan-10' });
    expect(splitHref('img.png?raw=true')).toEqual({ path: 'img.png', fragment: '' });
    expect(splitHref('https://example.com')).toBeNull();
    expect(splitHref('javascript:alert(1)')).toBeNull();
    expect(splitHref('//host/share/x.md')).toBeNull();
    expect(splitHref('\\\\host\\share\\x.md')).toBeNull();
  });

  it('opens a folder link as its README, case-insensitively', () => {
    expect(documentFor('docs', files)).toBe('docs/README.md');
    expect(documentFor('readme.md', files)).toBe('README.md');
    expect(documentFor('docs/setup', files)).toBeNull();
    expect(documentFor('src/main.rs', files)).toBeNull();
  });

  it('starts at the root README', () => {
    expect(defaultDocument(files)).toBe('README.md');
    expect(defaultDocument(['docs/index.md', 'docs/a.md'])).toBe('docs/index.md');
    expect(defaultDocument(['b/x.md'])).toBe('b/x.md');
    expect(defaultDocument([])).toBeNull();
  });
});

describe('wiki links', () => {
  it('finds a page by name anywhere, preferring the nearest', () => {
    expect(resolveWikiLink('docs/setup/install.md', 'Network Plan', files)).toBe('docs/setup/Network Plan.md');
    expect(resolveWikiLink('docs/setup/Network Plan.md', 'install', files)).toBe('docs/setup/install.md');
    expect(resolveWikiLink('notes/x.md', 'install', files)).toBe('notes/install.md');
    expect(resolveWikiLink('README.md', '2026-10-20', files)).toBe('vault/Daily/2026-10-20.md');
    expect(resolveWikiLink('README.md', 'missing', files)).toBeNull();
  });

  it('accepts a path form', () => {
    expect(resolveWikiLink('README.md', 'docs/setup/install', files)).toBe('docs/setup/install.md');
  });
});

describe('folder listing rules', () => {
  it('skips version control, dependencies and build output but keeps .github', () => {
    for (const name of ['.git', 'node_modules', 'target', 'dist', '.venv', '.obsidian', 'bin', 'obj']) expect(skipFolder(name)).toBe(true);
    for (const name of ['.github', 'docs', 'src', 'Notes']) expect(skipFolder(name)).toBe(false);
  });

  it('builds a tree with folders first', () => {
    const tree = buildTree(files);
    expect(tree.map((n) => n.name)).toEqual(['.github', 'docs', 'notes', 'vault', 'README.md']);
    const docs = tree.find((n) => n.name === 'docs')!;
    expect(docs.children.map((n) => n.name)).toEqual(['setup', 'README.md']);
  });
});
