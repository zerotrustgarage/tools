import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Listing } from '../src/platform/types';
import { Sidebar } from '../src/ui/sidebar';

// RD20: the folder sidebar is a WAI-ARIA tree. The browser suite drives it in Chromium; these
// tests hold the pattern's logic (roles, levels, roving tabindex, keys) without a browser.

const FILES = ['README.md', 'docs/intro.md', 'docs/guide/setup.md', 'docs/guide/more.md', 'notes/todo.md'];
const listing = (files = FILES): Listing => ({ folder: { name: 'proj', root: '/proj' } as Listing['folder'], files, truncated: false });

function mount(files = FILES) {
  const open = vi.fn();
  const sidebar = new Sidebar(open, vi.fn());
  document.body.append(sidebar.element);
  sidebar.show(listing(files));
  return { sidebar, open };
}
const items = () => [...document.querySelectorAll<HTMLElement>('[role="treeitem"]')];
const item = (path: string) => items().find((i) => i.dataset.path === path)!;
const press = (key: string, target: Element = document.activeElement!) => target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
const focused = () => (document.activeElement as HTMLElement | null)?.dataset.path;

// jsdom does no layout, so it has no scrollIntoView.
Element.prototype.scrollIntoView = () => undefined;

afterEach(() => {
  document.body.innerHTML = '';
  vi.useRealTimers();
});

describe('folder tree roles', () => {
  it('is a tree of treeitems and groups with levels, sizes and expanded state', () => {
    mount();
    expect(document.querySelectorAll('[role="tree"]')).toHaveLength(1);
    expect(document.querySelector('[role="tree"]')!.getAttribute('aria-label')).toBe('Markdown files');
    const attrs = (p: string) => ['aria-level', 'aria-setsize', 'aria-posinset', 'aria-expanded'].map((a) => item(p).getAttribute(a));
    expect(attrs('docs')).toEqual(['1', '3', '1', 'true']);
    expect(attrs('docs/guide')).toEqual(['2', '2', '1', 'false']);
    expect(attrs('docs/guide/setup.md')).toEqual(['3', '2', '2', null]);
    expect(item('docs/guide').querySelector('[role="group"]')!.hasAttribute('hidden')).toBe(true);
    expect(item('docs').querySelector(':scope > [role="group"]')!.hasAttribute('hidden')).toBe(false);
    // Every item is named by its own label, not by the text of the items inside it.
    expect(item('docs').getAttribute('aria-label')).toBe('docs');
    expect(item('docs/intro.md').getAttribute('aria-label')).toBe('intro.md');
  });

  it('has exactly one tab stop: the open file, else the first item', () => {
    const { sidebar } = mount();
    expect(items().filter((i) => i.tabIndex === 0).map((i) => i.dataset.path)).toEqual(['docs']);
    sidebar.select('README.md');
    expect(items().filter((i) => i.tabIndex === 0).map((i) => i.dataset.path)).toEqual(['README.md']);
    expect(item('README.md').getAttribute('aria-selected')).toBe('true');
    expect(item('README.md').getAttribute('aria-current')).toBe('page');
    expect(item('docs/intro.md').getAttribute('aria-selected')).toBe('false');
  });

  it('opens the folders above the open file', () => {
    const { sidebar } = mount();
    sidebar.select('docs/guide/setup.md');
    expect(item('docs/guide').getAttribute('aria-expanded')).toBe('true');
    sidebar.select('notes/todo.md');
    expect(item('notes').getAttribute('aria-expanded')).toBe('true');
  });
});

