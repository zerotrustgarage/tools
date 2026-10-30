import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { isLarge, over, parseColor, ratio, type Rgba } from '../uitest/contrast.mjs';

// RD20: WCAG 2.1 contrast of the colour pairs the stylesheets use, in both themes, computed from
// the CSS custom properties themselves (src/styles/app.css and preview.css) and the palette in
// design-tokens.json. The browser suite (uitest/run.mjs) measures the same thing on screen from
// computed styles; this test covers pairs no scenario reaches (hover, active rows, the title bar)
// and fails the moment a token is swapped for one that is too weak.
//
// CONTRAST_REPORT=1 prints the table; CONTRAST_REF=<git ref> reads the stylesheets of that
// commit instead (to compare before and after).

const root = join(__dirname, '..');
const read = (path: string): string => {
  const ref = process.env.CONTRAST_REF;
  return ref ? execFileSync('git', ['show', `${ref}:readout/${path}`], { cwd: root, encoding: 'utf8' }) : readFileSync(join(root, path), 'utf8');
};
const appCss = read('src/styles/app.css');
const previewCss = read('src/styles/preview.css');
const tokens = (JSON.parse(readFileSync(join(root, '..', 'design-tokens.json'), 'utf8')) as { tokens: Record<string, string> }).tokens;

/** The custom properties declared in the block that starts with selector. */
function block(css: string, selector: string): Map<string, string> {
  const start = css.indexOf(selector);
  if (start < 0) throw new Error(`selector not found: ${selector}`);
  const open = css.indexOf('{', start);
  const vars = new Map<string, string>();
  for (const m of css.slice(open + 1, css.indexOf('}', open)).matchAll(/--([\w-]+)\s*:\s*([^;]+);/g)) vars.set(m[1]!, m[2]!.trim());
  return vars;
}

const themes = {
  light: new Map([...block(appCss, ":root,\n:root[data-theme='light'] {"), ...block(previewCss, ":root,\n[data-theme='light'] {")]),
  dark: new Map([...block(appCss, ":root[data-theme='dark'] {"), ...block(previewCss, "[data-theme='dark'] {")]),
};

/** A palette value or a custom property of the theme as an opaque colour over `base`. */
function color(theme: keyof typeof themes, name: string, base: Rgba): Rgba {
  const raw = themes[theme].get(name);
  if (raw === undefined) throw new Error(`--${name} is not defined in the ${theme} theme`);
  return over(parseColor(raw), base);
}

