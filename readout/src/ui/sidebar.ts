import { buildTree, type TreeNode } from '../core/paths';
import type { Listing } from '../platform/types';
import { el, icon } from './dom';

// The folder sidebar: the open folder's Markdown files as a WAI-ARIA tree (role tree, treeitem and
// group), with a filter. One tab stop (a roving tabindex); Up and Down move between the visible
// items, Right opens a folder or enters it, Left closes it or goes to its parent, Home and End
// jump, Enter or Space opens a file (or opens and closes a folder), and typing a few letters jumps
// to the next name that starts with them. Folders on the path to the current file open
// automatically. The filter shows a flat list of the matching paths, as a tree of one level.

const TYPE_AHEAD_MS = 700;
const ANNOUNCE_DELAY_MS = 600;
const DEFAULT_OPEN = /^(docs?|documentation|\.github)$/i;

export class Sidebar {
  readonly element = el('aside', { class: 'sidebar', 'aria-label': 'Folder', hidden: '' });
  private readonly title = el('span', { class: 'sidebar-title' });
  private readonly filter = el('input', { type: 'search', class: 'sidebar-filter', placeholder: 'Filter files', 'aria-label': 'Filter files', spellcheck: 'false' });
  private readonly list = el('ul', { class: 'tree-list', role: 'tree', 'aria-label': 'Markdown files' });
  private readonly scroller = el('div', { class: 'tree' }, this.list);
  private readonly count = el('div', { class: 'sidebar-count' });
  /** Said politely a moment after the reader stops typing in the filter, never per keystroke. */
  private readonly live = el('span', { class: 'sr-only', role: 'status' });
  private listing: Listing | null = null;
  private current: string | undefined;
  /** Folders the reader (or the current file) opened. */
  private expanded = new Set<string>();
  /** The item that holds the tab stop, by path. */
  private stop: string | undefined;
  private typed = '';
  private typedTimer: number | undefined;
  private announceTimer: number | undefined;

  constructor(
    private readonly onOpen: (folderPath: string) => void,
    onClose: () => void,
  ) {
    const close = el('button', { type: 'button', class: 'icon-button', title: 'Close folder', 'aria-label': 'Close folder' }, icon('close'));
    close.addEventListener('click', onClose);
    this.filter.addEventListener('input', () => {
      this.render();
      window.clearTimeout(this.announceTimer);
      this.announceTimer = window.setTimeout(() => (this.live.textContent = this.count.textContent), ANNOUNCE_DELAY_MS);
    });
    this.filter.addEventListener('keydown', (event) => {
      if (event.key !== 'ArrowDown') return;
      const first = this.tabStop();
      if (!first) return;
      event.preventDefault();
      first.focus();
    });
    this.list.addEventListener('click', (event) => this.click(event));
    this.list.addEventListener('keydown', (event) => this.key(event));
    this.list.addEventListener('focusin', (event) => {
      const item = (event.target as Element).closest<HTMLElement>('[role="treeitem"]');
      if (item && item === event.target) this.setStop(item);
    });
    this.element.append(el('div', { class: 'sidebar-head' }, icon('folder'), this.title, close), this.filter, this.scroller, this.count, this.live);
  }

  show(listing: Listing | null): void {
    this.listing = listing;
    this.filter.value = '';
    this.live.textContent = '';
    this.element.hidden = listing === null;
    this.title.textContent = listing?.folder.name ?? '';
    this.title.title = listing?.folder.root ?? listing?.folder.name ?? '';
    // Top-level documentation folders start open; the others open on demand or when they hold the
    // current file.
    this.expanded = new Set(buildTree(listing?.files ?? []).filter((n) => !n.file && DEFAULT_OPEN.test(n.name)).map((n) => n.path));
    this.stop = undefined;
    this.render();
  }

  /** Swaps in a refreshed listing, keeping the filter text and the folders the reader opened. */
  update(listing: Listing): void {
    this.listing = listing;
    this.render();
  }