describe('folder tree keys', () => {
  it('moves with Up, Down, Home and End over the visible items only', () => {
    const { sidebar } = mount();
    sidebar.select('README.md');
    item('README.md').focus();
    press('Home');
    expect(focused()).toBe('docs');
    press('ArrowDown');
    expect(focused()).toBe('docs/guide'); // guide is closed, so its files are skipped
    press('ArrowDown');
    expect(focused()).toBe('docs/intro.md');
    press('End');
    expect(focused()).toBe('README.md');
    press('ArrowDown');
    expect(focused()).toBe('README.md');
    expect(items().filter((i) => i.tabIndex === 0)).toEqual([item('README.md')]);
  });

  it('opens, enters, leaves and closes folders with Right and Left', () => {
    mount();
    item('docs/guide').focus();
    press('ArrowRight');
    expect(item('docs/guide').getAttribute('aria-expanded')).toBe('true');
    expect(focused()).toBe('docs/guide');
    press('ArrowRight');
    expect(focused()).toBe('docs/guide/more.md');
    press('ArrowRight');
    expect(focused()).toBe('docs/guide/more.md');
    press('ArrowLeft');
    expect(focused()).toBe('docs/guide');
    press('ArrowLeft');
    expect(item('docs/guide').getAttribute('aria-expanded')).toBe('false');
    press('ArrowLeft');
    expect(focused()).toBe('docs');
    press('ArrowLeft');
    expect(item('docs').getAttribute('aria-expanded')).toBe('false');
    press('ArrowLeft'); // a top-level closed folder has no parent
    expect(focused()).toBe('docs');
  });

  it('opens a file with Enter or Space and toggles a folder', () => {
    const { open } = mount();
    item('notes').focus();
    press('Enter');
    expect(item('notes').getAttribute('aria-expanded')).toBe('true');
    press(' ');
    expect(item('notes').getAttribute('aria-expanded')).toBe('false');
    expect(open).not.toHaveBeenCalled();
    item('docs/intro.md').focus();
    press('Enter');
    expect(open).toHaveBeenCalledWith('docs/intro.md');
    press(' ');
    expect(open).toHaveBeenCalledTimes(2);
  });

  it('ignores keys with Ctrl, Alt or Meta, and keys typed in the filter field', () => {
    const { open } = mount();
    item('docs/intro.md').focus();
    item('docs/intro.md').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true }));
    expect(open).not.toHaveBeenCalled();
  });

  it('jumps to the next name that starts with what was typed', () => {
    vi.useFakeTimers();
    mount();
    item('docs').focus();
    press('n');
    expect(focused()).toBe('notes');
    vi.advanceTimersByTime(800);
    press('r');
    expect(focused()).toBe('README.md');
    vi.advanceTimersByTime(800);
    press('d');
    expect(focused()).toBe('docs');
    press('x'); // no name starts with "dx": stays put
    expect(focused()).toBe('docs');
  });

  it('cycles through names with one repeated letter', () => {
    vi.useFakeTimers();
    mount(['alpha.md', 'apple.md', 'beta.md']);
    item('alpha.md').focus();
    press('a');
    expect(focused()).toBe('apple.md');
    press('a');
    expect(focused()).toBe('alpha.md');
  });

  it('Down from the filter field enters the tree', () => {
    mount();
    const filter = document.querySelector<HTMLInputElement>('input[aria-label="Filter files"]')!;
    filter.focus();
    press('ArrowDown', filter);
    expect(focused()).toBe('docs');
  });
});

describe('folder tree filter and refresh', () => {
  it('shows matches as a flat tree and says the count a moment after typing stops', () => {
    vi.useFakeTimers();
    mount();
    const filter = document.querySelector<HTMLInputElement>('input[aria-label="Filter files"]')!;
    filter.value = 'setup';
    filter.dispatchEvent(new Event('input'));
    expect(items().map((i) => [i.dataset.path, i.getAttribute('aria-level')])).toEqual([['docs/guide/setup.md', '1']]);
    const live = document.querySelector('.sidebar [role="status"]')!;
    expect(live.textContent).toBe('');
    vi.advanceTimersByTime(700);
    expect(live.textContent).toBe('1 of 5 files');
  });

  it('keeps open folders and the focused item when the listing is refreshed', () => {
    const { sidebar } = mount();
    item('docs/guide').focus();
    press('ArrowRight');
    item('docs/intro.md').focus();
    sidebar.update(listing([...FILES, 'zeta.md']));
    expect(item('docs/guide').getAttribute('aria-expanded')).toBe('true');
    expect(focused()).toBe('docs/intro.md');
    expect(items().filter((i) => i.tabIndex === 0)).toHaveLength(1);
  });

  it('clicking a row focuses and opens its item', () => {
    const { open } = mount();
    item('docs/intro.md').querySelector<HTMLElement>('.tree-row')!.click();
    expect(open).toHaveBeenCalledWith('docs/intro.md');
    expect(focused()).toBe('docs/intro.md');
    item('notes').querySelector<HTMLElement>('.tree-row')!.click();
    expect(item('notes').getAttribute('aria-expanded')).toBe('true');
  });
});