type Pair = { fg: string; bg: string; min: number; what: string };
// 4.5 is body text; 3 is a user interface component or graphic (WCAG 1.4.11) or large text.
const TEXT = 4.5;
const UI = 3;
const pairs: Pair[] = [
  ...['panel', 'bg', 'panel2'].flatMap((bg) => [
    { fg: 'text', bg, min: TEXT, what: `body text on ${bg}` },
    { fg: 'dim', bg, min: TEXT, what: `secondary text on ${bg}` },
    { fg: 'focus', bg, min: UI, what: `focus ring on ${bg}` },
  ]),
  { fg: 'bright', bg: 'panel', min: TEXT, what: 'headings on panel' },
  { fg: 'bright', bg: 'panel2', min: TEXT, what: 'tree row hover text' },
  { fg: 'bright', bg: 'bg', min: TEXT, what: 'bright on page' },
  { fg: 'cm-mark', bg: 'panel', min: TEXT, what: 'Markdown marks in the editor' },
  { fg: 'cm-mark', bg: 'active-line', min: TEXT, what: 'Markdown marks on the active line' },
  { fg: 'dim', bg: 'active-line', min: TEXT, what: 'line numbers on the active line' },
  { fg: 'acc', bg: 'panel', min: TEXT, what: 'list markers and accent text on panel' },
  { fg: 'acc-ink', bg: 'acc', min: TEXT, what: 'text on a pressed toggle' },
  { fg: 'crit', bg: 'panel', min: TEXT, what: 'error text' },
  { fg: 'warn', bg: 'panel', min: TEXT, what: 'warning text (not found, unsaved)' },
  { fg: 'text', bg: 'titlebar-bg', min: TEXT, what: 'title bar name and caption glyphs' },
  { fg: 'dim', bg: 'titlebar-bg', min: TEXT, what: 'title bar text' },
  { fg: 'focus', bg: 'titlebar-bg', min: UI, what: 'focus ring on the title bar' },
  { fg: 'dim2', bg: 'panel', min: UI, what: 'edge of the filter field and find fields' },
  { fg: 'dim2', bg: 'panel2', min: UI, what: 'edge of the filter field on its own fill' },
  { fg: 'dim', bg: 'panel', min: UI, what: 'tree and button icons' },
  { fg: 'acc', bg: 'acc-panel', min: UI, what: 'accent icons on panel' },
  { fg: 'acc', bg: 'panel', min: UI, what: 'pressed toggle against the bar' },
  { fg: 'bright', bg: 'tree-active', min: TEXT, what: 'open file in the tree' },
  { fg: 'bright', bg: 'banner', min: TEXT, what: 'banner text' },
  { fg: 'md-text', bg: 'md-bg', min: TEXT, what: 'preview body text' },
  { fg: 'md-heading', bg: 'md-bg', min: TEXT, what: 'preview headings' },
  { fg: 'md-muted', bg: 'md-bg', min: TEXT, what: 'preview muted text' },
  { fg: 'md-link', bg: 'md-bg', min: TEXT, what: 'preview links' },
  { fg: 'md-text', bg: 'md-code-bg', min: TEXT, what: 'preview code blocks' },
  { fg: 'md-text', bg: 'md-subtle', min: TEXT, what: 'preview table stripes and quotes' },
  { fg: 'focus', bg: 'md-bg', min: UI, what: 'focus ring on the preview' },
  ...['md-note', 'md-tip', 'md-important', 'md-warning', 'md-caution'].map((fg) => ({ fg, bg: 'md-bg', min: TEXT, what: `callout title ${fg}` })),
  // RD17, focus mode: lines outside the current paragraph are one colour (--focus-dim), and must stay
  // at 3:1 or better wherever the editor can put a background under them. The current paragraph keeps
  // the ordinary colours (text, headings and marks above), with the same backgrounds checked here.
  { fg: 'focus-dim', bg: 'panel', min: UI, what: 'dimmed text outside the current paragraph (focus mode)' },
  { fg: 'focus-dim', bg: 'active-line', min: UI, what: 'dimmed text over an active-line background' },
  { fg: 'focus-dim', bg: 'selection-match', min: UI, what: 'dimmed text in a selection match' },
  // A find match inside a dimmed line is drawn in --bright (app.css/editor.ts), not in the dim colour.
  { fg: 'bright', bg: 'search-match', min: UI, what: 'a find match in a dimmed line' },
  { fg: 'bright', bg: 'search-match-current', min: UI, what: 'the current find match in a dimmed line' },
  { fg: 'text', bg: 'selection', min: TEXT, what: 'current paragraph text inside a selection' },
  ...['hl-keyword', 'hl-string', 'hl-number', 'hl-comment', 'hl-function', 'hl-type', 'hl-attr'].flatMap((fg) => [
    { fg, bg: 'md-code-bg', min: TEXT, what: `${fg} in a preview code block` },
    { fg, bg: 'panel', min: TEXT, what: `${fg} in the editor` },
    { fg, bg: 'active-line', min: TEXT, what: `${fg} on the active line` },
  ]),
];

