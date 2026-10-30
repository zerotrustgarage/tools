import { afterEach, describe, expect, it } from 'vitest';
import { rovingToolbar } from '../src/ui/roving';

afterEach(() => (document.body.innerHTML = ''));

function mount(disabled = -1) {
  const bar = document.createElement('div');
  bar.setAttribute('role', 'toolbar');
  for (const name of ['a', 'b', 'c', 'd']) {
    const b = document.createElement('button');
    b.textContent = name;
    bar.append(b);
  }
  if (disabled >= 0) (bar.children[disabled] as HTMLButtonElement).disabled = true;
  document.body.append(bar);
  rovingToolbar(bar);
  return [...bar.querySelectorAll('button')];
}
const press = (key: string) => document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));

describe('roving toolbar', () => {
  it('has one tab stop and moves with the arrow keys, wrapping at the ends', () => {
    const b = mount();
    expect(b.map((x) => x.tabIndex)).toEqual([0, -1, -1, -1]);
    b[0]!.focus();
    press('ArrowRight');
    expect(document.activeElement).toBe(b[1]);
    expect(b.map((x) => x.tabIndex)).toEqual([-1, 0, -1, -1]);
    press('End');
    expect(document.activeElement).toBe(b[3]);
    press('ArrowRight');
    expect(document.activeElement).toBe(b[0]);
    press('ArrowLeft');
    expect(document.activeElement).toBe(b[3]);
    press('Home');
    expect(document.activeElement).toBe(b[0]);
  });

  it('skips disabled buttons and follows a click', () => {
    const b = mount(1);
    b[0]!.focus();
    press('ArrowRight');
    expect(document.activeElement).toBe(b[2]);
    b[3]!.focus();
    expect(b.map((x) => x.tabIndex)).toEqual([-1, -1, -1, 0]);
  });
});
