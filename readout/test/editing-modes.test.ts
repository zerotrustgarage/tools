import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// RD17: both modes are off by default, are remembered per viewer under their own keys, and their
// shortcuts do not collide with any that the app or the editor binds. The browser suite
// (uitest/run.mjs) drives the real toggles; this checks the table.

const src = (path: string): string => readFileSync(join(__dirname, '..', path), 'utf8');
const app = src('src/ui/app.ts');
const format = src('src/ui/format.ts');

describe('focus and typewriter settings', () => {
  it('are off unless the stored value says on', () => {
    expect(app).toContain("stored('ztg-readout-focus', ['on', 'off'], 'off') === 'on'");
    expect(app).toContain("stored('ztg-readout-typewriter', ['on', 'off'], 'off') === 'on'");
  });
  it('are written under their own keys when toggled', () => {
    expect(app).toContain("store('ztg-readout-focus'");
    expect(app).toContain("store('ztg-readout-typewriter'");
  });
  it('announce a change through the polite live region', () => {
    expect(app).toContain('this.announce(`Focus mode ${');
    expect(app).toContain('this.announce(`Typewriter mode ${');
  });
});

describe('shortcuts', () => {
  it('F8 and F9 are not bound by the formatting keymap or the global shortcuts', () => {
    expect(format).not.toMatch(/F8|F9/);
    const bound = [...app.matchAll(/event\.key === '(F\d+)'/g)].map((m) => m[1]);
    expect([...new Set(bound)].sort()).toEqual(['F8', 'F9']);
  });
  it('are plain keys, so Ctrl+Shift+E (Recent) and Ctrl+M (Tab focus) are untouched', () => {
    expect(app).toMatch(/\(event\.key === 'F8' \|\| event\.key === 'F9'\) && !event\.ctrlKey && !event\.metaKey && !event\.altKey && !event\.shiftKey/);
    expect(app).toContain("key === 'e' && event.shiftKey && this.platform.recent");
  });
});
