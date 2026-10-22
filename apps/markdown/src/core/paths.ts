// Path handling for links and images inside an opened folder (a repository, a docs tree or a
// vault). Paths are folder-relative and '/'-separated. The desktop app resolves paths in Rust
// (src-tauri/src/lib.rs, resolve_target); the browser build uses these functions, which follow
// the same rules: a leading '/' means the folder root, as on GitHub, and nothing may leave the
// folder.

/** Folders a listing skips: version control, dependencies and build output. */
export const SKIPPED_FOLDERS = new Set([
  '.git', '.hg', '.svn', 'node_modules', 'bower_components', 'target', 'dist', 'build', 'out',
  '.venv', 'venv', '__pycache__', '.tox', '.next', '.nuxt', '.cache', '.idea', '.vs', 'bin', 'obj',
  '.terraform', '.obsidian', '.trash',
]);
export const KEPT_HIDDEN_FOLDERS = new Set(['.github', '.gitlab']);
export const LISTED_EXTENSIONS = /\.(md|markdown|mdown|mkd|mkdn)$/i;

export function skipFolder(name: string): boolean {
  if (name.startsWith('.') && !KEPT_HIDDEN_FOLDERS.has(name)) return true;
  return SKIPPED_FOLDERS.has(name.toLowerCase());
}

/** Splits an href into its decoded path and its fragment; null for anything not a path. */
export function splitHref(href: string): { path: string; fragment: string } | null {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//') || href.startsWith('\\\\')) return null;
  const hash = href.indexOf('#');
  const beforeHash = hash < 0 ? href : href.slice(0, hash);
  const fragment = hash < 0 ? '' : href.slice(hash + 1);
  const query = beforeHash.indexOf('?');
  const raw = query < 0 ? beforeHash : beforeHash.slice(0, query);
  let path: string;
  try {
    path = decodeURIComponent(raw);
  } catch {
    path = raw;
  }
  let decodedFragment: string;
  try {
    decodedFragment = decodeURIComponent(fragment);
  } catch {
    decodedFragment = fragment;
  }
  return { path, fragment: decodedFragment };
}

export const dirname = (path: string) => (path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '');
export const basename = (path: string) => path.slice(path.lastIndexOf('/') + 1);

/**
 * Resolves target against the folder-relative path of the current document. Returns the
 * folder-relative result, or null when it would leave the folder or is not a relative path.
 */
export function resolveInFolder(fromFile: string, target: string): string | null {
  if (!target || /^[a-z]:/i.test(target) || target.includes('\\\\')) return null;
  const parts = (target.startsWith('/') ? [] : dirname(fromFile).split('/')).filter(Boolean);
  for (const segment of target.replace(/\\/g, '/').split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      if (parts.length === 0) return null;
      parts.pop();
    } else parts.push(segment);
  }
  return parts.join('/');
}

/** A link to a folder opens its README or index, as on GitHub. */
export function documentFor(path: string, files: readonly string[]): string | null {
  const lower = new Map(files.map((f) => [f.toLowerCase(), f]));
  const exact = lower.get(path.toLowerCase());
  if (exact) return exact;
  const prefix = path ? `${path}/` : '';
  for (const name of ['readme.md', 'index.md', 'readme.markdown']) {
    const hit = lower.get(`${prefix}${name}`.toLowerCase());
    if (hit) return hit;
  }
  return null;
}

/**
 * Obsidian-style [[wikilink]] resolution: by file name anywhere in the folder, preferring the
 * closest match to the current document, with or without the .md extension.
 */
export function resolveWikiLink(fromFile: string, target: string, files: readonly string[]): string | null {
  const wanted = target.trim().replace(/\\/g, '/');
  if (!wanted) return null;
  const withExt = LISTED_EXTENSIONS.test(wanted) ? wanted : `${wanted}.md`;
  if (wanted.includes('/')) {
    const relative = resolveInFolder(fromFile, withExt);
    const fromRoot = resolveInFolder('', withExt);
    return documentFor(relative ?? '', files) ?? documentFor(fromRoot ?? '', files);
  }
  const name = withExt.toLowerCase();
  const candidates = files.filter((f) => basename(f).toLowerCase() === name);
  if (candidates.length === 0) return null;
  const here = dirname(fromFile);
  candidates.sort((a, b) => distance(here, dirname(a)) - distance(here, dirname(b)) || a.length - b.length);
  return candidates[0]!;
}

function distance(a: string, b: string): number {
  const pa = a.split('/').filter(Boolean);
  const pb = b.split('/').filter(Boolean);
  let common = 0;
  while (common < pa.length && common < pb.length && pa[common] === pb[common]) common++;
  return pa.length - common + (pb.length - common);
}

/** The README a freshly opened folder shows first. */
export function defaultDocument(files: readonly string[]): string | null {
  return documentFor('', files) ?? documentFor('docs', files) ?? files.find((f) => !f.includes('/')) ?? files[0] ?? null;
}

export interface TreeNode {
  name: string;
  path: string;
  children: TreeNode[];
  file: boolean;
}

/** Folder tree for the sidebar: folders first, then files, each alphabetical. */
export function buildTree(files: readonly string[]): TreeNode[] {
  const root: TreeNode = { name: '', path: '', children: [], file: false };
  for (const file of files) {
    let node = root;
    const segments = file.split('/');
    segments.forEach((segment, i) => {
      const leaf = i === segments.length - 1;
      const path = segments.slice(0, i + 1).join('/');
      let child = node.children.find((c) => c.name === segment && c.file === leaf);
      if (!child) {
        child = { name: segment, path, children: [], file: leaf };
        node.children.push(child);
      }
      node = child;
    });
  }
  const sort = (nodes: TreeNode[]) => {
    nodes.sort((a, b) => Number(a.file) - Number(b.file) || a.name.localeCompare(b.name, undefined, { sensitivity: 'base', numeric: true }));
    nodes.forEach((n) => sort(n.children));
  };
  sort(root.children);
  return root.children;
}