  select(folderPath: string | undefined): void {
    this.current = folderPath;
    // The tab stop follows the open file, unless the reader is working in the tree and keeps their place.
    if (!this.list.contains(document.activeElement)) this.stop = undefined;
    this.mark(true);
  }

  // ---- building ---------------------------------------------------------------------------

  private row(kind: 'folder' | 'file', label: string, path: string): HTMLElement {
    return el('div', { class: 'tree-row', title: path }, icon(kind === 'folder' ? 'chevron' : 'file'), el('span', {}, label));
  }

  private fileItem(path: string, label: string, level: number, size: number, position: number): HTMLElement {
    return el('li', { class: 'tree-file', role: 'treeitem', 'data-path': path, 'aria-label': label, 'aria-level': String(level), 'aria-setsize': String(size), 'aria-posinset': String(position), 'aria-selected': 'false', tabindex: '-1' }, this.row('file', label, path));
  }

  private nodes(nodes: TreeNode[], level: number): HTMLElement[] {
    return nodes.map((node, index) => {
      if (node.file) return this.fileItem(node.path, node.name, level, nodes.length, index + 1);
      const open = this.expanded.has(node.path);
      const group = el('ul', { role: 'group' }, ...this.nodes(node.children, level + 1));
      group.hidden = !open;
      return el(
        'li',
        { class: 'tree-folder', role: 'treeitem', 'data-path': node.path, 'data-folder': '', 'aria-label': node.name, 'aria-level': String(level), 'aria-setsize': String(nodes.length), 'aria-posinset': String(index + 1), 'aria-expanded': String(open), tabindex: '-1' },
        this.row('folder', node.name, node.path),
        group,
      );
    });
  }

  private render(): void {
    const focused = this.list.contains(document.activeElement) ? (document.activeElement as HTMLElement) : null;
    if (focused?.dataset.path !== undefined) this.stop = focused.dataset.path;
    const files = this.listing?.files ?? [];
    const query = this.filter.value.trim().toLowerCase();
    if (query) {
      const hits = files.filter((f) => f.toLowerCase().includes(query));
      this.list.replaceChildren(...hits.map((f, i) => this.fileItem(f, f, 1, hits.length, i + 1)));
      this.count.textContent = `${hits.length} of ${files.length} files`;
    } else {
      this.list.replaceChildren(...this.nodes(buildTree(files), 1));
      this.count.textContent = `${files.length} Markdown file${files.length === 1 ? '' : 's'}${this.listing?.truncated ? ' (list truncated)' : ''}`;
    }
    // Folders above the open file are open (as they were when the tree was last built).
    this.mark(query === '');
    // A list swapped under the reader keeps their place (or the first item when it is gone).
    if (focused) (this.itemFor(this.stop) ?? this.tabStop())?.focus();
  }

  /** Shows which file is open, opens the folders above it, and keeps one item in the tab order. */
  private mark(reveal: boolean): void {
    for (const item of this.items()) {
      if (item.dataset.folder !== undefined) continue;
      const active = item.dataset.path === this.current;
      item.classList.toggle('active', active);
      item.setAttribute('aria-selected', String(active));
      if (!active) item.removeAttribute('aria-current');
      else {
        item.setAttribute('aria-current', 'page');
        if (reveal) {
          for (let f = item.parentElement?.closest<HTMLElement>('[role="treeitem"]'); f; f = f.parentElement?.closest<HTMLElement>('[role="treeitem"]') ?? null) this.setExpanded(f, true);
          item.scrollIntoView({ block: 'nearest' });
        }
      }
    }
    this.setStop(this.tabStop());
  }

  // ---- items ------------------------------------------------------------------------------

  private items(): HTMLElement[] {
    return [...this.list.querySelectorAll<HTMLElement>('[role="treeitem"]')];
  }

  /** The items a reader can reach now: those not inside a closed folder. */
  private visible(): HTMLElement[] {
    return this.items().filter((item) => item.parentElement?.closest('ul[role="group"][hidden]') == null);
  }

