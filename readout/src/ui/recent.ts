import type { RecentEntry } from '../platform/types';
import { el, icon } from './dom';

// The recent-files panel: the documents opened lately, newest first. It only displays what it is
// given and reports what was chosen; opening, the unsaved-changes prompt and the storage are the
// app's. Every control is a native button, so the keyboard works without extra roles. Up and Down
// move between files, Home and End jump, Delete removes the focused line, Escape closes.

export interface RecentHandlers {
  open(entry: RecentEntry): void;
  remove(entry: RecentEntry): void;
  clear(): void;
  onClose(): void;
}

export class RecentPanel {
  readonly element = el('aside', { class: 'tool-panel recent-panel', 'aria-label': 'Recent files', hidden: '' });
  private readonly list = el('ul', { class: 'recent-list', 'aria-label': 'Recent files' });
  private readonly empty = el('p', { class: 'tool-empty' }, 'No recent files yet. Files you open appear here.');
  private readonly clearButton = el('button', { type: 'button', class: 'text-button' }, 'Clear list');
  private readonly status = el('span', { class: 'search-status', role: 'status' });
  private entries: RecentEntry[] = [];

  constructor(private readonly handlers: RecentHandlers) {
    const close = el('button', { type: 'button', class: 'icon-button', title: 'Close recent files', 'aria-label': 'Close recent files' }, icon('close'));
    close.addEventListener('click', handlers.onClose);
    this.clearButton.addEventListener('click', () => handlers.clear());
    this.element.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        handlers.onClose();
      }
    });
    this.list.addEventListener('keydown', (event) => this.key(event));
    this.element.append(
      el('div', { class: 'sidebar-head' }, icon('file'), el('span', { class: 'sidebar-title' }, 'Recent files'), close),
      el('div', { class: 'tool-body' }, this.list, this.empty),
      el('div', { class: 'search-bar' }, this.status, this.clearButton),
    );
    this.render();
  }

  get hidden(): boolean {
    return this.element.hidden !== false;
  }

  show(visible: boolean): void {
    this.element.hidden = !visible;
  }

  /** Moves focus to the first file, or to the close button when the list is empty. */
  focus(): void {
    (this.openButtons()[0] ?? this.element.querySelector<HTMLElement>('button'))?.focus();
  }

  /** Replaces the lines. A focused line keeps its place (or its neighbour's) after a removal. */
  update(entries: RecentEntry[]): void {
    const active = document.activeElement;
    const hadFocus = this.element.contains(active);
    const focused = [...this.list.children].indexOf(active?.closest('.recent-row') as Element);
    this.entries = entries;
    this.render();
    if (!hadFocus) return;
    const buttons = this.openButtons();
    const target = buttons[Math.min(Math.max(focused, 0), buttons.length - 1)];
    (target ?? this.element.querySelector<HTMLElement>('.icon-button'))?.focus();
  }

  private openButtons(): HTMLElement[] {
    return [...this.list.querySelectorAll<HTMLElement>('.recent-item')];
  }

  private render(): void {
    this.list.replaceChildren(
      ...this.entries.map((entry) => {
        const open = el(
          'button',
          { type: 'button', class: `recent-item${entry.missing ? ' missing' : ''}`, title: entry.detail ? `${entry.detail}/${entry.name}`.replace(/\/+$/, '') : entry.name },
          icon('file'),
          el('span', { class: 'recent-text' }, el('span', { class: 'recent-name' }, entry.name), ...(entry.detail ? [el('span', { class: 'recent-detail' }, entry.detail)] : [])),
          ...(entry.missing ? [el('span', { class: 'recent-missing' }, 'Not found')] : []),
        );
        open.addEventListener('click', () => this.handlers.open(entry));
        const remove = el(
          'button',
          { type: 'button', class: 'icon-button recent-remove', title: `Remove ${entry.name} from the list`, 'aria-label': `Remove ${entry.name} from the list` },
          icon('close'),
        );
        remove.addEventListener('click', () => this.handlers.remove(entry));
        return el('li', { class: 'recent-row' }, open, remove);
      }),
    );
    this.empty.hidden = this.entries.length > 0;
    this.list.hidden = this.entries.length === 0;
    this.clearButton.disabled = this.entries.length === 0;
    const missing = this.entries.filter((e) => e.missing).length;
    this.status.textContent =
      this.entries.length === 0 ? '' : `${this.entries.length} file${this.entries.length === 1 ? '' : 's'}${missing > 0 ? `, ${missing} not found` : ''}`;
  }

  private key(event: KeyboardEvent): void {
    const buttons = this.openButtons();
    const row = (event.target as Element).closest('.recent-row');
    const at = buttons.findIndex((b) => b.closest('.recent-row') === row);
    if (at < 0) return;
    if (event.key === 'Delete') {
      event.preventDefault();
      const entry = this.entries[at];
      if (entry) this.handlers.remove(entry);
      return;
    }
    const next =
      event.key === 'ArrowDown' ? at + 1 : event.key === 'ArrowUp' ? at - 1 : event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : null;
    if (next === null) return;
    event.preventDefault();
    buttons[Math.min(Math.max(next, 0), buttons.length - 1)]?.focus();
  }
}
