import type { OutlineHeading } from '../core/outline';
import { el, icon } from './dom';

// The outline panel: the document's headings as a flat tree (aria-level carries the nesting,
// indentation shows it). One tab stop; arrow keys, Home and End move between headings, Enter or
// Space jumps. The panel only displays what it is given and reports which line was chosen.

export class OutlinePanel {
  readonly element = el('aside', { class: 'tool-panel outline-panel', 'aria-label': 'Outline', hidden: '' });
  private readonly list = el('ul', { class: 'outline-list', role: 'tree', 'aria-label': 'Headings' });
  private readonly empty = el('p', { class: 'tool-empty' }, 'No headings in this document.');
  private headings: OutlineHeading[] = [];
  private signature = '';
  private active = -1;

  constructor(
    private readonly onJump: (line: number) => void,
    onClose: () => void,
  ) {
    const close = el('button', { type: 'button', class: 'icon-button', title: 'Close outline', 'aria-label': 'Close outline' }, icon('close'));
    close.addEventListener('click', onClose);
    this.list.addEventListener('keydown', (event) => this.key(event));
    this.element.addEventListener('keydown', (event) => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        onClose();
      }
    });
    this.element.append(
      el('div', { class: 'sidebar-head' }, icon('file'), el('span', { class: 'sidebar-title' }, 'Outline'), close),
      el('div', { class: 'tool-body' }, this.list, this.empty),
    );
    this.render();
  }

  get hidden(): boolean {
    return this.element.hidden !== false;
  }

  show(visible: boolean): void {
    this.element.hidden = !visible;
  }

  focus(): void {
    this.items()[Math.max(this.active, 0)]?.focus();
  }

  /** Replaces the headings. Unchanged headings leave the DOM (and a focused item) alone. */
  update(headings: OutlineHeading[]): void {
    const signature = headings.map((h) => `${h.level}\t${h.line}\t${h.text}`).join('\n');
    if (signature === this.signature) return;
    const focused = this.items().indexOf(document.activeElement as HTMLElement);
    this.signature = signature;
    this.headings = headings;
    this.render();
    if (focused >= 0) this.items()[Math.min(focused, headings.length - 1)]?.focus();
  }

  /** Marks the heading that holds the cursor (the last one at or above the zero-based line). */
  setCursorLine(line: number): void {
    let index = -1;
    for (let i = 0; i < this.headings.length; i++) {
      if (this.headings[i]!.line > line) break;
      index = i;
    }
    this.active = index;
    this.mark();
  }

  private items(): HTMLElement[] {
    return [...this.list.querySelectorAll<HTMLElement>('[role="treeitem"]')];
  }

  private render(): void {
    const minLevel = this.headings.reduce((m, h) => Math.min(m, h.level), 6);
    this.list.replaceChildren(
      ...this.headings.map((h, i) => {
        const item = el(
          'button',
          {
            type: 'button',
            role: 'treeitem',
            class: `outline-item level-${h.level}`,
            'aria-level': String(h.level - minLevel + 1),
            'aria-setsize': String(this.headings.length),
            'aria-posinset': String(i + 1),
            title: `${h.text} (line ${h.line + 1})`,
            tabindex: '-1',
          },
          el('span', {}, h.text),
        );
        // CSSOM, not a style attribute: the page's CSP does not allow inline style attributes.
        item.style.setProperty('--depth', String(h.level - minLevel));
        item.addEventListener('click', () => this.onJump(h.line));
        return el('li', { role: 'none' }, item);
      }),
    );
    this.empty.hidden = this.headings.length > 0;
    this.list.hidden = this.headings.length === 0;
    this.mark();
  }

  private mark(): void {
    const items = this.items();
    const tabStop = this.active >= 0 ? this.active : 0;
    items.forEach((item, i) => {
      item.classList.toggle('active', i === this.active);
      if (i === this.active) item.setAttribute('aria-current', 'location');
      else item.removeAttribute('aria-current');
      item.tabIndex = i === tabStop ? 0 : -1;
    });
  }

  private key(event: KeyboardEvent): void {
    const items = this.items();
    const at = items.indexOf(document.activeElement as HTMLElement);
    const next =
      event.key === 'ArrowDown' ? at + 1 : event.key === 'ArrowUp' ? at - 1 : event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : null;
    if (next === null || items.length === 0) return;
    event.preventDefault();
    const target = items[Math.min(Math.max(next, 0), items.length - 1)]!;
    for (const item of items) item.tabIndex = item === target ? 0 : -1;
    target.focus();
  }
}