/** The surfaces that are a colour over another, not a variable of their own. */
function surface(theme: keyof typeof themes, name: string): Rgba {
  const panel = color(theme, 'panel', [255, 255, 255, 1]);
  const page = color(theme, 'bg', [255, 255, 255, 1]);
  switch (name) {
    case 'active-line':
      return over(parseColor(themes[theme].get('active-line')!), panel);
    case 'tree-active':
      return over([...parseColor(themes[theme].get('acc')!).slice(0, 3), 0.16] as Rgba, panel);
    case 'banner':
      return over([...parseColor(themes[theme].get('warn')!).slice(0, 3), 0.16] as Rgba, panel);
    case 'acc-panel':
      return panel;
    case 'selection':
    case 'selection-match':
      return over(parseColor(themes[theme].get(name)!), panel);
    // EditorView.theme: color-mix(in srgb, var(--warn) 30% and 55%, transparent)
    case 'search-match':
      return over([...parseColor(themes[theme].get('warn')!).slice(0, 3), 0.3] as Rgba, panel);
    case 'search-match-current':
      return over([...parseColor(themes[theme].get('warn')!).slice(0, 3), 0.55] as Rgba, panel);
    default:
      return themes[theme].has(name) ? color(theme, name, page) : (() => { throw new Error(`unknown surface ${name}`); })();
  }
}

function measure(theme: keyof typeof themes, pair: Pair): number {
  const bg = surface(theme, pair.bg);
  const fg = over(parseColor(themes[theme].get(pair.fg)!), bg);
  return ratio(fg, bg);
}

describe('stylesheet contrast (WCAG 2.1 AA)', () => {
  if (process.env.CONTRAST_REPORT) {
    it('prints the table', () => {
      for (const theme of ['light', 'dark'] as const)
        for (const pair of pairs) process.stderr.write(`${theme.padEnd(5)} ${measure(theme, pair).toFixed(2).padStart(6)}:1 (needs ${pair.min})  ${pair.what}: --${pair.fg} on ${pair.bg}\n`);
    });
  }

  for (const theme of ['light', 'dark'] as const)
    it(`every text and component pair meets its minimum in the ${theme} theme`, () => {
      const failures = pairs.filter((p) => measure(theme, p) < p.min).map((p) => `${measure(theme, p).toFixed(2)}:1 < ${p.min}:1  ${p.what} (--${p.fg} on ${p.bg})`);
      expect(failures).toEqual([]);
    });

  it('uses only colours of the ZTG palette for the text and focus tokens it changed', () => {
    // Readout may choose which palette token it uses, never a new colour (design-tokens.json is shared).
    const palette = new Set(Object.values(tokens).map((v) => v.replace(/\s+/g, '').toLowerCase()));
    expect(palette.has(themes.light.get('cm-mark')!.toLowerCase())).toBe(true);
    expect(palette.has(themes.dark.get('cm-mark')!.toLowerCase())).toBe(true);
    expect(palette.has(themes.light.get('hl-comment')!.toLowerCase())).toBe(true);
    expect(palette.has(themes.dark.get('hl-comment')!.toLowerCase())).toBe(true);
    expect(palette.has(themes.light.get('focus-dim')!.toLowerCase())).toBe(true);
    expect(palette.has(themes.dark.get('focus-dim')!.toLowerCase())).toBe(true);
  });
});

describe('contrast maths', () => {
  it('matches the WCAG reference values', () => {
    expect(ratio([0, 0, 0, 1], [255, 255, 255, 1])).toBeCloseTo(21, 5);
    expect(ratio([255, 255, 255, 1], [255, 255, 255, 1])).toBeCloseTo(1, 5);
    expect(ratio(parseColor('#767676'), parseColor('#ffffff'))).toBeCloseTo(4.54, 2);
  });
  it('composites alpha over the background', () => {
    expect(over(parseColor('rgba(0, 0, 0, 0.5)'), [255, 255, 255, 1])).toEqual([127.5, 127.5, 127.5, 1]);
    expect(parseColor('color(srgb 1 0 0 / 0.5)')).toEqual([255, 0, 0, 0.5]);
  });
  it('knows large text', () => {
    expect(isLarge(24, 400)).toBe(true);
    expect(isLarge(18.66, 700)).toBe(true);
    expect(isLarge(18.66, 400)).toBe(false);
    expect(isLarge(16, 700)).toBe(false);
  });
});
