import { afterEach, describe, expect, it, vi } from 'vitest';
import { extractOutline } from '../src/core/outline';
import { OutlinePanel } from '../src/ui/outline';

function mount() {
  const jump = vi.fn();
  const close = vi.fn();
  const panel = new OutlinePanel(jump, close);
  document.body.append(panel.element);
  panel.show(true);
  return { panel, jump, close };
}
const items = () => [...document.querySelectorAll<HTMLElement>('[role="treeitem"]')];

afterEach(() => (document.body.innerHTML = ''));

describe('outline panel', () => {
  it('is a tree with levels, set sizes and one tab stop; duplicates are separate items', () => {
    const { panel } = mount();
    panel.update(extractOutline('# A\n## Same\n## Same\n### Deep\n'));
    expect(document.querySelector('[role="tree"]')).not.toBeNull();
    expect(items().map((i) => i.getAttribute('aria-level'))).toEqual(['1', '2', '2', '3']);
    expect(items().map((i) => i.getAttribute('aria-posinset'))).toEqual(['1', '2', '3', '4']);
    expect(items()[0]!.getAttribute('aria-setsize')).toBe('4');
    expect(items().filter((i) => i.tabIndex === 0)).toHaveLength(1);
    expect(items()[1]!.title).not.toBe(items()[2]!.title);
  });

  it('numbers levels relative to the shallowest heading', () => {
    const { panel } = mount();
    panel.update(extractOutline('## Only\n#### Child\n'));
    expect(items().map((i) => i.getAttribute('aria-level'))).toEqual(['1', '3']);
  });

  it('jumps to the heading line on click, and on Enter through the native button', () => {
    const { panel, jump } = mount();
    panel.update(extractOutline('# A\n\ntext\n\n## B\n'));
    items()[1]!.click();
    expect(jump).toHaveBeenCalledWith(4);
  });

  it('moves between headings with the arrow keys, Home and End', () => {
    const { panel } = mount();
    panel.update(extractOutline('# A\n## B\n## C\n'));
    items()[0]!.focus();
    const press = (key: string) => document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
    press('ArrowDown');
    expect(document.activeElement).toBe(items()[1]);
    press('End');
    expect(document.activeElement).toBe(items()[2]);
    press('ArrowDown');
    expect(document.activeElement).toBe(items()[2]);
    press('ArrowUp');
    expect(document.activeElement).toBe(items()[1]);
    press('Home');
    expect(document.activeElement).toBe(items()[0]);
    expect(items().filter((i) => i.tabIndex === 0)).toEqual([items()[0]]);
  });

  it('follows edits and shows an empty state when the headings are gone', () => {
    const { panel } = mount();
    const empty = document.querySelector<HTMLElement>('.tool-empty')!;
    expect(empty.hidden).toBe(false);
    panel.update(extractOutline('# A\n'));
    expect(items()).toHaveLength(1);
    expect(empty.hidden).toBe(true);
    panel.update(extractOutline('# A\n## B\n'));
    expect(items().map((i) => i.textContent)).toEqual(['A', 'B']);
    panel.update([]);
    expect(items()).toHaveLength(0);
    expect(empty.hidden).toBe(false);
    expect(document.querySelector<HTMLElement>('.outline-list')!.hidden).toBe(true);
  });

  it('marks the heading under the cursor', () => {
    const { panel } = mount();
    panel.update(extractOutline('# A\n\n## B\n\ntext\n'));
    panel.setCursorLine(4);
    expect(items()[1]!.getAttribute('aria-current')).toBe('location');
    expect(items()[0]!.hasAttribute('aria-current')).toBe(false);
    panel.setCursorLine(0);
    expect(items()[0]!.getAttribute('aria-current')).toBe('location');
  });

  it('closes on Escape', () => {
    const { panel, close } = mount();
    panel.update(extractOutline('# A\n'));
    items()[0]!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(close).toHaveBeenCalled();
  });
});