  private itemFor(path: string | undefined): HTMLElement | undefined {
    if (path === undefined) return undefined;
    return this.visible().find((item) => item.dataset.path === path);
  }

  /** The item that takes Tab: the one that last had focus, else the open file, else the first. */
  private tabStop(): HTMLElement | undefined {
    return this.itemFor(this.stop) ?? this.itemFor(this.current) ?? this.visible()[0];
  }

  private setStop(item: HTMLElement | undefined): void {
    this.stop = item?.dataset.path;
    for (const other of this.items()) other.tabIndex = other === item ? 0 : -1;
  }

  private setExpanded(folder: HTMLElement, open: boolean): void {
    const path = folder.dataset.path ?? '';
    if (open) this.expanded.add(path);
    else this.expanded.delete(path);
    folder.setAttribute('aria-expanded', String(open));
    const group = [...folder.children].find((c) => c.matches('ul[role="group"]')) as HTMLElement | undefined;
    if (group) group.hidden = !open;
  }

  private activate(item: HTMLElement): void {
    if (item.dataset.folder !== undefined) this.setExpanded(item, item.getAttribute('aria-expanded') !== 'true');
    else if (item.dataset.path !== undefined) this.onOpen(item.dataset.path);
  }

  // ---- input ------------------------------------------------------------------------------

  private click(event: MouseEvent): void {
    // A click on a row, or on the item itself (a script or an assistive technology's default action).
    const target = event.target as Element;
    const item = target.getAttribute('role') === 'treeitem' ? target : target.closest('.tree-row')?.parentElement;
    if (item?.getAttribute('role') !== 'treeitem') return;
    // A click on a row is a click on its item: focus follows it, as it does for a button.
    (item as HTMLElement).focus();
    this.activate(item as HTMLElement);
  }

  private key(event: KeyboardEvent): void {
    const item = event.target as HTMLElement;
    if (item.getAttribute('role') !== 'treeitem' || event.ctrlKey || event.altKey || event.metaKey) return;
    const visible = this.visible();
    const at = visible.indexOf(item);
    const folder = item.dataset.folder !== undefined;
    const open = item.getAttribute('aria-expanded') === 'true';
    let target: HTMLElement | undefined;
    switch (event.key) {
      case 'ArrowDown':
        target = visible[Math.min(at + 1, visible.length - 1)];
        break;
      case 'ArrowUp':
        target = visible[Math.max(at - 1, 0)];
        break;
      case 'Home':
        target = visible[0];
        break;
      case 'End':
        target = visible[visible.length - 1];
        break;
      case 'ArrowRight':
        if (!folder) break;
        if (!open) this.setExpanded(item, true);
        else target = visible[at + 1] && item.contains(visible[at + 1]!) ? visible[at + 1] : undefined;
        break;
      case 'ArrowLeft':
        if (folder && open) this.setExpanded(item, false);
        else target = item.parentElement?.closest<HTMLElement>('[role="treeitem"]') ?? undefined;
        break;
      case 'Enter':
      case ' ':
        this.activate(item);
        break;
      default:
        if (event.key.length === 1 && !event.shiftKey) target = this.typeAhead(event.key, visible, at);
        else return;
        if (!target) return;
    }
    event.preventDefault();
    if (target) target.focus();
  }

  /** The next visible item whose name starts with what was typed lately (one letter cycles). */
  private typeAhead(key: string, visible: HTMLElement[], at: number): HTMLElement | undefined {
    window.clearTimeout(this.typedTimer);
    this.typedTimer = window.setTimeout(() => (this.typed = ''), TYPE_AHEAD_MS);
    this.typed += key.toLowerCase();
    const cycling = this.typed.split('').every((c) => c === this.typed[0]);
    const prefix = cycling ? this.typed[0]! : this.typed;
    const order = [...visible.slice(at + (cycling ? 1 : 0)), ...visible.slice(0, at + (cycling ? 1 : 0))];
    return order.find((item) => (item.getAttribute('aria-label') ?? '').toLowerCase().startsWith(prefix));
  }
}
