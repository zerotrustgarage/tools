import { buildTree, type TreeNode } from '../core/paths';
import type { Listing } from '../platform/types';
import { el, icon } from './dom';

// The folder sidebar: a tree of the open folder's Markdown files, with a filter. Folders on the
// path to the current file open automatically.

export class Sidebar {
  readonly element = el('aside', { class: 'sidebar', 'aria-label': 'Folder', hidden: '' });
  private readonly title = el('span', { class: 'sidebar-title' });
  private readonly filter = el('input', { type: 'search', class: 'sidebar-filter', placeholder: 'Filter files', 'aria-label': 'Filter files', spellcheck: 'false' });
  private readonly tree = el('nav', { class: 'tree' });
  private readonly count = el('div', { class: 'sidebar-count' });
  private listing: Listing | null = null;
  private current: string | undefined;

  constructor(
    private readonly onOpen: (folderPath: string) => void,
    onClose: () => void,
  ) {
    const close = el('button', { type: 'button', class: 'icon-button', title: 'Close folder', 'aria-label': 'Close folder' }, icon('close'));
    close.addEventListener('click', onClose);
    this.filter.addEventListener('input', () => this.render());
    this.element.append(el('div', { class: 'sidebar-head' }, icon('folder'), this.title, close), this.filter, this.tree, this.count);
  }

  show(listing: Listing | null): void {
    this.listing = listing;
    this.filter.value = '';
    this.element.hidden = listing === null;
    this.title.textContent = listing?.folder.name ?? '';
    this.title.title = listing?.folder.root ?? listing?.folder.name ?? '';
    this.render();
  }

  select(folderPath: string | undefined): void {
    this.current = folderPath;
    for (const node of this.tree.querySelectorAll<HTMLElement>('[data-path]')) {
      const active = node.dataset.path === folderPath;
      node.classList.toggle('active', active);
      if (active) {
        node.setAttribute('aria-current', 'page');
        for (let d = node.closest('details'); d; d = d.parentElement?.closest('details') ?? null) d.open = true;
        node.scrollIntoView({ block: 'nearest' });
      } else node.removeAttribute('aria-current');
    }
  }

  private fileButton(path: string, label: string): HTMLButtonElement {
    const b = el('button', { type: 'button', class: 'tree-file', 'data-path': path, title: path }, icon('file'), el('span', {}, label));
    b.addEventListener('click', () => this.onOpen(path));
    return b;
  }

  private renderNodes(nodes: TreeNode[], depth: number): HTMLElement[] {
    return nodes.map((node) => {
      if (node.file) return this.fileButton(node.path, node.name);
      const details = el('details', { class: 'tree-folder' }) as HTMLDetailsElement;
      // Top-level docs folders start open; deeper ones open on demand or when they hold the
      // current file.
      details.open = depth === 0 && /^(docs?|documentation|\.github)$/i.test(node.name);
      details.append(el('summary', { title: node.path }, icon('chevron'), el('span', {}, node.name)), ...this.renderNodes(node.children, depth + 1));
      return details;
    });
  }

  private render(): void {
    const files = this.listing?.files ?? [];
    const query = this.filter.value.trim().toLowerCase();
    if (query) {
      const hits = files.filter((f) => f.toLowerCase().includes(query));
      this.tree.replaceChildren(...hits.map((f) => this.fileButton(f, f)));
      this.count.textContent = `${hits.length} of ${files.length} files`;
    } else {
      this.tree.replaceChildren(...this.renderNodes(buildTree(files), 0));
      this.count.textContent = `${files.length} Markdown file${files.length === 1 ? '' : 's'}${this.listing?.truncated ? ' (list truncated)' : ''}`;
    }
    this.select(this.current);
  }
}
